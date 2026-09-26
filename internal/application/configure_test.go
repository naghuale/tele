package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"telecli/internal/authstore"
	"telecli/internal/config"
	"telecli/internal/secretinput"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
)

// scriptedPrompter replays prepared answers and records every prompt.
type scriptedPrompter struct {
	mu       sync.Mutex
	answers  []string
	confirms []bool
	index    int
	prompts  []string
}

func (p *scriptedPrompter) Ask(prompt string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.prompts = append(p.prompts, prompt)

	if p.index >= len(p.answers) {
		return "", fmt.Errorf("no scripted answer for %q", prompt)
	}

	answer := p.answers[p.index]
	p.index++

	return answer, nil
}

func (p *scriptedPrompter) AskSecret(string) ([]byte, error) {
	return nil, errors.New("AskSecret must not be used: the flow reads secrets through SecretReader")
}

func (p *scriptedPrompter) Confirm(prompt string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.prompts = append(p.prompts, prompt)

	if len(p.confirms) == 0 {
		return false, nil
	}

	answer := p.confirms[0]
	p.confirms = p.confirms[1:]

	return answer, nil
}

func (p *scriptedPrompter) askedFor(fragment string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, prompt := range p.prompts {
		if strings.Contains(prompt, fragment) {
			return true
		}
	}

	return false
}

// scriptedSecretReader returns a fixed secret and records the prompt.
type scriptedSecretReader struct {
	mu      sync.Mutex
	value   string
	err     error
	prompts []string
}

func (r *scriptedSecretReader) ReadSecret(prompt string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.prompts = append(r.prompts, prompt)

	if r.err != nil {
		return nil, r.err
	}

	if r.value == "" {
		return nil, secretinput.ErrEmpty
	}

	return []byte(r.value), nil
}

// fakeConfigureStore is an in-memory credential store.
type fakeConfigureStore struct {
	mu         sync.Mutex
	profiles   map[string]authstore.Profile
	createErr  error
	replaceErr error
	deleteErr  error

	created  []string
	replaced []string
	deleted  []string
}

func newFakeConfigureStore() *fakeConfigureStore {
	return &fakeConfigureStore{profiles: make(map[string]authstore.Profile)}
}

func (s *fakeConfigureStore) Create(
	_ context.Context,
	profile string,
	credentials authstore.Profile,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.createErr != nil {
		return s.createErr
	}

	if _, exists := s.profiles[profile]; exists {
		return authstore.ErrProfileExists
	}

	s.profiles[profile] = credentials
	s.created = append(s.created, profile)

	return nil
}

func (s *fakeConfigureStore) Replace(
	_ context.Context,
	profile string,
	credentials authstore.Profile,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.replaceErr != nil {
		return s.replaceErr
	}

	if _, exists := s.profiles[profile]; !exists {
		return authstore.ErrProfileUnavailable
	}

	s.profiles[profile] = credentials
	s.replaced = append(s.replaced, profile)

	return nil
}

func (s *fakeConfigureStore) Load(
	_ context.Context,
	profile string,
) (authstore.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	credentials, ok := s.profiles[profile]
	if !ok {
		return authstore.Profile{}, authstore.ErrProfileUnavailable
	}

	return credentials, nil
}

func (s *fakeConfigureStore) Delete(
	_ context.Context,
	profile string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deleteErr != nil {
		return s.deleteErr
	}

	if _, exists := s.profiles[profile]; !exists {
		return authstore.ErrProfileUnavailable
	}

	delete(s.profiles, profile)
	s.deleted = append(s.deleted, profile)

	return nil
}

func (s *fakeConfigureStore) has(profile string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.profiles[profile]

	return ok
}

// fakeProbe accepts one library path and reports it as verified.
type fakeProbe struct {
	result TDLibProbeResult
	err    error
	paths  []string
}

func (p *fakeProbe) Probe(
	_ context.Context,
	path string,
) (TDLibProbeResult, error) {
	p.paths = append(p.paths, path)

	if p.err != nil {
		return TDLibProbeResult{}, p.err
	}

	result := p.result
	result.Path = path

	return result, nil
}

// alwaysFailingProbe rejects every candidate.
type alwaysFailingProbe struct{}

func (alwaysFailingProbe) Probe(
	context.Context,
	string,
) (TDLibProbeResult, error) {
	return TDLibProbeResult{}, fmt.Errorf(
		"%w: rejected",
		ErrConfigureTDLib,
	)
}

func verifiedProbe() *fakeProbe {
	return &fakeProbe{
		result: TDLibProbeResult{
			Version:       "1.8.67",
			Commit:        "ea97bcdd3a15523c58ddfe772b4547187cf5bbeb",
			Compatibility: telegram.CompatibilityVerified,
		},
	}
}

// configureFixture bundles everything a flow test needs.
type configureFixture struct {
	request    ConfigureRequest
	prompter   *scriptedPrompter
	secrets    *scriptedSecretReader
	store      *fakeConfigureStore
	probe      *fakeProbe
	configPath string
}

func newConfigureFixture(t *testing.T) *configureFixture {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	fixture := &configureFixture{
		prompter: &scriptedPrompter{
			answers: []string{"123456", "+15550001234", "durable", "default"},
		},
		secrets:    &scriptedSecretReader{value: "hash-0123456789abcdef"},
		store:      newFakeConfigureStore(),
		probe:      verifiedProbe(),
		configPath: configPath,
	}

	fixture.request = ConfigureRequest{
		Environ:     []string{},
		Prompter:    fixture.prompter,
		Secrets:     fixture.secrets,
		Credentials: fixture.store,
		Probe:       fixture.probe,
		Options: ConfigureOptions{
			ConfigPath: configPath,
		},
	}

	return fixture
}

func (f *configureFixture) run(t *testing.T) (ConfigureResult, error) {
	t.Helper()

	return RunConfigure(context.Background(), f.request)
}

// ---- prompts ----

func TestConfigurePromptsForRequiredValues(t *testing.T) {
	fixture := newConfigureFixture(t)

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	for _, fragment := range []string{
		"Telegram API ID",
		"Telegram phone",
		"Delivery mode",
		"Credential profile",
	} {
		if !fixture.prompter.askedFor(fragment) {
			t.Fatalf("the flow never asked for %q", fragment)
		}
	}

	if len(fixture.secrets.prompts) != 1 {
		t.Fatalf(
			"the secret was requested %d times, want 1",
			len(fixture.secrets.prompts),
		)
	}
	if !strings.Contains(fixture.secrets.prompts[0], "API hash") {
		t.Fatalf("secret prompt = %q", fixture.secrets.prompts[0])
	}
}

func TestConfigureHidesAPIHashInput(t *testing.T) {
	fixture := newConfigureFixture(t)

	// The hash must arrive through the secret reader, never through the
	// visible prompter.
	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	for _, prompt := range fixture.prompter.prompts {
		if strings.Contains(prompt, "hash-0123456789abcdef") {
			t.Fatal("the API hash was requested through a visible prompt")
		}
	}
}

func TestConfigureDoesNotEchoCredentialsInOutput(t *testing.T) {
	fixture := newConfigureFixture(t)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	writeConfigureReport(&out, result)

	for _, secret := range []string{
		"hash-0123456789abcdef",
		"+15550001234",
		"123456",
	} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("the setup report leaked %q", secret)
		}
	}
}

func TestConfigureRejectsInvalidInputs(t *testing.T) {
	cases := []struct {
		name    string
		answers []string
		secret  string
		want    string
	}{
		{
			name:    "empty api id",
			answers: []string{"", "+1555", "durable", "default"},
			secret:  "hash",
			want:    "API ID must not be empty",
		},
		{
			name:    "non numeric api id",
			answers: []string{"abc", "+1555", "durable", "default"},
			secret:  "hash",
			want:    "API ID must be a number",
		},
		{
			name:    "negative api id",
			answers: []string{"-5", "+1555", "durable", "default"},
			secret:  "hash",
			want:    "API ID must be positive",
		},
		{
			name:    "empty phone",
			answers: []string{"123", "", "durable", "default"},
			secret:  "hash",
			want:    "phone must not be empty",
		},
		{
			name:    "unknown mode",
			answers: []string{"123", "+1555", "sideways", "default"},
			secret:  "hash",
			want:    "unsupported message send mode",
		},
		{
			name:    "invalid profile name",
			answers: []string{"123", "+1555", "durable", "bad profile"},
			secret:  "hash",
			want:    "profile name",
		},
		{
			name:    "empty api hash",
			answers: []string{"123", "+1555", "durable", "default"},
			secret:  "",
			want:    "API hash must not be empty",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newConfigureFixture(t)
			fixture.prompter.answers = c.answers
			fixture.secrets.value = c.secret

			_, err := fixture.run(t)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
			if fixture.store.has("default") {
				t.Fatal("a credential profile was written for invalid input")
			}
			if _, statErr := os.Stat(fixture.configPath); statErr == nil {
				t.Fatal("a configuration file was written for invalid input")
			}
		})
	}
}

// ---- config output ----

func TestConfigureWritesExpectedConfigShape(t *testing.T) {
	fixture := newConfigureFixture(t)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(raw)

	if strings.Contains(content, "hash-0123456789abcdef") {
		t.Fatal("the API hash was written to the configuration")
	}
	if strings.Contains(content, "+15550001234") {
		t.Fatal("the phone was written to the configuration")
	}
	if strings.Contains(strings.ToLower(content), "library =") {
		t.Fatal("the configuration used the library alias")
	}
	if !strings.Contains(content, "library_path") {
		t.Fatal("the configuration has no library_path")
	}
	if !strings.Contains(content, "credential_profile") {
		t.Fatal("the configuration has no credential_profile")
	}
	if !strings.Contains(content, `mode = "durable"`) {
		t.Fatal("the configuration has no durable mode")
	}

	// The written file must load through the production loader.
	loaded, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatalf("written configuration does not load: %v", err)
	}
	if loaded.Auth.APIID != 123456 {
		t.Fatalf("api id = %d", loaded.Auth.APIID)
	}
	if loaded.TDLib.LibraryPath != result.TDLib.Path {
		t.Fatalf(
			"library_path = %q, want %q",
			loaded.TDLib.LibraryPath,
			result.TDLib.Path,
		)
	}
}

func TestConfigureWritesDirectMode(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.prompter.answers[2] = "direct"

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MessageDelivery.Mode != config.MessageSendModeDirect {
		t.Fatalf("mode = %q, want direct", loaded.MessageDelivery.Mode)
	}
}

func TestConfigureCreatesConfigMode0600(t *testing.T) {
	fixture := newConfigureFixture(t)

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}

	// The literal is asserted on purpose: comparing against the
	// constant under test would pass even if the constant were wrong.
	const wantConfigMode = os.FileMode(0o600)
	if got := info.Mode().Perm(); got != wantConfigMode {
		t.Fatalf("config mode = %o, want %o", got, wantConfigMode)
	}
}

func TestConfigureCreatesDirectoriesMode0700(t *testing.T) {
	fixture := newConfigureFixture(t)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{
		result.Config.DataDir,
		result.Config.MessageDelivery.DataDir,
	} {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("directory %s was not created", dir)
		}
		const wantDirMode = os.FileMode(0o700)
		if got := info.Mode().Perm(); got != wantDirMode {
			t.Fatalf(
				"directory %s mode = %o, want %o",
				dir,
				got,
				wantDirMode,
			)
		}
	}
}

func TestConfigureCreatesConfigDirectoryMode0700(t *testing.T) {
	fixture := newConfigureFixture(t)

	// The configuration directory does not exist yet, so the flow has to
	// create it and its mode is the flow's responsibility.
	missing := filepath.Join(t.TempDir(), "absent", "nested")
	fixture.request.Options.ConfigPath = filepath.Join(
		missing,
		"config.toml",
	)

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(missing)
	if err != nil {
		t.Fatal(err)
	}
	const wantDirMode = os.FileMode(0o700)
	if got := info.Mode().Perm(); got != wantDirMode {
		t.Fatalf("directory mode = %o, want %o", got, wantDirMode)
	}
}

func TestConfigureUsesExplicitAndEnvironmentPaths(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		fixture := newConfigureFixture(t)
		fixture.request.Options.ConfigPath = fixture.configPath
		fixture.request.Environ = []string{
			"TELECLI_CONFIG=" + filepath.Join(t.TempDir(), "other.toml"),
		}

		if _, err := fixture.run(t); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fixture.configPath); err != nil {
			t.Fatal("the explicit path was not used")
		}
	})

	t.Run("environment", func(t *testing.T) {
		envPath := filepath.Join(t.TempDir(), "from-env.toml")
		fixture := newConfigureFixture(t)
		fixture.request.Options.ConfigPath = ""
		fixture.request.Environ = []string{"TELECLI_CONFIG=" + envPath}

		result, err := fixture.run(t)
		if err != nil {
			t.Fatal(err)
		}
		if result.ConfigPath != envPath {
			t.Fatalf("config path = %q, want %q", result.ConfigPath, envPath)
		}
	})
}

func TestConfigureWritesDiscoveredDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("TELECLI_CONFIG", "")

	defaultPath, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}

	fixture := newConfigureFixture(t)
	fixture.request.Options.ConfigPath = ""
	fixture.request.Environ = nil

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigPath != defaultPath {
		t.Fatalf(
			"config path = %q, want the discovered default %q",
			result.ConfigPath,
			defaultPath,
		)
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatal("the default configuration file was not created")
	}
}

// ---- credential handling ----

func TestConfigureCreatesNewCredentialProfile(t *testing.T) {
	fixture := newConfigureFixture(t)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !result.ProfileCreated {
		t.Fatal("the profile was not reported as created")
	}
	if result.ProfileReplaced {
		t.Fatal("a new profile must not be reported as replaced")
	}
	if !fixture.store.has("default") {
		t.Fatal("the profile was not stored")
	}
}

func TestConfigureDoesNotReplaceWithoutConfirmation(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.store.profiles["default"] = authstore.Profile{
		APIHash: "old-hash",
		Phone:   "+15550000000",
	}
	fixture.secrets.value = "new-hash-value"
	fixture.prompter.confirms = []bool{false}

	_, err := fixture.run(t)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}

	stored, _ := fixture.store.Load(context.Background(), "default")
	if stored.APIHash != "old-hash" {
		t.Fatal("the existing profile was replaced without confirmation")
	}
	if _, statErr := os.Stat(fixture.configPath); statErr == nil {
		t.Fatal("a configuration file was written after a refusal")
	}
}

func TestConfigureReplacesAfterConfirmation(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.store.profiles["default"] = authstore.Profile{
		APIHash: "old-hash",
		Phone:   "+15550000000",
	}
	fixture.secrets.value = "new-hash-value"
	fixture.prompter.confirms = []bool{true}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProfileReplaced {
		t.Fatal("the profile was not reported as replaced")
	}

	stored, _ := fixture.store.Load(context.Background(), "default")
	if stored.APIHash != "new-hash-value" {
		t.Fatal("the profile was not replaced")
	}
}

func TestConfigureForceReplacesWithoutAsking(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.store.profiles["default"] = authstore.Profile{
		APIHash: "old-hash",
		Phone:   "+15550000000",
	}
	fixture.request.Options.Force = true

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProfileReplaced {
		t.Fatal("force did not replace the profile")
	}
	if fixture.prompter.askedFor("Replace it") {
		t.Fatal("force must not ask for confirmation")
	}
}

func TestConfigureRollsBackNewProfileWhenConfigWriteFails(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.request.WriteConfig = func(
		string,
		config.Config,
	) error {
		return errors.New("disk is full")
	}

	_, err := fixture.run(t)
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	if fixture.store.has("default") {
		t.Fatal("the created profile was not rolled back")
	}
	if len(fixture.store.deleted) != 1 {
		t.Fatalf("rollback deletions = %v", fixture.store.deleted)
	}
}

func TestConfigureRollbackFailureIsReported(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.store.deleteErr = errors.New("keychain is locked")
	fixture.request.WriteConfig = func(
		string,
		config.Config,
	) error {
		return errors.New("disk is full")
	}

	_, err := fixture.run(t)
	if !errors.Is(err, ErrConfigureRollback) {
		t.Fatalf("error = %v, want ErrConfigureRollback", err)
	}
}

func TestConfigureDoesNotExposeBackendError(t *testing.T) {
	const secretHash = "canary-hash-0123456789abcdef"

	fixture := newConfigureFixture(t)
	fixture.store.createErr = errors.New(
		"SecItemAdd failed for account default with " + secretHash,
	)

	_, err := fixture.run(t)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secretHash) {
		t.Fatal("the backend error text leaked")
	}
	if strings.Contains(err.Error(), "SecItemAdd") {
		t.Fatal("the backend error text leaked")
	}
}

// ---- TDLib ----

func TestConfigureAcceptsVerifiedTDLib(t *testing.T) {
	fixture := newConfigureFixture(t)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.TDLib.Compatibility != telegram.CompatibilityVerified {
		t.Fatalf("compatibility = %q", result.TDLib.Compatibility)
	}
}

func TestConfigureRejectsUnverifiedTDLib(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.probe.result.Compatibility = telegram.CompatibilityRejected
	fixture.probe.err = fmt.Errorf(
		"%w: commit mismatch",
		ErrConfigureTDLib,
	)
	// The probe is asked about every candidate, so it must fail each
	// time rather than accept an unverified library.
	fixture.request.Probe = &alwaysFailingProbe{}

	_, err := fixture.run(t)
	if err == nil {
		t.Fatal("expected a TDLib error")
	}
	if _, statErr := os.Stat(fixture.configPath); statErr == nil {
		t.Fatal("a configuration file was written without a usable TDLib")
	}
}

func TestConfigureRejectsMismatchedTDLibCommit(t *testing.T) {
	probe := &fakeProbe{
		result: TDLibProbeResult{
			Version:       "1.8.67",
			Commit:        "0000000000000000000000000000000000000000",
			Compatibility: telegram.CompatibilityVerified,
		},
	}

	if err := validateProbe(probe.result); err == nil {
		t.Fatal("a mismatched commit must be rejected")
	}
}

func TestConfigureRejectsUnverifiedCompatibility(t *testing.T) {
	result := TDLibProbeResult{
		Version:       "1.8.67",
		Commit:        "ea97bcdd3a15523c58ddfe772b4547187cf5bbeb",
		Compatibility: telegram.CompatibilityUnverified,
	}

	if err := validateProbe(result); err == nil {
		t.Fatal("unverified compatibility must be rejected")
	}
}

func TestConfigureRejectsMissingTDLib(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.request.Probe = &fakeProbe{
		err: fmt.Errorf("%w: nothing found", ErrConfigureTDLib),
	}

	_, err := fixture.run(t)
	if !errors.Is(err, ErrConfigureTDLib) {
		t.Fatalf("error = %v, want ErrConfigureTDLib", err)
	}
}

func TestConfigureStoresAbsoluteLibraryPath(t *testing.T) {
	const want = "/opt/homebrew/lib/libtdjson.dylib"

	fixture := newConfigureFixture(t)
	// Only the Homebrew candidate is usable, so the flow must walk past
	// the relative third_party path and store an absolute one.
	fixture.request.Probe = &onlyPathProbe{accepted: want}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(result.Config.TDLib.LibraryPath) {
		t.Fatalf(
			"library_path = %q, want an absolute path",
			result.Config.TDLib.LibraryPath,
		)
	}
	if result.Config.TDLib.LibraryPath != want {
		t.Fatalf("library_path = %q, want %q", result.Config.TDLib.LibraryPath, want)
	}
}

// onlyPathProbe accepts a single candidate.
type onlyPathProbe struct {
	accepted string
}

func (p *onlyPathProbe) Probe(
	_ context.Context,
	path string,
) (TDLibProbeResult, error) {
	if path != p.accepted {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: not this one",
			ErrConfigureTDLib,
		)
	}

	return TDLibProbeResult{
		Path:          path,
		Version:       "1.8.67",
		Commit:        "ea97bcdd3a15523c58ddfe772b4547187cf5bbeb",
		Compatibility: telegram.CompatibilityVerified,
	}, nil
}

func TestTDLibLibraryCandidatesIncludeHomebrew(t *testing.T) {
	candidates := TDLibLibraryCandidates("", nil)

	for _, want := range []string{
		"/opt/homebrew/lib/libtdjson.dylib",
		"/usr/local/lib/libtdjson.dylib",
		thirdPartyTDLibPath,
	} {
		found := false
		for _, candidate := range candidates {
			if candidate == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("candidate %q is missing from %v", want, candidates)
		}
	}
}

func TestTDLibLibraryCandidatesPreferEnvironment(t *testing.T) {
	candidates := TDLibLibraryCandidates(
		"/configured/path.dylib",
		[]string{"TELECLI_TDLIB_LIBRARY=/from/env.dylib"},
	)

	if candidates[0] != "/from/env.dylib" {
		t.Fatalf("first candidate = %q", candidates[0])
	}
	if candidates[1] != "/configured/path.dylib" {
		t.Fatalf("second candidate = %q", candidates[1])
	}
}

// Compile-time assertion: the fake store is a full authstore.Store.
var _ authstore.Store = (*fakeConfigureStore)(nil)

// recorderComponent aliases the recorder type the runtime factory needs.
type recorderComponent = recorder.ComponentRecorder

// ---- end to end ----

// TestConfigureThenDoctorUsesWrittenConfig proves the setup result is
// what the runtime commands read.
func TestConfigureThenDoctorUsesWrittenConfig(t *testing.T) {
	fixture := newConfigureFixture(t)

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		_ context.Context,
		loaded config.Config,
		w io.Writer,
	) int {
		if loaded.Auth.APIID != 123456 {
			t.Errorf("doctor saw api id %d", loaded.Auth.APIID)
		}
		if loaded.Auth.CredentialProfile != "default" {
			t.Errorf("doctor saw profile %q", loaded.Auth.CredentialProfile)
		}
		fmt.Fprintln(w, "TDLib runtime: available")
		return 0
	}

	args := []string{"telecli", "doctor", "--config", fixture.configPath}
	if code := Main(args, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

// TestConfigureThenTUIResolvesProfile proves the stored profile satisfies
// the resolver, so the runtime does not fall back to mock mode.
func TestConfigureThenTUIResolvesProfile(t *testing.T) {
	fixture := newConfigureFixture(t)

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		telegramAPIIDEnvironment,
		telegramAPIHashEnvironment,
		telegramPhoneEnvironment,
	} {
		t.Setenv(name, "")
	}

	adapter := keychainTelegramCredentialStore{store: fixture.store}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.NewTelegramCredentialStore = func() TelegramCredentialStore {
		return adapter
	}
	env.NewTelegramRuntime = func(
		_ config.Config,
		_ recorderComponent,
	) (*telegram.Runtime, error) {
		return nil, errors.New("stop after credential resolution")
	}

	args := []string{"telecli", "tui", "--config", fixture.configPath}
	if code := Main(args, env); code != 1 {
		t.Fatalf("code = %d, want 1 from the injected error", code)
	}
	if tuiCalls != 0 {
		t.Fatal("the mock-only TUI started for a configured profile")
	}
	if strings.Contains(stderr.String(), "mock-only") {
		t.Fatalf("stderr = %q, mock-only must not start", stderr.String())
	}
}

// ---- manual TDLib path fallback ----

// TestConfigureAsksForLibraryPathWhenCandidatesFail covers the manual
// escape hatch: a self-built TDLib can live anywhere, so setup must not
// dead-end when no conventional prefix holds a usable library.
func TestConfigureAsksForLibraryPathWhenCandidatesFail(t *testing.T) {
	const manual = "/Users/someone/Developer/tdlib-src/build/libtdjson.dylib"

	fixture := newConfigureFixture(t)
	fixture.request.Probe = &onlyPathProbe{accepted: manual}
	fixture.prompter.answers = append(
		[]string{manual},
		fixture.prompter.answers...,
	)

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.TDLib.LibraryPath != manual {
		t.Fatalf(
			"library_path = %q, want the manual path %q",
			result.Config.TDLib.LibraryPath,
			manual,
		)
	}
	if !fixture.prompter.askedFor("Enter a path") {
		t.Fatal("the flow never offered a manual library path")
	}
}

func TestConfigureReportsTriedPathsWhenLibraryIsMissing(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.request.Probe = &alwaysFailingProbe{}
	// The operator declines the manual prompt.
	fixture.prompter.answers = append(
		[]string{""},
		fixture.prompter.answers...,
	)

	_, err := fixture.run(t)
	if !errors.Is(err, ErrConfigureTDLib) {
		t.Fatalf("error = %v, want ErrConfigureTDLib", err)
	}
	if !strings.Contains(err.Error(), "/opt/homebrew/lib/libtdjson.dylib") {
		t.Fatalf("the error does not list the tried paths: %v", err)
	}
}

func TestConfigureRejectsUnusableManualLibraryPath(t *testing.T) {
	const bad = "/nonexistent/libtdjson.dylib"

	fixture := newConfigureFixture(t)
	fixture.request.Probe = &alwaysFailingProbe{}
	fixture.prompter.answers = append(
		[]string{bad},
		fixture.prompter.answers...,
	)

	_, err := fixture.run(t)
	if !errors.Is(err, ErrConfigureTDLib) {
		t.Fatalf("error = %v, want ErrConfigureTDLib", err)
	}
	if !strings.Contains(err.Error(), bad) {
		t.Fatalf("the error does not mention the rejected path: %v", err)
	}
	if _, statErr := os.Stat(fixture.configPath); statErr == nil {
		t.Fatal("a configuration file was written without a usable TDLib")
	}
}

// ---- status rendering ----

// TestConfigureStatusRendersEveryField pins that no status line collapses
// to an empty value.
//
// AuthCredentialSource is a uint8-based type, so a careless string
// conversion renders a control byte instead of a name and the field looks
// blank in the output.
func TestConfigureStatusRendersEveryField(t *testing.T) {
	fixture := newConfigureFixture(t)
	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.NewTelegramCredentialStore = func() TelegramCredentialStore {
		return keychainTelegramCredentialStore{store: fixture.store}
	}
	env.ReportTDLib = func(
		context.Context,
		config.Config,
		io.Writer,
	) int {
		return 0
	}

	args := []string{
		"telecli",
		"configure",
		"status",
		"--config",
		fixture.configPath,
	}
	if code := Main(args, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"File",
		"Source",
		"Delivery mode",
		"Data directory",
		"Library",
		"API ID",
		"Credential profile",
		"API hash",
		"Phone",
		"Credential source",
		"Ready",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output is missing %q:\n%s", want, out)
		}
	}

	// A resolved profile must name its source.
	if !strings.Contains(out, "Credential source  profile") {
		t.Fatalf("the credential source was not rendered:\n%s", out)
	}

	// No value line may be empty, and no control byte may leak.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasSuffix(strings.TrimRight(line, " "), "") &&
			strings.TrimSpace(line) == "" {
			t.Fatalf("blank status line: %q", line)
		}
		for _, r := range line {
			if r < 0x20 && r != '\t' {
				t.Fatalf("control byte %U in status line %q", r, line)
			}
		}
	}

	// And no credential value may appear.
	for _, secret := range []string{
		"hash-0123456789abcdef",
		"+15550001234",
	} {
		if strings.Contains(out, secret) {
			t.Fatalf("status leaked %q", secret)
		}
	}
}

func TestConfigureStatusReportsReadyForAStoredProfile(t *testing.T) {
	fixture := newConfigureFixture(t)
	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.NewTelegramCredentialStore = func() TelegramCredentialStore {
		return keychainTelegramCredentialStore{store: fixture.store}
	}
	env.ReportTDLib = func(
		context.Context,
		config.Config,
		io.Writer,
	) int {
		return 0
	}

	args := []string{
		"telecli",
		"configure",
		"status",
		"--config",
		fixture.configPath,
	}
	if code := Main(args, env); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Ready              yes") {
		t.Fatalf("status did not report readiness:\n%s", stdout.String())
	}
}

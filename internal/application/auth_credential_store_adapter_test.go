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
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
)

// recordingAuthStore serves profiles from memory and records the calls it
// receives, so tests can prove the store is only consulted when the
// resolution matrix allows it.
type recordingAuthStore struct {
	mu        sync.Mutex
	profiles  map[string]TelegramProfileCredentials
	err       error
	callCount int
}

func newRecordingAuthStore() *recordingAuthStore {
	return &recordingAuthStore{profiles: make(map[string]TelegramProfileCredentials)}
}

func (s *recordingAuthStore) LoadTelegramCredentials(
	_ context.Context,
	profile string,
) (TelegramProfileCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.callCount++

	if s.err != nil {
		return TelegramProfileCredentials{}, s.err
	}

	credentials, ok := s.profiles[profile]
	if !ok {
		return TelegramProfileCredentials{}, authstore.ErrProfileUnavailable
	}

	return credentials, nil
}

func (s *recordingAuthStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.callCount
}

// writeAuthConfig writes a configuration selecting a credential profile.
func writeAuthConfig(
	t *testing.T,
	apiID int,
	profile string,
) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	body := "log_level = \"info\"\n" +
		"[auth]\n" +
		fmt.Sprintf("api_id = %d\n", apiID) +
		fmt.Sprintf("credential_profile = %q\n", profile)

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TELECLI_CONFIG", path)
}

// injectStore points the environment at a fake credential store.
func injectStore(env *Environment, store TelegramCredentialStore) {
	env.NewTelegramCredentialStore = func() TelegramCredentialStore {
		return store
	}
}

// ---- adapter ----

// failingAuthStore is an authstore.Store whose every operation fails, so
// the adapter can be tested against each backend sentinel.
type failingAuthStore struct{ err error }

func (s failingAuthStore) Load(
	context.Context,
	string,
) (authstore.Profile, error) {
	return authstore.Profile{}, s.err
}

func (s failingAuthStore) Create(
	context.Context,
	string,
	authstore.Profile,
) error {
	return s.err
}

func (s failingAuthStore) Replace(
	context.Context,
	string,
	authstore.Profile,
) error {
	return s.err
}

func (s failingAuthStore) Delete(context.Context, string) error {
	return s.err
}

// Compile-time assertion: failingAuthStore implements authstore.Store.
var _ authstore.Store = failingAuthStore{}

// failingCredentialStore is the application-level view of a store that
// cannot be reached.
type failingCredentialStore struct{ err error }

func (s failingCredentialStore) LoadTelegramCredentials(
	context.Context,
	string,
) (TelegramProfileCredentials, error) {
	return TelegramProfileCredentials{}, s.err
}

func TestCredentialStoreAdapterMapsBackendSentinels(t *testing.T) {
	cases := []struct {
		name    string
		backend error
		want    error
	}{
		{
			name:    "profile unavailable",
			backend: authstore.ErrProfileUnavailable,
			want:    ErrTelegramCredentialProfileUnavailable,
		},
		{
			name:    "store unavailable",
			backend: authstore.ErrStoreUnavailable,
			want:    ErrTelegramCredentialProfileUnavailable,
		},
		{
			name:    "invalid profile",
			backend: authstore.ErrInvalidProfile,
			want:    ErrTelegramCredentialsInvalid,
		},
		{
			name:    "profile exists",
			backend: authstore.ErrProfileExists,
			want:    ErrTelegramCredentialsInvalid,
		},
		{
			name:    "unknown backend error",
			backend: errors.New("something else"),
			want:    ErrTelegramCredentialProfileUnavailable,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter := keychainTelegramCredentialStore{
				store: failingAuthStore{err: c.backend},
			}

			_, err := adapter.LoadTelegramCredentials(
				context.Background(),
				"default",
			)
			if !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
}

// TestCredentialStoreAdapterDoesNotExposeBackendError pins that the
// platform message never reaches the application surface.
func TestCredentialStoreAdapterDoesNotExposeBackendError(t *testing.T) {
	const (
		secretHash  = "canary-hash-0123456789abcdef"
		secretPhone = "+15550009999"
	)

	backend := errors.New(
		"SecItem failed for account " + secretPhone +
			" with data " + secretHash,
	)

	adapter := keychainTelegramCredentialStore{
		store: failingAuthStore{err: backend},
	}

	_, err := adapter.LoadTelegramCredentials(
		context.Background(),
		"default",
	)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secretPhone) {
		t.Fatal("the adapter leaked the account attribute")
	}
	if strings.Contains(err.Error(), secretHash) {
		t.Fatal("the adapter leaked credential data")
	}
	if strings.Contains(err.Error(), "SecItem") {
		t.Fatal("the adapter leaked a backend message")
	}
}

func TestNewTelegramCredentialStoreIsUsable(t *testing.T) {
	store := NewTelegramCredentialStore()
	if store == nil {
		t.Fatal("NewTelegramCredentialStore returned nil")
	}
}

// ---- environment does not touch the store ----

func TestTUIRejectsPartialEnvironmentWithoutLoadingKeychain(t *testing.T) {
	clearTelegramEnvironment(t)
	t.Setenv(telegramAPIHashEnvironment, "orphan-hash")

	store := newRecordingAuthStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash",
		Phone:   "+15552220000",
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)

	if code := Main([]string{"telecli", "tui"}, env); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if got := store.calls(); got != 0 {
		t.Fatalf("credential store was consulted %d times, want 0", got)
	}
}

func TestTUICompleteEnvironmentDoesNotLoadKeychain(t *testing.T) {
	clearTelegramEnvironment(t)
	setCompleteEnvironment(t)

	store := newRecordingAuthStore()

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)
	env.NewTelegramRuntime = func(
		config.Config,
		recorder.ComponentRecorder,
	) (*telegram.Runtime, error) {
		return nil, errors.New("stop after resolution")
	}

	if code := Main([]string{"telecli", "tui"}, env); code != 1 {
		t.Fatalf("code = %d, want 1 from the injected error", code)
	}
	if got := store.calls(); got != 0 {
		t.Fatalf("credential store was consulted %d times, want 0", got)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times", tuiCalls)
	}
}

// ---- profile resolution through the wiring ----

func TestTUIUsesProfileWhenEnvironmentAbsent(t *testing.T) {
	clearTelegramEnvironment(t)
	writeAuthConfig(t, 4242, "default")

	store := newRecordingAuthStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash-0123456789abcd",
		Phone:   "+15553330000",
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)

	var gotParams telegram.TdlibParameters
	var callsBeforeFactory int
	env.NewTelegramRuntime = func(
		cfg config.Config,
		_ recorder.ComponentRecorder,
	) (*telegram.Runtime, error) {
		// Count what the wiring itself consumed, before this test
		// resolves again for its own assertions.
		callsBeforeFactory = store.calls()

		resolver := NewTelegramCredentialResolver(store)
		resolved, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			cfg.Auth,
		)
		if err != nil {
			return nil, err
		}
		if resolved.Source != AuthCredentialSourceProfile {
			return nil, fmt.Errorf(
				"source = %v, want profile",
				resolved.Source,
			)
		}

		gotParams, err = TdlibParametersFromEnv(cfg, resolved.Credentials)
		if err != nil {
			return nil, err
		}

		return nil, errors.New("stop after credential resolution")
	}

	if code := Main([]string{"telecli", "tui"}, env); code != 1 {
		t.Fatalf("code = %d, want 1 from the injected error", code)
	}
	if callsBeforeFactory != 1 {
		t.Fatalf(
			"credential store was consulted %d times by the wiring, want 1",
			callsBeforeFactory,
		)
	}
	if gotParams.APIID != 4242 {
		t.Fatalf("api id = %d, want 4242 from the config", gotParams.APIID)
	}
	if gotParams.APIHash != "profile-hash-0123456789abcd" {
		t.Fatal("the runtime did not receive the profile hash")
	}
}

func TestTUIRejectsMissingConfiguredProfile(t *testing.T) {
	clearTelegramEnvironment(t)
	writeAuthConfig(t, 4242, "default")

	store := newRecordingAuthStore()

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)

	code := Main([]string{"telecli", "tui"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for a missing profile", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times, want 0", tuiCalls)
	}
	if strings.Contains(stderr.String(), "mock-only") {
		t.Fatalf("stderr = %q, mock-only must not start", stderr.String())
	}
}

func TestTUIDoesNotStartMockModeForKeychainFailure(t *testing.T) {
	clearTelegramEnvironment(t)
	writeAuthConfig(t, 4242, "default")

	store := failingCredentialStore{err: authstore.ErrStoreUnavailable}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)

	code := Main([]string{"telecli", "tui"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for a store failure", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times, want 0", tuiCalls)
	}
	if strings.Contains(stderr.String(), "mock-only") {
		t.Fatalf("stderr = %q, mock-only must not start", stderr.String())
	}
}

// ---- doctor ----

func TestDoctorReportsProfileSourceWithoutSecretValues(t *testing.T) {
	const (
		secretHash  = "doctor-profile-hash-0123456789"
		secretPhone = "+15550007777"
	)

	clearTelegramEnvironment(t)
	writeAuthConfig(t, 4242, "default")

	store := newRecordingAuthStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: secretHash,
		Phone:   secretPhone,
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)
	env.ReportTDLib = func(
		context.Context,
		config.Config,
		io.Writer,
	) int {
		return 0
	}

	if code := Main([]string{"telecli", "doctor"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "Auth configuration: ready") {
		t.Fatalf("doctor output = %q", out)
	}
	if !strings.Contains(out, "Auth source: profile") {
		t.Fatalf("doctor output = %q, want the profile source", out)
	}
	for _, secret := range []string{secretHash, secretPhone, "4242"} {
		if strings.Contains(out, secret) {
			t.Fatal("doctor output leaked a credential or config value")
		}
	}
}

// ---- env-absent, store-unavailable path ----

// TestCompleteEnvironmentDoesNotRequirePlatformStore pins that a machine
// without a credential store can still run with a complete environment.
func TestCompleteEnvironmentDoesNotRequirePlatformStore(t *testing.T) {
	clearTelegramEnvironment(t)
	setCompleteEnvironment(t)

	store := failingCredentialStore{err: authstore.ErrStoreUnavailable}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	injectStore(&env, store)

	var reached bool
	env.NewTelegramRuntime = func(
		config.Config,
		recorder.ComponentRecorder,
	) (*telegram.Runtime, error) {
		reached = true
		return nil, errors.New("stop after resolution")
	}

	if code := Main([]string{"telecli", "tui"}, env); code != 1 {
		t.Fatalf("code = %d, want 1 from the injected error", code)
	}
	if !reached {
		t.Fatal("the runtime must be reachable without a credential store")
	}
}

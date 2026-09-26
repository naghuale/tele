package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
)

// fakeTelegramCredentialStore serves profiles from memory and records how
// often it was consulted, so tests can prove the store is never touched
// when it must not be.
type fakeTelegramCredentialStore struct {
	mu       sync.Mutex
	profiles map[string]TelegramProfileCredentials
	err      error
	calls    []string
}

func newFakeTelegramCredentialStore() *fakeTelegramCredentialStore {
	return &fakeTelegramCredentialStore{
		profiles: make(map[string]TelegramProfileCredentials),
	}
}

func (f *fakeTelegramCredentialStore) LoadTelegramCredentials(
	_ context.Context,
	profile string,
) (TelegramProfileCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, profile)

	if f.err != nil {
		return TelegramProfileCredentials{}, f.err
	}

	secrets, ok := f.profiles[profile]
	if !ok {
		return TelegramProfileCredentials{}, errors.New(
			"no such profile",
		)
	}

	return secrets, nil
}

func (f *fakeTelegramCredentialStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

// clearTelegramEnvironment removes every credential variable so a test
// starts from a known state.
func clearTelegramEnvironment(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		telegramAPIIDEnvironment,
		telegramAPIHashEnvironment,
		telegramPhoneEnvironment,
	} {
		t.Setenv(name, "")
	}
}

func setCompleteEnvironment(t *testing.T) {
	t.Helper()

	t.Setenv(telegramAPIIDEnvironment, "12345")
	t.Setenv(telegramAPIHashEnvironment, "env-hash")
	t.Setenv(telegramPhoneEnvironment, "+15551110000")
}

func completeAuthConfig() config.AuthConfig {
	return config.AuthConfig{
		APIID:             777,
		CredentialProfile: "default",
	}
}

// ---- complete environment ----

func TestTelegramCredentialResolverUsesCompleteEnvironment(t *testing.T) {
	clearTelegramEnvironment(t)
	setCompleteEnvironment(t)

	store := newFakeTelegramCredentialStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash",
		Phone:   "+15552220000",
	}

	resolver := NewTelegramCredentialResolver(store)
	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if !resolved.Ready() {
		t.Fatalf("availability = %v, want ready", resolved.Availability)
	}
	if resolved.Source != AuthCredentialSourceEnvironment {
		t.Fatalf("source = %v, want environment", resolved.Source)
	}
	if resolved.Credentials.APIID != 12345 {
		t.Fatalf("api id = %d, want 12345", resolved.Credentials.APIID)
	}
	if resolved.Credentials.APIHash != "env-hash" {
		t.Fatal("the environment hash must win")
	}
	if resolved.Credentials.Phone != "+15551110000" {
		t.Fatal("the environment phone must win")
	}
}

func TestTelegramCredentialResolverDoesNotLoadProfileForCompleteEnvironment(
	t *testing.T,
) {
	clearTelegramEnvironment(t)
	setCompleteEnvironment(t)

	store := newFakeTelegramCredentialStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash",
		Phone:   "+15552220000",
	}

	resolver := NewTelegramCredentialResolver(store)
	if _, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	); err != nil {
		t.Fatal(err)
	}

	if got := store.callCount(); got != 0 {
		t.Fatalf("credential store was consulted %d times, want 0", got)
	}
}

func TestTelegramCredentialResolverRejectsInvalidEnvironmentAPIID(t *testing.T) {
	for _, apiID := range []string{"0", "-5", "not-a-number"} {
		clearTelegramEnvironment(t)
		setCompleteEnvironment(t)
		t.Setenv(telegramAPIIDEnvironment, apiID)

		store := newFakeTelegramCredentialStore()
		resolver := NewTelegramCredentialResolver(store)

		resolved, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			config.AuthConfig{},
		)
		if !errors.Is(err, ErrTelegramCredentialsInvalid) {
			t.Fatalf(
				"api id %q: error = %v, want ErrTelegramCredentialsInvalid",
				apiID,
				err,
			)
		}
		if resolved.Availability != AuthAvailabilityInvalid {
			t.Fatalf(
				"api id %q: availability = %v, want invalid",
				apiID,
				resolved.Availability,
			)
		}
		if got := store.callCount(); got != 0 {
			t.Fatalf("credential store was consulted %d times", got)
		}
	}
}

// ---- partial environment ----

func TestTelegramCredentialResolverRejectsPartialEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		hash  string
		phone string
	}{
		{"only id", "1", "", ""},
		{"only hash", "", "h", ""},
		{"only phone", "", "", "+1"},
		{"id and hash", "1", "h", ""},
		{"id and phone", "1", "", "+1"},
		{"hash and phone", "", "h", "+1"},
		{"whitespace id with hash", "  ", "h", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearTelegramEnvironment(t)
			t.Setenv(telegramAPIIDEnvironment, c.id)
			t.Setenv(telegramAPIHashEnvironment, c.hash)
			t.Setenv(telegramPhoneEnvironment, c.phone)

			// A complete profile is available and must NOT be used.
			store := newFakeTelegramCredentialStore()
			store.profiles["default"] = TelegramProfileCredentials{
				APIHash: "profile-hash",
				Phone:   "+15552220000",
			}

			resolver := NewTelegramCredentialResolver(store)
			resolved, err := resolver.ResolveTelegramCredentials(
				context.Background(),
				completeAuthConfig(),
			)

			if !errors.Is(err, ErrTelegramCredentialsInvalid) {
				t.Fatalf("error = %v, want ErrTelegramCredentialsInvalid", err)
			}
			if resolved.Availability != AuthAvailabilityInvalid {
				t.Fatalf(
					"availability = %v, want invalid",
					resolved.Availability,
				)
			}
			if got := store.callCount(); got != 0 {
				t.Fatalf(
					"credential store was consulted %d times after a "+
						"partial environment, want 0",
					got,
				)
			}
		})
	}
}

// TestTelegramCredentialResolverTreatsWhitespaceAsUnset pins the
// documented rule that empty and whitespace-only values count as unset,
// so they cannot create a partial environment.
func TestTelegramCredentialResolverTreatsWhitespaceAsUnset(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		hash  string
		phone string
	}{
		{"whitespace id", "  ", "", ""},
		{"whitespace hash", "", "\t", ""},
		{"whitespace phone", "", "", "  "},
		{"all whitespace", " ", " ", " "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearTelegramEnvironment(t)
			t.Setenv(telegramAPIIDEnvironment, c.id)
			t.Setenv(telegramAPIHashEnvironment, c.hash)
			t.Setenv(telegramPhoneEnvironment, c.phone)

			store := newFakeTelegramCredentialStore()
			resolver := NewTelegramCredentialResolver(store)

			resolved, err := resolver.ResolveTelegramCredentials(
				context.Background(),
				config.AuthConfig{},
			)
			if err != nil {
				t.Fatalf("whitespace must be treated as unset: %v", err)
			}
			if resolved.Availability != AuthAvailabilityAbsent {
				t.Fatalf(
					"availability = %v, want absent",
					resolved.Availability,
				)
			}
			if got := store.callCount(); got != 0 {
				t.Fatalf("credential store was consulted %d times", got)
			}
		})
	}
}

func TestTelegramCredentialResolverDoesNotFallbackAfterPartialEnvironment(
	t *testing.T,
) {
	clearTelegramEnvironment(t)
	t.Setenv(telegramAPIHashEnvironment, "orphan-hash")

	store := newFakeTelegramCredentialStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash",
		Phone:   "+15552220000",
	}

	resolver := NewTelegramCredentialResolver(store)
	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	)
	if err == nil {
		t.Fatal("a partial environment must not fall back to the profile")
	}
	if resolved.Credentials.APIHash == "profile-hash" {
		t.Fatal("credentials must not be completed from the profile")
	}
}

// ---- profile ----

func TestTelegramCredentialResolverUsesProfileWhenEnvironmentAbsent(
	t *testing.T,
) {
	clearTelegramEnvironment(t)

	store := newFakeTelegramCredentialStore()
	store.profiles["default"] = TelegramProfileCredentials{
		APIHash: "profile-hash",
		Phone:   "+15552220000",
	}

	resolver := NewTelegramCredentialResolver(store)
	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if !resolved.Ready() {
		t.Fatalf("availability = %v, want ready", resolved.Availability)
	}
	if resolved.Source != AuthCredentialSourceProfile {
		t.Fatalf("source = %v, want profile", resolved.Source)
	}
	if resolved.Credentials.APIID != 777 {
		t.Fatalf("api id = %d, want 777 from the config", resolved.Credentials.APIID)
	}
	if resolved.Credentials.APIHash != "profile-hash" {
		t.Fatal("the profile hash must be used")
	}
	if resolved.Credentials.Phone != "+15552220000" {
		t.Fatal("the profile phone must be used")
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("credential store calls = %d, want 1", got)
	}
}

func TestTelegramCredentialResolverRejectsIncompleteProfile(t *testing.T) {
	cases := []struct {
		name     string
		secrets  TelegramProfileCredentials
		wantText string
	}{
		{
			name:     "empty hash",
			secrets:  TelegramProfileCredentials{Phone: "+15552220000"},
			wantText: "no API hash",
		},
		{
			name:     "whitespace hash",
			secrets:  TelegramProfileCredentials{APIHash: "  ", Phone: "+1"},
			wantText: "no API hash",
		},
		{
			name:     "empty phone",
			secrets:  TelegramProfileCredentials{APIHash: "h"},
			wantText: "no phone",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearTelegramEnvironment(t)

			store := newFakeTelegramCredentialStore()
			store.profiles["default"] = c.secrets

			resolver := NewTelegramCredentialResolver(store)
			resolved, err := resolver.ResolveTelegramCredentials(
				context.Background(),
				completeAuthConfig(),
			)
			if !errors.Is(err, ErrTelegramCredentialsInvalid) {
				t.Fatalf("error = %v, want ErrTelegramCredentialsInvalid", err)
			}
			if resolved.Availability != AuthAvailabilityInvalid {
				t.Fatalf("availability = %v, want invalid", resolved.Availability)
			}
			if !strings.Contains(err.Error(), c.wantText) {
				t.Fatalf("error = %v, want %q", err, c.wantText)
			}
		})
	}
}

func TestTelegramCredentialResolverRejectsMissingProfile(t *testing.T) {
	clearTelegramEnvironment(t)

	store := newFakeTelegramCredentialStore()
	resolver := NewTelegramCredentialResolver(store)

	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	)
	if !errors.Is(err, ErrTelegramCredentialsInvalid) {
		t.Fatalf("error = %v, want ErrTelegramCredentialsInvalid", err)
	}
	if resolved.Availability != AuthAvailabilityInvalid {
		t.Fatalf("availability = %v, want invalid", resolved.Availability)
	}
}

// TestTelegramCredentialResolverRejectsConfiguredProfileWithoutStore pins
// the boundary of this PR: a configured profile without a real backend
// is a hard error, never mock-only.
func TestTelegramCredentialResolverRejectsConfiguredProfileWithoutStore(
	t *testing.T,
) {
	clearTelegramEnvironment(t)

	resolver := NewTelegramCredentialResolver(nil)
	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		completeAuthConfig(),
	)
	if !errors.Is(err, ErrTelegramCredentialProfileUnavailable) {
		t.Fatalf("error = %v, want ErrTelegramCredentialProfileUnavailable", err)
	}
	if resolved.Availability != AuthAvailabilityInvalid {
		t.Fatalf("availability = %v, want invalid", resolved.Availability)
	}
	if resolved.Ready() {
		t.Fatal("a missing backend must not report ready")
	}
}

// ---- partial auth config ----

func TestTelegramCredentialResolverRejectsPartialAuthConfig(t *testing.T) {
	cases := []struct {
		name string
		auth config.AuthConfig
	}{
		{"api id only", config.AuthConfig{APIID: 5}},
		{"profile only", config.AuthConfig{CredentialProfile: "default"}},
		{"negative api id", config.AuthConfig{APIID: -1, CredentialProfile: "d"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearTelegramEnvironment(t)

			store := newFakeTelegramCredentialStore()
			store.profiles["default"] = TelegramProfileCredentials{
				APIHash: "profile-hash",
				Phone:   "+15552220000",
			}

			resolver := NewTelegramCredentialResolver(store)
			resolved, err := resolver.ResolveTelegramCredentials(
				context.Background(),
				c.auth,
			)
			if !errors.Is(err, ErrTelegramCredentialsInvalid) {
				t.Fatalf("error = %v, want ErrTelegramCredentialsInvalid", err)
			}
			if resolved.Availability != AuthAvailabilityInvalid {
				t.Fatalf("availability = %v, want invalid", resolved.Availability)
			}
			if got := store.callCount(); got != 0 {
				t.Fatalf("credential store was consulted %d times", got)
			}
		})
	}
}

// ---- absent ----

func TestTelegramCredentialResolverReportsAbsentWithoutAnyConfiguration(
	t *testing.T,
) {
	clearTelegramEnvironment(t)

	store := newFakeTelegramCredentialStore()
	resolver := NewTelegramCredentialResolver(store)

	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		config.AuthConfig{},
	)
	if err != nil {
		t.Fatalf("an absent configuration must not be an error: %v", err)
	}
	if resolved.Availability != AuthAvailabilityAbsent {
		t.Fatalf("availability = %v, want absent", resolved.Availability)
	}
	if resolved.Source != AuthCredentialSourceNone {
		t.Fatalf("source = %v, want none", resolved.Source)
	}
	if got := store.callCount(); got != 0 {
		t.Fatalf("credential store was consulted %d times", got)
	}
}

// ---- no source mixing ----

func TestTelegramCredentialResolverDoesNotMixSources(t *testing.T) {
	cases := []struct {
		name       string
		id         string
		hash       string
		phone      string
		auth       config.AuthConfig
		wantSource AuthCredentialSource
	}{
		{
			name: "id from env with profile secrets",
			id:   "12345",
			auth: completeAuthConfig(),
		},
		{
			name:       "hash from env with config api id and profile phone",
			hash:       "env-hash",
			phone:      "",
			auth:       completeAuthConfig(),
			wantSource: AuthCredentialSourceNone,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearTelegramEnvironment(t)
			t.Setenv(telegramAPIIDEnvironment, c.id)
			t.Setenv(telegramAPIHashEnvironment, c.hash)
			t.Setenv(telegramPhoneEnvironment, c.phone)

			store := newFakeTelegramCredentialStore()
			store.profiles["default"] = TelegramProfileCredentials{
				APIHash: "profile-hash",
				Phone:   "+15552220000",
			}

			resolver := NewTelegramCredentialResolver(store)
			resolved, _ := resolver.ResolveTelegramCredentials(
				context.Background(),
				c.auth,
			)

			if resolved.Ready() &&
				resolved.Source != AuthCredentialSourceEnvironment {
				t.Fatalf(
					"a mixed set must never be ready from %v",
					resolved.Source,
				)
			}
			if resolved.Credentials.APIHash == "env-hash" &&
				resolved.Credentials.Phone == "+15552220000" {
				t.Fatal("credentials were combined from two sources")
			}
		})
	}
}

// ---- privacy ----

func TestTelegramCredentialResolverErrorsDoNotLeakSecrets(t *testing.T) {
	const secretHash = "leaky-hash-value-0123456789"
	const secretPhone = "+15550007777"

	t.Run("partial environment", func(t *testing.T) {
		clearTelegramEnvironment(t)
		t.Setenv(telegramAPIHashEnvironment, secretHash)

		resolver := NewTelegramCredentialResolver(
			newFakeTelegramCredentialStore(),
		)
		_, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			completeAuthConfig(),
		)
		if err == nil {
			t.Fatal("expected an error")
		}
		assertNoSecrets(t, err, secretHash, secretPhone)
	})

	t.Run("unavailable store", func(t *testing.T) {
		clearTelegramEnvironment(t)

		resolver := NewTelegramCredentialResolver(nil)
		_, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			completeAuthConfig(),
		)
		if err == nil {
			t.Fatal("expected an error")
		}
		assertNoSecrets(t, err, secretHash, secretPhone)
	})

	t.Run("profile load failure", func(t *testing.T) {
		clearTelegramEnvironment(t)

		store := newFakeTelegramCredentialStore()
		store.err = errors.New("backend exploded")

		resolver := NewTelegramCredentialResolver(store)
		_, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			completeAuthConfig(),
		)
		if err == nil {
			t.Fatal("expected an error")
		}
		// The backend message must not be propagated verbatim.
		if strings.Contains(err.Error(), "backend exploded") {
			t.Fatal("the backend error text must not be propagated")
		}
		assertNoSecrets(t, err, secretHash, secretPhone)
	})
}

func assertNoSecrets(t *testing.T, err error, secrets ...string) {
	t.Helper()

	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("the error leaked a credential value")
		}
	}
}

// ---- availability strings ----

func TestAuthAvailabilityAndSourceStrings(t *testing.T) {
	cases := []struct {
		value AuthAvailability
		want  string
	}{
		{AuthAvailabilityAbsent, "absent"},
		{AuthAvailabilityReady, "ready"},
		{AuthAvailabilityInvalid, "invalid"},
		{AuthAvailability(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.value.String(); got != c.want {
			t.Fatalf("String() = %q, want %q", got, c.want)
		}
	}

	sources := []struct {
		value AuthCredentialSource
		want  string
	}{
		{AuthCredentialSourceNone, "none"},
		{AuthCredentialSourceEnvironment, "environment"},
		{AuthCredentialSourceProfile, "profile"},
		{AuthCredentialSource(99), "unknown"},
	}
	for _, c := range sources {
		if got := c.value.String(); got != c.want {
			t.Fatalf("String() = %q, want %q", got, c.want)
		}
	}
}

// ---- application wiring ----

// TestTUIStartsMockModeOnlyWhenAuthIsAbsent pins the single state that
// may use the mock-only UI.
func TestTUIStartsMockModeOnlyWhenAuthIsAbsent(t *testing.T) {
	clearTelegramEnvironment(t)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	if code := Main([]string{"telecli", "tui"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if tuiCalls != 1 {
		t.Fatalf("RunTUI was called %d times, want 1", tuiCalls)
	}
	if !strings.Contains(stderr.String(), "mock-only TUI") {
		t.Fatalf("stderr = %q, want the mock-only notice", stderr.String())
	}
}

func TestTUIDoesNotStartMockModeForPartialEnvironment(t *testing.T) {
	clearTelegramEnvironment(t)
	t.Setenv(telegramAPIHashEnvironment, "orphan-hash")

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	code := Main([]string{"telecli", "tui"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for a partial environment", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times, want 0", tuiCalls)
	}
	if strings.Contains(stderr.String(), "mock-only") {
		t.Fatalf("stderr = %q, mock-only must not start", stderr.String())
	}
	if !strings.Contains(stderr.String(), "credentials error") {
		t.Fatalf("stderr = %q, want a credentials error", stderr.String())
	}
}

func TestTUIDoesNotStartMockModeForBrokenProfile(t *testing.T) {
	clearTelegramEnvironment(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	body := "log_level = \"info\"\n" +
		"[auth]\napi_id = 42\ncredential_profile = \"missing-backend\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELECLI_CONFIG", path)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	code := Main([]string{"telecli", "tui"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for a broken profile", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times, want 0", tuiCalls)
	}
	if strings.Contains(stderr.String(), "mock-only") {
		t.Fatalf("stderr = %q, mock-only must not start", stderr.String())
	}
}

// TestTUIStartsTelegramRuntimeForCompleteEnvironment proves the resolver
// hands a usable credential set to the runtime factory.
func TestTUIStartsTelegramRuntimeForCompleteEnvironment(t *testing.T) {
	clearTelegramEnvironment(t)
	setCompleteEnvironment(t)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	var gotConfig config.Config
	var gotParams telegram.TdlibParameters
	env.NewTelegramRuntime = func(
		cfg config.Config,
		_ recorder.ComponentRecorder,
	) (*telegram.Runtime, error) {
		gotConfig = cfg

		resolver := NewTelegramCredentialResolver(nil)
		resolved, err := resolver.ResolveTelegramCredentials(
			context.Background(),
			cfg.Auth,
		)
		if err != nil {
			return nil, err
		}
		gotParams, err = TdlibParametersFromEnv(cfg, resolved.Credentials)
		if err != nil {
			return nil, err
		}

		return nil, errors.New("stop after credential resolution")
	}

	code := Main([]string{"telecli", "tui"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1 from the injected factory error", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times, want 0", tuiCalls)
	}
	if gotParams.APIID != 12345 || gotParams.APIHash != "env-hash" {
		t.Fatalf("the runtime did not receive the resolved credentials")
	}
	if gotConfig.LogLevel == "" {
		t.Fatal("the runtime did not receive the loaded configuration")
	}
}

// ---- doctor UX ----

func TestDoctorReportsAuthAbsent(t *testing.T) {
	clearTelegramEnvironment(t)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
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
	if !strings.Contains(out, "Auth configuration: absent") {
		t.Fatalf("doctor output = %q", out)
	}
	if !strings.Contains(out, "Mode: mock-only") {
		t.Fatalf("doctor output = %q, want the mock-only line", out)
	}
}

func TestDoctorReportsAuthReadyWithoutExposingSourceValues(t *testing.T) {
	const secretHash = "doctor-visible-hash-should-not-appear"
	const secretPhone = "+15550006666"

	clearTelegramEnvironment(t)
	t.Setenv(telegramAPIIDEnvironment, "12345")
	t.Setenv(telegramAPIHashEnvironment, secretHash)
	t.Setenv(telegramPhoneEnvironment, secretPhone)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
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
	if !strings.Contains(out, "Auth source: environment") {
		t.Fatalf("doctor output = %q, want the source line", out)
	}
	for _, secret := range []string{secretHash, secretPhone, "12345"} {
		if strings.Contains(out, secret) {
			t.Fatal("doctor output leaked a credential value")
		}
	}
}

func TestDoctorReportsAuthInvalid(t *testing.T) {
	clearTelegramEnvironment(t)
	t.Setenv(telegramAPIHashEnvironment, "orphan-hash")

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		context.Context,
		config.Config,
		io.Writer,
	) int {
		return 0
	}

	// doctor reports the invalid state and still exits 0 so the user
	// can see every diagnostic at once.
	if code := Main([]string{"telecli", "doctor"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "Auth configuration: invalid") {
		t.Fatalf("doctor output = %q", out)
	}
	if !strings.Contains(out, "Reason:") {
		t.Fatalf("doctor output = %q, want a reason line", out)
	}
	if strings.Contains(out, "orphan-hash") {
		t.Fatal("doctor output leaked the credential value")
	}
}

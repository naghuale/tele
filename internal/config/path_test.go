package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfigHome points os.UserConfigDir at a temporary directory so
// the default-path rules can be tested without touching the real user
// configuration, and returns the temporary home.
//
// HOME is the portable override. XDG_CONFIG_HOME is set as well so the
// same test works on Linux, where os.UserConfigDir prefers it.
func isolateConfigHome(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv(configPathEnvironment, "")

	// Guard against a platform where neither override applies: the
	// tests must never read or write the real user configuration.
	if !strings.HasPrefix(mustDefaultPath(t), dir) {
		t.Fatalf(
			"config isolation failed: default path %q is not under %q",
			mustDefaultPath(t),
			dir,
		)
	}

	return dir
}

// mustDefaultPath returns the platform default config path.
func mustDefaultPath(t *testing.T) string {
	t.Helper()

	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---- ResolvePath ----

func TestResolveConfigPathPrefersExplicit(t *testing.T) {
	isolateConfigHome(t)
	dir := t.TempDir()
	t.Setenv(configPathEnvironment, filepath.Join(dir, "from-env.toml"))

	explicit := filepath.Join(dir, "explicit.toml")
	writeConfig(t, explicit, "log_level = \"info\"\n")
	writeConfig(t, filepath.Join(dir, "from-env.toml"), "log_level = \"info\"\n")
	writeConfig(t, mustDefaultPath(t), "log_level = \"info\"\n")

	resolved, err := ResolvePath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceExplicit {
		t.Fatalf("source = %q, want explicit", resolved.Source)
	}
	if resolved.Path != explicit {
		t.Fatalf("path = %q, want %q", resolved.Path, explicit)
	}
}

func TestResolveConfigPathUsesEnvironment(t *testing.T) {
	isolateConfigHome(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, "from-env.toml")
	writeConfig(t, envPath, "log_level = \"info\"\n")
	writeConfig(t, mustDefaultPath(t), "log_level = \"info\"\n")

	t.Setenv(configPathEnvironment, envPath)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceEnvironment {
		t.Fatalf("source = %q, want environment", resolved.Source)
	}
	if resolved.Path != envPath {
		t.Fatalf("path = %q, want %q", resolved.Path, envPath)
	}
}

func TestResolveConfigPathUsesDefault(t *testing.T) {
	isolateConfigHome(t)
	want := mustDefaultPath(t)
	writeConfig(t, want, "log_level = \"info\"\n")

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceDefault {
		t.Fatalf("source = %q, want default", resolved.Source)
	}
	if resolved.Path != want {
		t.Fatalf("path = %q, want %q", resolved.Path, want)
	}
}

func TestResolveConfigPathReturnsNoneWhenDefaultMissing(t *testing.T) {
	isolateConfigHome(t)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatalf("a missing default config must not be an error: %v", err)
	}
	if resolved.Source != PathSourceNone {
		t.Fatalf("source = %q, want none", resolved.Source)
	}
	if resolved.Found() {
		t.Fatal("Found() must be false for PathSourceNone")
	}
	if resolved.Path != "" {
		t.Fatalf("path = %q, want empty", resolved.Path)
	}
}

func TestResolveConfigPathRejectsMissingExplicitFile(t *testing.T) {
	isolateConfigHome(t)
	missing := filepath.Join(t.TempDir(), "absent.toml")

	resolved, err := ResolvePath(missing)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("error = %v, want ErrConfigNotFound", err)
	}
	if resolved.Found() {
		t.Fatal("a failed resolution must not report a path")
	}
}

func TestResolveConfigPathRejectsMissingEnvironmentFile(t *testing.T) {
	isolateConfigHome(t)
	missing := filepath.Join(t.TempDir(), "absent.toml")
	t.Setenv(configPathEnvironment, missing)

	if _, err := ResolvePath(""); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("error = %v, want ErrConfigNotFound", err)
	}
}

func TestResolveConfigPathRejectsDirectoryAsExplicitPath(t *testing.T) {
	isolateConfigHome(t)

	dir := t.TempDir()
	if _, err := ResolvePath(dir); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("error = %v, want ErrConfigNotFound", err)
	}
}

func TestResolveConfigPathPrecedenceExplicitOverEnvironmentOverDefault(
	t *testing.T,
) {
	isolateConfigHome(t)

	explicit := filepath.Join(t.TempDir(), "explicit.toml")
	envPath := filepath.Join(t.TempDir(), "env.toml")
	defaultPath := mustDefaultPath(t)

	writeConfig(t, explicit, `log_level = "debug"`)
	writeConfig(t, envPath, `log_level = "debug"`)
	writeConfig(t, defaultPath, `log_level = "debug"`)

	t.Setenv(configPathEnvironment, envPath)

	resolved, err := ResolvePath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceExplicit {
		t.Fatalf("source = %q, want explicit", resolved.Source)
	}

	resolved, err = ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceEnvironment {
		t.Fatalf("source = %q, want environment", resolved.Source)
	}

	os.Unsetenv(configPathEnvironment)

	resolved, err = ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != PathSourceDefault {
		t.Fatalf("source = %q, want default", resolved.Source)
	}
}

// ---- LoadResolved ----

func TestLoadDefaultConfigWhenNoFileExists(t *testing.T) {
	isolateConfigHome(t)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadResolved(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Fatal("a missing config must yield the built-in defaults")
	}
}

func TestLoadRejectsInvalidDefaultConfig(t *testing.T) {
	isolateConfigHome(t)
	writeConfig(t, mustDefaultPath(t), "log_level = 5\n")

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResolved(resolved); err == nil {
		t.Fatal("a corrupt default config must be an error")
	}
}

func TestLoadRejectsUnknownDefaultConfigKey(t *testing.T) {
	isolateConfigHome(t)
	writeConfig(
		t,
		mustDefaultPath(t),
		"log_level = \"info\"\nnot_a_real_key = 1\n",
	)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResolved(resolved); err == nil {
		t.Fatal("an unknown key must remain a hard error")
	}
}

func TestLoadResolvesEnvironmentConfig(t *testing.T) {
	isolateConfigHome(t)
	path := filepath.Join(t.TempDir(), "env.toml")
	writeConfig(t, path, "log_level = \"debug\"\n")
	t.Setenv(configPathEnvironment, path)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadResolved(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level = %q, want debug", cfg.LogLevel)
	}
}

// ---- TDLib library_path ----

func TestTDLibConfigUsesLibraryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeConfig(
		t,
		path,
		"log_level = \"info\"\n"+
			"[tdlib]\n"+
			"library_path = \"/opt/homebrew/lib/libtdjson.dylib\"\n",
	)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TDLib.LibraryPath != "/opt/homebrew/lib/libtdjson.dylib" {
		t.Fatalf("library_path = %q", cfg.TDLib.LibraryPath)
	}
}

func TestTDLibConfigRejectsLibraryAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeConfig(
		t,
		path,
		"log_level = \"info\"\n"+
			"[tdlib]\n"+
			"library = \"/opt/homebrew/lib/libtdjson.dylib\"\n",
	)

	_, err := Load(path)
	if err == nil {
		t.Fatal("tdlib.library must be rejected: the key is library_path")
	}
	if !contains(err.Error(), "unknown config keys") {
		t.Fatalf("error = %v, want an unknown config keys error", err)
	}
}

// ---- auth section ----

func TestAuthConfigDecodesTOMLTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeConfig(
		t,
		path,
		"log_level = \"info\"\n"+
			"[auth]\n"+
			"api_id = 123456\n"+
			"credential_profile = \"default\"\n",
	)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.APIID != 123456 {
		t.Fatalf("api_id = %d, want 123456", cfg.Auth.APIID)
	}
	if cfg.Auth.CredentialProfile != "default" {
		t.Fatalf(
			"credential_profile = %q, want default",
			cfg.Auth.CredentialProfile,
		)
	}
	if !cfg.Auth.Configured() {
		t.Fatal("Configured() must be true for a populated auth section")
	}
}

func TestAuthConfigAbsentInDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Auth.Configured() {
		t.Fatal("the built-in defaults must not select an auth profile")
	}
}

func TestAuthConfigRejectsUnknownAuthKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeConfig(
		t,
		path,
		"log_level = \"info\"\n"+
			"[auth]\n"+
			"api_hash = \"must-not-be-here\"\n",
	)

	if _, err := Load(path); err == nil {
		t.Fatal("auth.api_hash must be rejected: secrets never live in TOML")
	}
}

// ---- security ----

// TestResolvePathErrorDoesNotLeakConfigContents pins that a resolution
// error may name the path but never the file body.
func TestResolvePathErrorDoesNotLeakConfigContents(t *testing.T) {
	isolateConfigHome(t)

	const secretish = "s3cr3t-token-value"
	path := filepath.Join(t.TempDir(), "absent.toml")

	_, err := ResolvePath(path)
	if err == nil {
		t.Fatal("expected an error for a missing explicit path")
	}
	if contains(err.Error(), secretish) {
		t.Fatal("the error must not contain configuration contents")
	}
	if !contains(err.Error(), path) {
		t.Fatal("the error should name the configuration path")
	}
}

func TestLoadErrorDoesNotLeakConfigContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeConfig(
		t,
		path,
		"log_level = \"info\"\n"+
			"[auth]\n"+
			"api_hash = \""+secretFixture+"\"\n",
	)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown auth key")
	}
	if contains(err.Error(), secretFixture) {
		t.Fatal("the parse error must not echo the rejected value")
	}
}

// secretFixture is an obvious non-credential used to prove that error
// strings never echo file contents.
const secretFixture = "not-a-real-secret-value"

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

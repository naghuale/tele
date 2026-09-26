package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileUsesOwnerOnlyModes pins the literal modes rather than the
// constants, so a wrong constant cannot make the test pass.
func TestWriteFileUsesOwnerOnlyModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	if err := WriteFile(path, Default()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	const want = os.FileMode(0o600)
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("config mode = %o, want %o", got, want)
	}
}

func TestWriteFileCreatesDirectoryMode0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent", "nested")
	path := filepath.Join(dir, "config.toml")

	if err := WriteFile(path, Default()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	const want = os.FileMode(0o700)
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("directory mode = %o, want %o", got, want)
	}
}

// TestWriteFileLeavesNoTemporaryFiles proves the atomic rename cleans up.
func TestWriteFileLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := WriteFile(path, Default()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory contains %v, want only config.toml", names)
	}
}

// TestWriteFilePreservesPreviousContentOnFailure proves a failed write
// does not truncate the existing configuration.
func TestWriteFilePreservesPreviousContentOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := "log_level = \"debug\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// A directory in place of the target makes the rename fail.
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(blocked, Default()); err == nil {
		t.Fatal("writing onto a directory must fail")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatal("the previous configuration was modified")
	}
}

// TestRejectSecretsDetectsSecretFields exercises the guard directly,
// because no exported Config field can carry a secret.
func TestRejectSecretsDetectsSecretFields(t *testing.T) {
	cases := []string{
		`api_hash = "abc"`,
		`apihash = "abc"`,
		`phone = "+1"`,
		`password = "abc"`,
		`database_encryption_key = "abc"`,
	}

	for _, rendered := range cases {
		if err := rejectSecrets([]byte(rendered)); !errors.Is(
			err,
			ErrSecretInConfig,
		) {
			t.Fatalf(
				"rendered = %q: error = %v, want ErrSecretInConfig",
				rendered,
				err,
			)
		}
	}

	clean := `log_level = "info"
[auth]
api_id = 123
credential_profile = "default"`

	if err := rejectSecrets([]byte(clean)); err != nil {
		t.Fatalf("a clean configuration was rejected: %v", err)
	}
}

func TestEncodeRejectsSecretShapedContent(t *testing.T) {
	cfg := Default()
	cfg.Auth.CredentialProfile = "default"

	encoded, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{"api_hash", "phone", "password"} {
		if strings.Contains(
			strings.ToLower(string(encoded)),
			forbidden,
		) {
			t.Fatalf("the encoded configuration contains %q", forbidden)
		}
	}
}

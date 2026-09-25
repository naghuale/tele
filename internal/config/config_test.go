package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestExplicitTOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("log_level = \"debug\"\ndata_dir = \"/tmp/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" || cfg.DataDir != "/tmp/x" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("log_levle = \"debug\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestEmptyLogLevelRejected(t *testing.T) {
	cfg := Config{LogLevel: "", DataDir: "/tmp"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyDataDirRejected(t *testing.T) {
	cfg := Config{LogLevel: "info", DataDir: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

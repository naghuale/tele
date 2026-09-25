//go:build tdlib_integration && cgo && (darwin || linux)

package tdjson

import (
	"encoding/json"
	"os"
	"testing"
)

type optionValueString struct {
	Type  string `json:"@type"`
	Value string `json:"value"`
}

func TestNativeRuntimeLoadsAndExecutes(t *testing.T) {
	libraryPath := os.Getenv("TELECLI_TDLIB_LIBRARY")
	if libraryPath == "" {
		t.Fatal("TELECLI_TDLIB_LIBRARY is required")
	}

	native, err := Open(libraryPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	t.Cleanup(func() {
		if err := native.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	versionResponse, err := native.Execute(
		[]byte(`{"@type":"getOption","name":"version"}`),
	)
	if err != nil {
		t.Fatalf("Execute(version) error = %v", err)
	}

	var version optionValueString
	if err := json.Unmarshal(versionResponse, &version); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if version.Type != "optionValueString" {
		t.Fatalf("version response type = %q, want optionValueString", version.Type)
	}
	if version.Value == "" {
		t.Fatal("TDLib version is empty")
	}

	commitResponse, err := native.Execute(
		[]byte(`{"@type":"getOption","name":"commit_hash"}`),
	)
	if err != nil {
		t.Fatalf("Execute(commit_hash) error = %v", err)
	}

	var commit optionValueString
	if err := json.Unmarshal(commitResponse, &commit); err != nil {
		t.Fatalf("decode commit response: %v", err)
	}
	if commit.Type != "optionValueString" {
		t.Fatalf("commit response type = %q, want optionValueString", commit.Type)
	}
	if commit.Value == "" {
		t.Fatal("TDLib commit hash is empty")
	}
}

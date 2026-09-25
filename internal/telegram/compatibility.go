package telegram

import (
	"context"
	"encoding/json"
	"fmt"
)

type CompatibilityMode string

const (
	CompatibilityVerified   CompatibilityMode = "verified"
	CompatibilityUnverified CompatibilityMode = "unverified"
	CompatibilityRejected   CompatibilityMode = "rejected"
)

type RuntimeInfo struct {
	Available bool
	Version   string
	Commit    string
	Mode      CompatibilityMode
	Reason    string
}

type CompatibilityManifest struct {
	Version string
	Commit  string
}

type optionValueString struct {
	Type  string `json:"@type"`
	Value string `json:"value"`
}

func InspectRuntime(ctx context.Context, native Native, expected CompatibilityManifest) (RuntimeInfo, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeInfo{}, err
	}
	version, err := executeStringOption(native, "version")
	if err != nil {
		return RuntimeInfo{Mode: CompatibilityRejected, Reason: err.Error()}, err
	}
	commit, err := executeStringOption(native, "commit_hash")
	if err != nil {
		return RuntimeInfo{Available: true, Version: version, Mode: CompatibilityUnverified, Reason: err.Error()}, nil
	}
	info := RuntimeInfo{Available: true, Version: version, Commit: commit}
	switch {
	case expected.Commit != "" && commit == expected.Commit && (expected.Version == "" || version == expected.Version):
		info.Mode = CompatibilityVerified
	case expected.Commit == "":
		info.Mode = CompatibilityUnverified
		info.Reason = "pinned TDLib commit is not configured"
	default:
		info.Mode = CompatibilityRejected
		info.Reason = fmt.Sprintf("TDLib commit mismatch: got %s, want %s", commit, expected.Commit)
	}
	return info, nil
}

func executeStringOption(native Native, name string) (string, error) {
	request, err := json.Marshal(map[string]string{"@type": "getOption", "name": name})
	if err != nil {
		return "", err
	}
	response, err := native.Execute(request)
	if err != nil {
		return "", fmt.Errorf("get TDLib option %q: %w", name, err)
	}
	var value optionValueString
	if err := json.Unmarshal(response, &value); err != nil {
		return "", fmt.Errorf("decode TDLib option %q: %w", name, err)
	}
	if value.Type != "optionValueString" || value.Value == "" {
		return "", fmt.Errorf("TDLib option %q returned %q", name, value.Type)
	}
	return value.Value, nil
}

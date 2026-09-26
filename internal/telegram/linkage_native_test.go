package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	linkageHelperEnvironment = "TELECLI_TEST_LINKAGE_HELPER"
	linkageRepeatCount       = 10
	linkageHelperTimeout     = 60 * time.Second
)

type linkageHelperResult struct {
	Source          string `json:"source"`
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	Compatibility   string `json:"compatibility"`
	ClientCreated   string `json:"client_created"`
	SendReceive     string `json:"send_receive"`
	ExtraRoundTrip  string `json:"extra_round_trip"`
	ClientIDMatched string `json:"client_id_matched"`
	Failed          bool   `json:"failed"`
	Error           string `json:"error"`
}

func TestLinkageNativeProbe(t *testing.T) {
	if os.Getenv(linkageHelperEnvironment) == "1" {
		runLinkageHelper()
		return
	}

	if _, err := LoadNativeWithSource(""); err != nil {
		t.Skip("no TDLib runtime available for the native linkage probe")
	}

	for run := 1; run <= linkageRepeatCount; run++ {
		result, stdout, _ := runLinkageSubprocess(t)

		assertNoCredentialNames(t, stdout)
		assertNoAuthorizationTraffic(t, stdout)

		if result.Failed {
			t.Fatalf("run %d: probe reported failure: %s", run, result.Error)
		}
		if result.ClientCreated != LinkageStatusPass.String() ||
			result.SendReceive != LinkageStatusPass.String() ||
			result.ExtraRoundTrip != LinkageStatusPass.String() ||
			result.ClientIDMatched != LinkageStatusPass.String() {
			t.Fatalf(
				"run %d: incomplete probe: created=%s send=%s extra=%s clientID=%s",
				run,
				result.ClientCreated,
				result.SendReceive,
				result.ExtraRoundTrip,
				result.ClientIDMatched,
			)
		}

		expected := RuntimeCompatibility()
		if result.Version != expected.Version {
			t.Fatalf("run %d: version = %q, want %q", run, result.Version, expected.Version)
		}
		if result.Commit != expected.Commit {
			t.Fatalf("run %d: commit = %q, want %q", run, result.Commit, expected.Commit)
		}
		if result.Compatibility != string(CompatibilityVerified) {
			t.Fatalf(
				"run %d: compatibility = %q, want %q",
				run,
				result.Compatibility,
				CompatibilityVerified,
			)
		}
	}
}

func runLinkageSubprocess(t *testing.T) (linkageHelperResult, string, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), linkageHelperTimeout)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestLinkageNativeProbe$",
		"-test.v=false",
	)
	command.Env = cleanLinkageEnvironment(os.Environ())

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runError := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("linkage helper timed out: %s", safeLinkageDiagnostic(stderr.String()))
	}
	if runError != nil {
		t.Fatalf(
			"linkage helper failed: %v; %s",
			runError,
			safeLinkageDiagnostic(stderr.String()),
		)
	}

	var result linkageHelperResult
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode helper result: %v; %s", err, safeLinkageDiagnostic(stderr.String()))
	}

	return result, stdout.String(), stderr.String()
}

func cleanLinkageEnvironment(current []string) []string {
	blocked := []string{
		"TELECLI_TDLIB_API_ID=",
		"TELECLI_TDLIB_API_HASH=",
		"TELECLI_TDLIB_PHONE=",
		"TELECLI_CONFIG=",
		"TELECLI_AUTH_TRACE=",
		linkageHelperEnvironment + "=",
	}

	clean := make([]string, 0, len(current)+1)
	for _, entry := range current {
		drop := false
		for _, prefix := range blocked {
			if strings.HasPrefix(entry, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			clean = append(clean, entry)
		}
	}

	return append(clean, linkageHelperEnvironment+"=1")
}

func runLinkageHelper() {
	result := linkageHelperResult{Error: "unset"}

	probeResult, err := RunLinkageProbe(context.Background(), "")
	if err != nil {
		if errors.Is(err, ErrLinkageProbe) || errors.Is(err, ErrNativeUnavailable) {
			result.Error = "probe-unavailable"
		} else {
			result.Error = "probe-failed"
		}
	} else {
		result = linkageHelperResult{
			Source:          string(probeResult.Source),
			Version:         probeResult.Version,
			Commit:          probeResult.Commit,
			Compatibility:   string(probeResult.Compatibility),
			ClientCreated:   probeResult.ClientCreated.String(),
			SendReceive:     probeResult.SendReceive.String(),
			ExtraRoundTrip:  probeResult.ExtraRoundTrip.String(),
			ClientIDMatched: probeResult.ClientIDMatched.String(),
			Failed:          probeResult.Failed(),
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func assertNoCredentialNames(t *testing.T, outputs ...string) {
	t.Helper()

	forbidden := []string{
		"api_hash", "phone_number", "authentication_code", "password",
		"TELECLI_TDLIB_API_ID", "TELECLI_TDLIB_API_HASH", "TELECLI_TDLIB_PHONE",
	}
	for _, output := range outputs {
		lower := strings.ToLower(output)
		for _, name := range forbidden {
			if strings.Contains(lower, strings.ToLower(name)) {
				t.Fatalf("forbidden credential name in probe output: %s", name)
			}
		}
	}
}

// assertNoAuthorizationTraffic proves the probe stayed credential-free.
//
// The check applies to stdout only. stdout is this package's own closed output
// contract, so any authorization marker there would mean the probe sent one.
// TDLib writes its own log to stderr, and an activated client legitimately
// makes TDLib report its pushed authorization state there. That is TDLib
// volunteering state, not a request from this probe, so stderr is not evidence
// of authorization traffic. What the probe sends is pinned separately by
// TestLinkageRequestIsCredentialFreeAndExact and by the source gate.
func assertNoAuthorizationTraffic(t *testing.T, outputs ...string) {
	t.Helper()

	forbidden := []string{
		"authorizationState",
		"waitPhoneNumber",
		"waitCode",
		"\"phone_number\"",
		"setTdlibParameters",
		"setAuthenticationPhoneNumber",
		"checkAuthenticationCode",
	}
	for _, output := range outputs {
		for _, marker := range forbidden {
			if strings.Contains(output, marker) {
				t.Fatalf("authorization traffic observed in probe stdout: %s", marker)
			}
		}
	}
}

func safeLinkageDiagnostic(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return "no stderr"
	}
	return "stderr withheld"
}

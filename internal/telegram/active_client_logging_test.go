package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	activeLoggingHelperEnvironment = "TELECLI_TEST_ACTIVE_LOGGING_HELPER"
	activeLoggingHelperTimeout     = 60 * time.Second
)

// activeLoggingProbeResult is the closed stdout contract of the helper. It
// carries no native payload, no library path and no backend message.
type activeLoggingProbeResult struct {
	Linkage            string `json:"linkage"`
	Source             string `json:"source"`
	Version            string `json:"version"`
	Commit             string `json:"commit"`
	Compatibility      string `json:"compatibility"`
	ClientCreated      string `json:"client_created"`
	SendReceive        string `json:"send_receive"`
	ExtraRoundTrip     string `json:"extra_round_trip"`
	ClientIDMatched    string `json:"client_id_matched"`
	Failed             bool   `json:"failed"`
	ObservationPresent bool   `json:"observation_present"`
}

// nativeTDLibLevelPrefix matches the level prefix TDLib writes on its own log
// lines. TDLib renders a line as an ANSI colour escape followed by
// "[ 3][t 0][...][Client.cpp:281]", so the escape has to be allowed before the
// level bracket or an anchored match never succeeds.
var nativeTDLibLevelPrefix = regexp.MustCompile(
	`(?m)^(?:\x1b\[[0-9;]*m)*\[\s*([0-9]+)\]`,
)

// clientWaitMarker recognises TDLib's periodic receive-wait notice without
// keeping or printing the line itself.
var clientWaitMarker = regexp.MustCompile(`Begin to wait for updates`)

type nativeLevelSummary struct {
	MaximumLevel int
	CountByLevel map[int]int
	ClientWaits  int
	TotalLines   int
}

// TestActiveClientLoggingBaseline establishes whether the credential-free
// active-client workload reproduces native informational logging.
//
// It reuses the PR-TD1 production probe unchanged, so the loader, the runtime
// lifecycle, the request builder and the correlation logic are not duplicated.
// The probe activates the logical client through Runtime.Send, which is the
// step that earlier made TDLib emit informational output.
//
// A missing observation is INCONCLUSIVE, never a production policy failure.
func TestActiveClientLoggingBaseline(t *testing.T) {
	if os.Getenv(activeLoggingHelperEnvironment) == "1" {
		runActiveLoggingHelper()
		return
	}

	if _, err := LoadNativeWithSource(""); err != nil {
		t.Skip("no TDLib runtime available for the active-client logging probe")
	}

	result, stdout, stderr := runActiveLoggingSubprocess(t)

	// stdout is this package's own closed contract.
	assertNoCredentialNamesIn(t, stdout)
	assertNoAuthorizationTrafficIn(t, stdout)

	// stderr is written by TDLib itself once the client is active, so it is
	// checked for credential-bearing names and for requests, not for the
	// authorization state TDLib pushes on its own.
	assertNoCredentialNamesIn(t, stderr)
	assertNoAuthorizationRequestsIn(t, stderr)

	if result.Linkage != LinkageStatusPass.String() {
		t.Fatalf(
			"LG1-R2 baseline: INCONCLUSIVE stage=linkage linkage=%s",
			result.Linkage,
		)
	}
	if result.ClientCreated != LinkageStatusPass.String() ||
		result.SendReceive != LinkageStatusPass.String() ||
		result.ExtraRoundTrip != LinkageStatusPass.String() ||
		result.ClientIDMatched != LinkageStatusPass.String() {
		t.Fatalf(
			"LG1-R2 baseline: INCONCLUSIVE stage=linkage created=%s send=%s extra=%s clientID=%s",
			result.ClientCreated,
			result.SendReceive,
			result.ExtraRoundTrip,
			result.ClientIDMatched,
		)
	}

	expected := RuntimeCompatibility()
	if result.Version != expected.Version {
		t.Fatalf("baseline version = %q, want %q", result.Version, expected.Version)
	}
	if result.Commit != expected.Commit {
		t.Fatalf("baseline commit = %q, want %q", result.Commit, expected.Commit)
	}

	summary := summarizeNativeLevels(stderr)

	if summary.TotalLines == 0 {
		t.Log("LG1-R2 baseline: INCONCLUSIVE")
		t.Log("  linkage: PASS")
		t.Log("  native log lines: none")
		t.Log("  the workload did not activate observable native logging,")
		t.Log("  so configured cases could not be interpreted")
		return
	}

	if summary.MaximumLevel < 3 {
		t.Log("LG1-R2 baseline: INCONCLUSIVE")
		t.Log("  linkage: PASS")
		t.Logf("  maximum native level: %d", summary.MaximumLevel)
		t.Logf("  client wait lines: %d", summary.ClientWaits)
		t.Log("  informational level 3 was not reproduced")
		return
	}

	if summary.ClientWaits == 0 {
		t.Log("LG1-R2 baseline: INCONCLUSIVE")
		t.Log("  linkage: PASS")
		t.Logf("  maximum native level: %d", summary.MaximumLevel)
		t.Log("  the client-wait marker was not observed")
		return
	}

	t.Log("LG1-R2 baseline: PASS")
	t.Log("  linkage: PASS")
	t.Logf("  maximum native level: %d", summary.MaximumLevel)
	t.Logf("  client wait lines: %d", summary.ClientWaits)
	t.Logf("  recognized native log lines: %d", summary.TotalLines)
	t.Log("  authorization requests: none")
	t.Log("  credential fields: none")
}

func runActiveLoggingSubprocess(
	t *testing.T,
) (activeLoggingProbeResult, string, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		activeLoggingHelperTimeout,
	)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestActiveClientLoggingBaseline$",
		"-test.v=false",
	)
	command.Env = cleanActiveLoggingEnvironment(os.Environ())

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runError := command.Run()
	if ctx.Err() != nil {
		t.Fatal("LG1-R2 baseline: helper timed out")
	}
	if runError != nil {
		// The helper's own failure is reported through its closed result, so
		// the native stderr is not reproduced here.
		t.Fatalf("LG1-R2 baseline: helper failed: %v", runError)
	}

	var result activeLoggingProbeResult
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode helper result: %v", err)
	}

	return result, stdout.String(), stderr.String()
}

func cleanActiveLoggingEnvironment(current []string) []string {
	blocked := []string{
		"TELECLI_TDLIB_API_ID=",
		"TELECLI_TDLIB_API_HASH=",
		"TELECLI_TDLIB_PHONE=",
		"TELECLI_CONFIG=",
		"TELECLI_AUTH_TRACE=",
		activeLoggingHelperEnvironment + "=",
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

	return append(clean, activeLoggingHelperEnvironment+"=1")
}

func runActiveLoggingHelper() {
	result := activeLoggingProbeResult{Linkage: LinkageStatusFail.String()}

	probe, err := RunLinkageProbe(context.Background(), "")
	if err != nil {
		result.ObservationPresent = false
	} else {
		result = activeLoggingProbeResult{
			Linkage:         LinkageStatusPass.String(),
			Source:          string(probe.Source),
			Version:         probe.Version,
			Commit:          probe.Commit,
			Compatibility:   string(probe.Compatibility),
			ClientCreated:   probe.ClientCreated.String(),
			SendReceive:     probe.SendReceive.String(),
			ExtraRoundTrip:  probe.ExtraRoundTrip.String(),
			ClientIDMatched: probe.ClientIDMatched.String(),
			Failed:          probe.Failed(),
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func summarizeNativeLevels(stderr string) nativeLevelSummary {
	summary := nativeLevelSummary{
		MaximumLevel: -1,
		CountByLevel: make(map[int]int),
	}

	for _, match := range nativeTDLibLevelPrefix.FindAllStringSubmatch(stderr, -1) {
		if len(match) != 2 {
			continue
		}
		level, parseError := strconv.Atoi(match[1])
		if parseError != nil {
			continue
		}
		summary.CountByLevel[level]++
		summary.TotalLines++
		if level > summary.MaximumLevel {
			summary.MaximumLevel = level
		}
	}

	summary.ClientWaits = len(clientWaitMarker.FindAllString(stderr, -1))

	return summary
}

func assertNoCredentialNamesIn(t *testing.T, outputs ...string) {
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

func assertNoAuthorizationRequestsIn(t *testing.T, outputs ...string) {
	t.Helper()

	forbidden := []string{
		"setTdlibParameters",
		"setAuthenticationPhoneNumber",
		"checkAuthenticationCode",
		"checkAuthenticationPassword",
		"sendMessage",
	}
	for _, output := range outputs {
		for _, marker := range forbidden {
			if strings.Contains(output, marker) {
				t.Fatalf("authorization request observed in probe output: %s", marker)
			}
		}
	}
}

func assertNoAuthorizationTrafficIn(t *testing.T, outputs ...string) {
	t.Helper()

	forbidden := []string{
		"authorizationState",
		"waitPhoneNumber",
		"waitCode",
		"\"phone_number\"",
	}
	for _, output := range outputs {
		for _, marker := range forbidden {
			if strings.Contains(output, marker) {
				t.Fatalf("authorization traffic observed in probe stdout: %s", marker)
			}
		}
	}
}

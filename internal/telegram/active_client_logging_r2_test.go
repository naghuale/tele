package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	activeLoggingR2HelperEnvironment = "TELECLI_TEST_ACTIVE_LOGGING_R2_HELPER"
	activeLoggingR2HelperTimeout     = 60 * time.Second

	// activeLoggingConfiguredMarker separates native output produced before
	// the policy is installed from native output produced after it.
	activeLoggingConfiguredMarker = "telecli LG1-R2 marker: logging-configured"
)

// activeLoggingR2Result is the closed stdout contract of the R2 helper.
type activeLoggingR2Result struct {
	PreLinkagePassed    bool   `json:"pre_linkage_passed"`
	PreValue            string `json:"pre_value"`
	ConfigurePassed     bool   `json:"configure_passed"`
	PostLinkagePassed   bool   `json:"post_linkage_passed"`
	PostValue           string `json:"post_value"`
	Version             string `json:"version"`
	Commit              string `json:"commit"`
	Compatibility       string `json:"compatibility"`
	ClientCreatedPassed bool   `json:"client_created_passed"`
	MarkerEmitted       bool   `json:"marker_emitted"`
}

// TestActiveClientLoggingConfigureAfterActivation compares the same runtime,
// the same logical client and the same request type before and after the
// logging policy is installed, inside one subprocess.
//
// Before the policy it must observe informational native output. That
// observation is the parser self-check: without it, silence after the marker
// could not be told apart from a parser that recognizes nothing.
func TestActiveClientLoggingConfigureAfterActivation(t *testing.T) {
	if os.Getenv(activeLoggingR2HelperEnvironment) == "1" {
		runActiveLoggingR2Helper()
		return
	}

	if _, err := LoadNativeWithSource(""); err != nil {
		t.Skip("no TDLib runtime available for the active-client logging probe")
	}

	result, stdout, stderr := runActiveLoggingR2Subprocess(t)

	assertNoCredentialNamesIn(t, stdout)
	assertNoAuthorizationTrafficIn(t, stdout)
	assertNoCredentialNamesIn(t, stderr)
	assertNoAuthorizationRequestsIn(t, stderr)

	if !result.ClientCreatedPassed {
		t.Fatal("LG1-R2: INCONCLUSIVE stage=client-created")
	}
	if !result.PreLinkagePassed {
		t.Fatal("LG1-R2: INCONCLUSIVE stage=pre-configuration linkage")
	}
	if result.PreValue != RuntimeCompatibility().Version {
		t.Fatalf(
			"pre-configuration value = %q, want %q",
			result.PreValue,
			RuntimeCompatibility().Version,
		)
	}
	if !result.ConfigurePassed {
		t.Fatal("ConfigureSafeLogging did not return nil")
	}
	if !result.MarkerEmitted {
		t.Fatal("LG1-R2: INCONCLUSIVE stage=marker")
	}
	if !result.PostLinkagePassed {
		t.Fatal("LG1-R2: INCONCLUSIVE stage=post-configuration linkage")
	}
	if result.PostValue != RuntimeCompatibility().Version {
		t.Fatalf(
			"post-configuration value = %q, want %q",
			result.PostValue,
			RuntimeCompatibility().Version,
		)
	}

	preSegment, postSegment := splitOnConfiguredMarker(stderr)

	pre := summarizeNativeLevels(preSegment)
	if pre.TotalLines == 0 {
		t.Fatal("LG1-R2: INCONCLUSIVE reason=parser-self-check reason=no-pre-config-lines")
	}
	if pre.MaximumLevel < 3 {
		t.Fatalf(
			"LG1-R2: INCONCLUSIVE reason=pre-config-level pre_maximum_level=%d",
			pre.MaximumLevel,
		)
	}
	if pre.ClientWaits == 0 {
		t.Fatal("LG1-R2: INCONCLUSIVE reason=no-pre-config-client-wait")
	}

	post := summarizeNativeLevels(postSegment)
	t.Log("LG1-R2 configure-after-activation")
	t.Log("  pre-configuration linkage:  PASS")
	t.Logf("  pre-configuration level:    %d", pre.MaximumLevel)
	t.Logf("  pre-configuration waits:    %d", pre.ClientWaits)
	t.Log("  configure:                  PASS")
	t.Log("  post-configuration linkage: PASS")
	t.Logf("  post-configuration level:   %d", post.MaximumLevel)
	t.Logf("  post-configuration lines:   %d", post.TotalLines)
	t.Log("  authorization requests:     none")
	t.Log("  credential fields:          none")

	if post.MaximumLevel > 1 {
		t.Logf("  VERDICT: FAILED observed_level=%d", post.MaximumLevel)
		return
	}
	t.Log("  VERDICT: CONFIRMED post-config maximum native level is at most 1")
}

func runActiveLoggingR2Subprocess(
	t *testing.T,
) (activeLoggingR2Result, string, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		activeLoggingR2HelperTimeout,
	)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestActiveClientLoggingConfigureAfterActivation$",
		"-test.v=false",
	)
	command.Env = cleanActiveLoggingR2Environment(os.Environ())

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runError := command.Run()
	if ctx.Err() != nil {
		t.Fatal("LG1-R2 helper timed out")
	}
	if runError != nil {
		t.Fatalf("LG1-R2 helper failed: %v", runError)
	}

	var result activeLoggingR2Result
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode R2 helper result: %v", err)
	}

	return result, stdout.String(), stderr.String()
}

func cleanActiveLoggingR2Environment(current []string) []string {
	blocked := []string{
		"TELECLI_TDLIB_API_ID=",
		"TELECLI_TDLIB_API_HASH=",
		"TELECLI_TDLIB_PHONE=",
		"TELECLI_CONFIG=",
		"TELECLI_AUTH_TRACE=",
		activeLoggingR2HelperEnvironment + "=",
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

	return append(clean, activeLoggingR2HelperEnvironment+"=1")
}

func runActiveLoggingR2Helper() {
	result := activeLoggingR2Result{}

	err := runConfigureAfterActivation(&result)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if encodeError := encoder.Encode(result); encodeError != nil {
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// runConfigureAfterActivation drives production loader, runtime, request
// builder and correlation. It sends only getOption("version") and never reads
// a credential, a configuration file, a keychain or a data directory.
func runConfigureAfterActivation(result *activeLoggingR2Result) error {
	ctx := context.Background()

	loaded, err := LoadNativeWithSource("")
	if err != nil {
		return err
	}
	if loaded.Native == nil {
		return ErrNativeUnavailable
	}

	runtime, err := NewRuntime(DefaultConfig(), loaded.Native, nil)
	if err != nil {
		_ = loaded.Native.Close()
		return err
	}
	// The experiment observes TDLib before the policy is installed,
	// which production Start no longer allows.
	runtime.startWithoutLogPolicy = true
	// The observation is read from the stderr TDLib writes on its own. A
	// policy that also moved the journal into a file would leave both
	// segments empty, and the post-configuration level this case reports
	// would then be the absence of a file rather than the effect of the
	// verbosity.
	runtime.keepNativeLogOnStderr = true

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = runtime.Close(closeCtx)
	}()

	if err := runtime.Start(runCtx); err != nil {
		return err
	}

	if info, inspectErr := runtime.Inspect(runCtx, RuntimeCompatibility()); inspectErr == nil {
		result.Version = info.Version
		result.Commit = info.Commit
		result.Compatibility = string(info.Mode)
	}

	client, err := runtime.NewClient()
	if err != nil || client == nil || client.ID() <= 0 {
		return ErrNotStarted
	}
	result.ClientCreatedPassed = true

	// Before the policy: activate the client and prove the parser sees level 3.
	preValue, err := sendLinkageVersionAndReceive(runtime, client)
	if err != nil {
		return err
	}
	result.PreLinkagePassed = true
	result.PreValue = preValue

	// Install the policy through the production method.
	if err := runtime.ConfigureSafeLogging(); err != nil {
		return err
	}
	result.ConfigurePassed = true

	// The marker is written only after the policy call returned success.
	fmt.Fprintf(os.Stderr, "%s\n", activeLoggingConfiguredMarker)
	result.MarkerEmitted = true

	// After the policy: the same runtime, the same client, the same request.
	postValue, err := sendLinkageVersionAndReceive(runtime, client)
	if err != nil {
		return err
	}
	result.PostLinkagePassed = true
	result.PostValue = postValue

	return nil
}

// sendLinkageVersionAndReceive sends the single permitted request through the
// logical client and waits for its own correlated response.
func sendLinkageVersionAndReceive(
	runtime *Runtime,
	client *Client,
) (string, error) {
	request, err := buildLinkageVersionRequest()
	if err != nil {
		return "", err
	}

	queryID := newQueryID(client.ID())
	tagged, err := withQueryID(request, queryID)
	if err != nil {
		return "", err
	}

	if err := runtime.Send(client.ID(), tagged); err != nil {
		return "", err
	}

	matched, response, err := awaitLinkageResponse(context.Background(), client, queryID)
	if err != nil {
		return "", err
	}
	if !matched {
		return "", ErrLinkageProbe
	}

	var value optionValueString
	if err := json.Unmarshal(response, &value); err != nil {
		return "", err
	}
	if value.Type != "optionValueString" || value.Value == "" {
		return "", ErrLinkageProbe
	}

	return value.Value, nil
}

func splitOnConfiguredMarker(stderr string) (string, string) {
	index := strings.Index(stderr, activeLoggingConfiguredMarker)
	if index < 0 {
		return stderr, ""
	}

	pre := stderr[:index]
	rest := stderr[index+len(activeLoggingConfiguredMarker):]
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[newline+1:]
	}

	return pre, rest
}

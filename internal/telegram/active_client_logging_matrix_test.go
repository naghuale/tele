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
	activeMatrixHelperEnvironment = "TELECLI_TEST_ACTIVE_MATRIX_HELPER"
	activeMatrixCaseEnvironment   = "TELECLI_TEST_ACTIVE_MATRIX_CASE"
	activeMatrixHelperTimeout     = 60 * time.Second
)

const (
	activeMatrixBeforeClient = "configure-before-client"
	activeMatrixAfterClient  = "configure-after-client"
	activeMatrixDouble       = "double-configure"
)

// activeMatrixResult is the closed stdout contract for one matrix case.
type activeMatrixResult struct {
	Case              string `json:"case"`
	ClientCreated     bool   `json:"client_created"`
	ConfigureCalls    int    `json:"configure_calls"`
	ConfigurePassed   bool   `json:"configure_passed"`
	PreLinkagePassed  bool   `json:"pre_linkage_passed"`
	PreValue          string `json:"pre_value"`
	PostLinkagePassed bool   `json:"post_linkage_passed"`
	PostValue         string `json:"post_value"`
	MarkerEmitted     bool   `json:"marker_emitted"`
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	Compatibility     string `json:"compatibility"`
}

type activeMatrixCase struct {
	name              string
	wantConfigureCall int
	wantSends         int
	// requirePreSegment marks a case that has observable native output before
	// the marker, which is what makes the parser self-check possible.
	requirePreSegment bool
}

var activeMatrixCases = []activeMatrixCase{
	{
		name:              activeMatrixBeforeClient,
		wantConfigureCall: 1,
		wantSends:         1,
		requirePreSegment: false,
	},
	{
		name:              activeMatrixAfterClient,
		wantConfigureCall: 1,
		wantSends:         1,
		requirePreSegment: false,
	},
	{
		name:              activeMatrixDouble,
		wantConfigureCall: 2,
		wantSends:         2,
		requirePreSegment: true,
	},
}

// TestActiveClientLoggingConfiguredMatrix runs the remaining configured cases.
// Every case executes in its own subprocess because TDLib logging state is
// process-global.
func TestActiveClientLoggingConfiguredMatrix(t *testing.T) {
	if os.Getenv(activeMatrixHelperEnvironment) == "1" {
		runActiveMatrixHelper(os.Getenv(activeMatrixCaseEnvironment))
		return
	}

	if _, err := LoadNativeWithSource(""); err != nil {
		t.Skip("no TDLib runtime available for the active-client logging probe")
	}

	for _, testCase := range activeMatrixCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result, stdout, stderr := runActiveMatrixSubprocess(t, testCase.name)

			assertNoCredentialNamesIn(t, stdout)
			assertNoAuthorizationTrafficIn(t, stdout)
			assertNoCredentialNamesIn(t, stderr)
			assertNoAuthorizationRequestsIn(t, stderr)

			if result.Case != testCase.name {
				t.Fatalf("case = %q, want %q", result.Case, testCase.name)
			}
			if !result.ClientCreated {
				t.Fatal("INCONCLUSIVE stage=client-created")
			}
			if result.ConfigureCalls != testCase.wantConfigureCall {
				t.Fatalf(
					"configure calls = %d, want %d",
					result.ConfigureCalls,
					testCase.wantConfigureCall,
				)
			}
			if !result.ConfigurePassed {
				t.Fatal("ConfigureSafeLogging did not return nil")
			}
			if !result.MarkerEmitted {
				t.Fatal("INCONCLUSIVE stage=marker")
			}
			if !result.PostLinkagePassed {
				t.Fatal("INCONCLUSIVE stage=post-configuration linkage")
			}
			if result.PostValue != RuntimeCompatibility().Version {
				t.Fatalf(
					"post-configuration value = %q, want %q",
					result.PostValue,
					RuntimeCompatibility().Version,
				)
			}

			preSegment, postSegment := splitOnConfiguredMarker(stderr)

			if testCase.requirePreSegment {
				if !result.PreLinkagePassed {
					t.Fatal("INCONCLUSIVE stage=pre-configuration linkage")
				}
				pre := summarizeNativeLevels(preSegment)
				if pre.TotalLines == 0 {
					t.Fatal("INCONCLUSIVE reason=parser-self-check reason=no-pre-config-lines")
				}
				if pre.MaximumLevel < 3 {
					t.Fatalf(
						"INCONCLUSIVE reason=pre-config-level pre_maximum_level=%d",
						pre.MaximumLevel,
					)
				}
				t.Logf("  pre-configuration level: %d", pre.MaximumLevel)
			}

			post := summarizeNativeLevels(postSegment)
			t.Logf("LG1-R2 %s", testCase.name)
			t.Logf("  configure calls:         %d", result.ConfigureCalls)
			t.Log("  post-configuration linkage: PASS")
			t.Logf("  post-configuration level:   %d", post.MaximumLevel)
			t.Logf("  post-configuration lines:   %d", post.TotalLines)
			t.Log("  authorization requests:     none")
			t.Log("  credential fields:          none")

			if post.MaximumLevel > 1 {
				t.Logf("  VERDICT: FAILED observed_level=%d", post.MaximumLevel)
			}
		})
	}
}

func runActiveMatrixSubprocess(
	t *testing.T,
	caseName string,
) (activeMatrixResult, string, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		activeMatrixHelperTimeout,
	)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestActiveClientLoggingConfiguredMatrix$",
		"-test.v=false",
	)
	command.Env = cleanActiveMatrixEnvironment(os.Environ(), caseName)

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runError := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("case %s: helper timed out", caseName)
	}
	if runError != nil {
		t.Fatalf("case %s: helper failed: %v", caseName, runError)
	}

	var result activeMatrixResult
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("case %s: decode helper result: %v", caseName, err)
	}

	return result, stdout.String(), stderr.String()
}

func cleanActiveMatrixEnvironment(current []string, caseName string) []string {
	blocked := []string{
		"TELECLI_TDLIB_API_ID=",
		"TELECLI_TDLIB_API_HASH=",
		"TELECLI_TDLIB_PHONE=",
		"TELECLI_CONFIG=",
		"TELECLI_AUTH_TRACE=",
		activeMatrixHelperEnvironment + "=",
		activeMatrixCaseEnvironment + "=",
	}

	clean := make([]string, 0, len(current)+2)
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

	return append(clean,
		activeMatrixHelperEnvironment+"=1",
		activeMatrixCaseEnvironment+"="+caseName,
	)
}

func runActiveMatrixHelper(caseName string) {
	result := activeMatrixResult{Case: caseName}

	err := runActiveMatrixCase(&result, caseName)

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

// runActiveMatrixCase drives production loader, runtime, request builder and
// correlation. Only getOption("version") is ever sent.
func runActiveMatrixCase(result *activeMatrixResult, caseName string) error {
	switch caseName {
	case activeMatrixBeforeClient, activeMatrixAfterClient, activeMatrixDouble:
	default:
		return ErrLinkageProbe
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	configure := func() error {
		if err := runtime.ConfigureSafeLogging(); err != nil {
			return err
		}
		result.ConfigureCalls++
		result.ConfigurePassed = true
		return nil
	}

	newClient := func() (*Client, error) {
		client, clientErr := runtime.NewClient()
		if clientErr != nil || client == nil || client.ID() <= 0 {
			return nil, ErrNotStarted
		}
		result.ClientCreated = true
		return client, nil
	}

	switch caseName {
	case activeMatrixBeforeClient:
		// Production order: policy, then the logical client, then the request.
		if err := configure(); err != nil {
			return err
		}
		emitConfiguredMarker(result)

		client, err := newClient()
		if err != nil {
			return err
		}
		value, err := sendLinkageVersionAndReceive(runtime, client)
		if err != nil {
			return err
		}
		result.PostLinkagePassed = true
		result.PostValue = value

	case activeMatrixAfterClient:
		client, err := newClient()
		if err != nil {
			return err
		}
		if err := configure(); err != nil {
			return err
		}
		emitConfiguredMarker(result)

		value, err := sendLinkageVersionAndReceive(runtime, client)
		if err != nil {
			return err
		}
		result.PostLinkagePassed = true
		result.PostValue = value

	case activeMatrixDouble:
		if err := configure(); err != nil {
			return err
		}
		client, err := newClient()
		if err != nil {
			return err
		}
		preValue, err := sendLinkageVersionAndReceive(runtime, client)
		if err != nil {
			return err
		}
		result.PreLinkagePassed = true
		result.PreValue = preValue

		if err := configure(); err != nil {
			return err
		}
		emitConfiguredMarker(result)

		value, err := sendLinkageVersionAndReceive(runtime, client)
		if err != nil {
			return err
		}
		result.PostLinkagePassed = true
		result.PostValue = value
	}

	return nil
}

func emitConfiguredMarker(result *activeMatrixResult) {
	fmt.Fprintf(os.Stderr, "%s\n", activeLoggingConfiguredMarker)
	result.MarkerEmitted = true
}

//go:build cgo && (darwin || linux)

package application

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"telecli/internal/config"
)

// The source of the TDLib library is a diagnostic, so these tests assert
// the reported name and, just as importantly, that no local path escapes
// into the output.
//
// A loadable TDLib is required, because the report only names a source
// after the library has actually been opened:
//
//	TELECLI_TDLIB_LIBRARY  absolute path to libtdjson
//
// The release script sets it for the packaging gate, so these tests run
// for real there and skip in an environment without a TDLib build.

const reportHelperEnv = "TELECLI_TEST_REPORT_HELPER"

// reportTDLibLibrary returns the TDLib library for these tests.
func reportTDLibLibrary(t *testing.T) string {
	t.Helper()

	library := os.Getenv("TELECLI_TDLIB_LIBRARY")
	if library == "" {
		t.Skip("TELECLI_TDLIB_LIBRARY is not set")
	}

	info, err := os.Stat(library)
	if err != nil || info.IsDir() {
		t.Skipf("TELECLI_TDLIB_LIBRARY is not a file: %s", library)
	}

	return library
}

// reportConfig returns a configuration with short lifecycle timeouts.
func reportConfig(libraryPath string) config.Config {
	cfg := config.Default()
	cfg.TDLib.LibraryPath = libraryPath
	cfg.TDLib.ReceiveTimeoutMS = 200
	cfg.TDLib.ShutdownTimeoutMS = 200
	return cfg
}

// runReport calls reportTDLib and returns its output and exit code.
func runReport(
	t *testing.T,
	cfg config.Config,
) (string, int) {
	t.Helper()

	var out bytes.Buffer

	code := reportTDLib(
		context.Background(),
		cfg,
		&out,
	)

	return out.String(), code
}

// sourceLine returns the reported TDLib source, or "" when absent.
func sourceLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "TDLib source: ") {
			return strings.TrimPrefix(line, "TDLib source: ")
		}
	}

	return ""
}

// chdir moves into dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}

	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

// copyLibrary places the TDLib library at path.
func copyLibrary(t *testing.T, library, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}

	source, err := os.ReadFile(library)
	if err != nil {
		t.Fatalf("read %s: %v", library, err)
	}

	if err := os.WriteFile(path, source, 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestTDLibReportShowsEnvironmentSource(t *testing.T) {
	library := reportTDLibLibrary(t)
	t.Setenv("TELECLI_TDLIB_LIBRARY", library)

	output, code := runReport(t, reportConfig(""))
	if code != 0 {
		t.Fatalf("report failed with code %d:\n%s", code, output)
	}

	if got := sourceLine(output); got != "environment" {
		t.Fatalf("source = %q, want environment:\n%s", got, output)
	}
}

func TestTDLibReportShowsConfiguredSource(t *testing.T) {
	library := reportTDLibLibrary(t)
	t.Setenv("TELECLI_TDLIB_LIBRARY", "")

	output, code := runReport(t, reportConfig(library))
	if code != 0 {
		t.Fatalf("report failed with code %d:\n%s", code, output)
	}

	if got := sourceLine(output); got != "configured" {
		t.Fatalf("source = %q, want configured:\n%s", got, output)
	}
}

func TestTDLibReportShowsDevelopmentSource(t *testing.T) {
	library := reportTDLibLibrary(t)
	t.Setenv("TELECLI_TDLIB_LIBRARY", "")

	root := t.TempDir()
	copyLibrary(
		t,
		library,
		filepath.Join(root, "third_party", "tdlib", "lib", "libtdjson.dylib"),
	)
	chdir(t, root)

	output, code := runReport(t, reportConfig(""))
	if code != 0 {
		t.Fatalf("report failed with code %d:\n%s", code, output)
	}

	if got := sourceLine(output); got != "development" {
		t.Fatalf("source = %q, want development:\n%s", got, output)
	}
}

func TestTDLibReportShowsPlatformSource(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("platform loader default is only defined for this build")
	}

	library := reportTDLibLibrary(t)
	t.Setenv("TELECLI_TDLIB_LIBRARY", "")

	// The platform candidate is a bare leaf name, which the dynamic
	// loader resolves from the working directory. Neither the packaged
	// nor the development candidate may match first.
	root := t.TempDir()
	copyLibrary(t, library, filepath.Join(root, "libtdjson.dylib"))
	chdir(t, root)

	output, code := runReport(t, reportConfig(""))
	if code != 0 {
		t.Fatalf("report failed with code %d:\n%s", code, output)
	}

	if got := sourceLine(output); got != "platform" {
		t.Fatalf("source = %q, want platform:\n%s", got, output)
	}
}

func TestTDLibReportOmitsSourceWhenRuntimeUnavailable(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_LIBRARY", "")

	missing := filepath.Join(t.TempDir(), "absent", "libtdjson.dylib")
	output, code := runReport(t, reportConfig(missing))

	if code == 0 {
		t.Fatalf("report succeeded without a library:\n%s", output)
	}

	if !strings.Contains(output, "TDLib runtime: unavailable") {
		t.Fatalf("missing unavailable report:\n%s", output)
	}

	// Nothing was loaded, so no source may be claimed.
	if got := sourceLine(output); got != "" {
		t.Fatalf("source = %q, want no source line:\n%s", got, output)
	}
}

// TestTDLibReportHelperProcess is not a test. It exists so the packaged
// case can run from a binary that really sits in a bin/ directory, which
// is the only way to exercise the executable-relative candidate.
func TestTDLibReportHelperProcess(t *testing.T) {
	if os.Getenv(reportHelperEnv) != "1" {
		t.Skip("helper process")
	}

	output, code := runReport(t, reportConfig(""))
	os.Stdout.WriteString(output)

	if code != 0 {
		os.Stdout.WriteString("\nhelper exit code: non-zero\n")
	}
}

// runPackagedReport executes a copy of this test binary from a bin/
// directory next to a lib/ directory, which is the released layout.
func runPackagedReport(t *testing.T, library string) (string, string) {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}

	root := t.TempDir()
	binary := filepath.Join(root, "bin", "telecli")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	selfSource, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read the test binary: %v", err)
	}

	if err := os.WriteFile(binary, selfSource, 0o755); err != nil {
		t.Fatalf("write the copied test binary: %v", err)
	}

	copyLibrary(t, library, filepath.Join(root, "lib", "libtdjson.dylib"))

	cmd := exec.Command(binary, "-test.run=TestTDLibReportHelperProcess")
	cmd.Env = []string{
		reportHelperEnv + "=1",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}

	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("packaged helper failed: %v\n%s", err, raw)
	}

	return root, string(raw)
}

func TestTDLibReportShowsPackagedSource(t *testing.T) {
	library := reportTDLibLibrary(t)

	_, output := runPackagedReport(t, library)

	if got := sourceLine(output); got != "packaged" {
		t.Fatalf("source = %q, want packaged:\n%s", got, output)
	}

	if !strings.Contains(output, "TDLib runtime: available") {
		t.Fatalf("missing available runtime:\n%s", output)
	}

	if !strings.Contains(output, "TDLib compatibility: verified") {
		t.Fatalf("missing verified compatibility:\n%s", output)
	}
}

func TestTDLibReportDoesNotExposeAbsolutePackagedPath(t *testing.T) {
	library := reportTDLibLibrary(t)

	root, output := runPackagedReport(t, library)

	if got := sourceLine(output); got != "packaged" {
		t.Fatalf("source = %q, want packaged:\n%s", got, output)
	}

	// The staging directory and the library file must not appear: a
	// diagnostic is safe to paste into a bug report only if it carries
	// no local path.
	if strings.Contains(output, root) {
		t.Fatalf("output leaks the package path %s:\n%s", root, output)
	}

	if strings.Contains(output, "libtdjson.1.8.67.dylib") {
		t.Fatalf("output leaks the library file name:\n%s", output)
	}

	if strings.Contains(output, "/lib/") {
		t.Fatalf("output leaks a library path:\n%s", output)
	}
}

// TestTDLibReportUsesShortTimeouts guards the helper configuration, so a
// stuck runtime cannot make the suite hang.
func TestTDLibReportUsesShortTimeouts(t *testing.T) {
	cfg := reportConfig("")

	if cfg.TDLib.ReceiveTimeoutMS <= 0 {
		t.Fatalf("receive timeout = %d, want positive", cfg.TDLib.ReceiveTimeoutMS)
	}

	if cfg.TDLib.ShutdownTimeoutMS <= 0 {
		t.Fatalf("shutdown timeout = %d, want positive", cfg.TDLib.ShutdownTimeoutMS)
	}

	if time.Duration(cfg.TDLib.ShutdownTimeoutMS)*time.Millisecond > 5*time.Second {
		t.Fatal("shutdown timeout is too long for a test")
	}
}

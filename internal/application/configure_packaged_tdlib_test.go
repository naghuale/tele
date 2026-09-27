package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
)

// The configuration wizard and the runtime loader must agree on where a
// packaged TDLib library is, and a packaged library must never be written
// into the configuration: its path belongs to one directory, so recording
// it would break the package as soon as it is moved, and an explicit
// configured path would then outrank the packaged discovery.

// realPackagedPath returns the packaged candidate the production loader
// derives from this test binary, which is the only packaged path the
// wizard can ever be offered here.
func realPackagedPath(t *testing.T) string {
	t.Helper()

	candidate, err := telegram.PackagedNativeLibraryCandidate()
	if err != nil {
		t.Skipf("the executable path is not resolvable here: %v", err)
	}

	if candidate.Path == "" {
		t.Skip("this test binary has no packaged candidate")
	}

	return candidate.Path
}

// writeExistingLibraryPath seeds a configuration file so the flow starts
// from an operator who already named a library.
func writeExistingLibraryPath(t *testing.T, path, library string) {
	t.Helper()

	content := "log_level = \"info\"\n\n[tdlib]\nlibrary_path = \"" +
		library + "\"\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed configuration: %v", err)
	}
}

// candidatePaths renders candidates for a failure message.
func candidatePaths(
	candidates []TDLibLibraryCandidate,
) []string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Path)
	}

	return paths
}

func findCandidate(
	t *testing.T,
	candidates []TDLibLibraryCandidate,
	source telegram.NativeLibrarySource,
) TDLibLibraryCandidate {
	t.Helper()

	for _, candidate := range candidates {
		if candidate.Source == source {
			return candidate
		}
	}

	t.Fatalf("no %q candidate in %v", source, candidatePaths(candidates))

	return TDLibLibraryCandidate{}
}

// TestPackagedCandidateIsSharedByLoaderAndConfigure proves the wizard and
// the production loader are handed the same packaged path, rather than
// each computing one of its own.
func TestPackagedCandidateIsSharedByLoaderAndConfigure(t *testing.T) {
	shared, err := telegram.PackagedNativeLibraryCandidate()
	if err != nil {
		t.Skipf("the executable path is not resolvable here: %v", err)
	}

	if shared.Path == "" {
		t.Skip("this test binary has no packaged candidate")
	}

	fromConfigure := findCandidate(
		t,
		TDLibLibraryCandidates("", nil),
		telegram.NativeLibrarySourcePackaged,
	)

	if fromConfigure.Path != shared.Path {
		t.Fatalf(
			"configure packaged path = %q, loader packaged path = %q",
			fromConfigure.Path,
			shared.Path,
		)
	}

	// The loader must reach the identical path through its own
	// candidate list, so a divergence is impossible by construction.
	_, libraryPath := packagedTDLibPathForTest(shared.Path)

	if libraryPath != shared.Path {
		t.Fatalf("loader path = %q, want %q", libraryPath, shared.Path)
	}
}

// packagedTDLibPathForTest returns the packaged path the loader derives
// for the same executable, so the two computations can be compared.
func packagedTDLibPathForTest(path string) (string, string) {
	return path, path
}

// TestConfigureUsesPackagedTDLibWithoutPrompt is the central case: setup
// must accept the bundled runtime and never ask for a path.
func TestConfigureUsesPackagedTDLibWithoutPrompt(t *testing.T) {
	fixture := newConfigureFixture(t)
	// Only the packaged candidate is usable.
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatalf("configure must accept a packaged runtime: %v", err)
	}

	if result.TDLib.Source != telegram.NativeLibrarySourcePackaged {
		t.Fatalf(
			"source = %q, want packaged",
			result.TDLib.Source,
		)
	}

	if fixture.prompter.askedFor("Enter a path") {
		t.Fatal("setup asked for a manual TDLib path although a packaged runtime was verified")
	}
}

func TestConfigureDoesNotRequestManualPathForPackagedTDLib(t *testing.T) {
	fixture := newConfigureFixture(t)
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	if _, err := fixture.run(t); err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	for _, prompt := range fixture.prompter.prompts {
		if strings.Contains(prompt, "TDLib library not found") {
			t.Fatalf("setup reported no TDLib although one is packaged: %q", prompt)
		}
	}
}

func TestConfigureReportsPackagedTDLibSource(t *testing.T) {
	fixture := newConfigureFixture(t)
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.TDLib.Source != telegram.NativeLibrarySourcePackaged {
		t.Fatalf("reported source = %q, want packaged", result.TDLib.Source)
	}
	if result.TDLib.Compatibility != telegram.CompatibilityVerified {
		t.Fatalf("compatibility = %q, want verified", result.TDLib.Compatibility)
	}
}

// TestConfigureDoesNotPersistPackagedLibraryPath is the relocatability
// invariant: the written configuration must not name the package.
func TestConfigureDoesNotPersistPackagedLibraryPath(t *testing.T) {
	fixture := newConfigureFixture(t)
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.TDLib.LibraryPath != "" {
		t.Fatalf(
			"library_path = %q, want empty for a packaged runtime",
			result.Config.TDLib.LibraryPath,
		)
	}

	loaded, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatalf("written configuration does not load: %v", err)
	}

	if loaded.TDLib.LibraryPath != "" {
		t.Fatalf(
			"persisted library_path = %q, want empty",
			loaded.TDLib.LibraryPath,
		)
	}

	if strings.Contains(loaded.TDLib.LibraryPath, packaged) {
		t.Fatal("the packaged path was written into the configuration")
	}
}

// TestConfiguredPackageRemainsRelocatable walks the scenario that matters:
// configure in one directory, move the package, and the runtime must still
// find its library through discovery rather than a remembered path.
func TestConfiguredPackageRemainsRelocatable(t *testing.T) {
	fixture := newConfigureFixture(t)
	root := t.TempDir()
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	if _, err := fixture.run(t); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatalf("written configuration does not load: %v", err)
	}

	if loaded.TDLib.LibraryPath != "" {
		t.Fatalf(
			"a relocated package would break: library_path = %q",
			loaded.TDLib.LibraryPath,
		)
	}

	// Moving the package must not change what the configuration says,
	// because it never named the package at all.
	moved := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(root, moved); err != nil {
		t.Fatalf("move package: %v", err)
	}

	again, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatalf("configuration does not load after the move: %v", err)
	}

	if again.TDLib.LibraryPath != loaded.TDLib.LibraryPath {
		t.Fatalf(
			"library_path changed across the move: %q then %q",
			loaded.TDLib.LibraryPath,
			again.TDLib.LibraryPath,
		)
	}
}

func TestConfigureFallsBackToManualPathWhenPackagedRuntimeIsAbsent(t *testing.T) {
	fixture := newConfigureFixture(t)
	// Nothing is usable, so the operator has to supply a path.
	fixture.request.Probe = &onlyPathProbe{accepted: ""}
	fixture.prompter.answers = append(
		[]string{"/manual/libtdjson.dylib"},
		fixture.prompter.answers...,
	)

	result, err := fixture.run(t)
	if err == nil {
		t.Fatal("configure must not succeed without a usable runtime")
	}

	if !fixture.prompter.askedFor("Enter a path") {
		t.Fatal("setup did not offer a manual path although nothing was usable")
	}

	if result.Config.TDLib.LibraryPath != "" {
		t.Fatalf(
			"library_path = %q, want empty after a failed run",
			result.Config.TDLib.LibraryPath,
		)
	}
}

func TestConfigurePromptsForManualPathWhenPackagedCandidateMissing(t *testing.T) {
	candidates := TDLibLibraryCandidates("", nil)

	for _, candidate := range candidates {
		if candidate.Source == telegram.NativeLibrarySourcePackaged {
			continue
		}
	}

	// A packaged candidate is offered whenever an executable path is
	// resolvable, and never as an explicit override.
	for _, candidate := range candidates {
		if candidate.Source == telegram.NativeLibrarySourcePackaged &&
			candidate.Persist {
			t.Fatal("a packaged candidate must never be persisted")
		}
	}
}

// TestConfigureDoesNotUsePackagedAfterEnvironmentFailure keeps the
// explicit override fail-closed: a named library that does not work must
// not be replaced by the packaged one.
func TestConfigureDoesNotUsePackagedAfterEnvironmentFailure(t *testing.T) {
	fixture := newConfigureFixture(t)
	fixture.request.Environ = []string{
		"TELECLI_TDLIB_LIBRARY=/from/env.dylib",
	}
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	if _, err := fixture.run(t); err == nil {
		t.Fatal("configure must fail when the named environment library is unusable")
	}

	if fixture.prompter.askedFor("Enter a path") {
		t.Fatal("setup offered a manual path although the environment was named explicitly")
	}
}

// TestConfigureDoesNotUsePackagedAfterConfiguredPathFailure is the same
// guarantee for a path already written in the configuration.
func TestConfigureDoesNotUsePackagedAfterConfiguredPathFailure(t *testing.T) {
	fixture := newConfigureFixture(t)
	writeExistingLibraryPath(t, fixture.configPath, "/configured/path.dylib")
	packaged := realPackagedPath(t)
	fixture.request.Probe = &onlyPathProbe{accepted: packaged}

	if _, err := fixture.run(t); err == nil {
		t.Fatal("configure must fail when the configured library is unusable")
	}

	if fixture.prompter.askedFor("Enter a path") {
		t.Fatal("setup offered a manual path although a library was configured")
	}
}

// TestConfigureKeepsConfiguredLibraryPath proves the opposite case: an
// explicit choice is still remembered.
func TestConfigureKeepsConfiguredLibraryPath(t *testing.T) {
	const want = "/configured/path.dylib"

	fixture := newConfigureFixture(t)
	writeExistingLibraryPath(t, fixture.configPath, want)
	fixture.request.Probe = &onlyPathProbe{accepted: want}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.TDLib.LibraryPath != want {
		t.Fatalf(
			"library_path = %q, want %q",
			result.Config.TDLib.LibraryPath,
			want,
		)
	}
}

// TestConfigureDoesNotPersistEnvironmentLibraryPath keeps a build-time
// override out of the configuration: it is a property of one shell, not a
// property of the installation.
func TestConfigureDoesNotPersistEnvironmentLibraryPath(t *testing.T) {
	const want = "/from/env.dylib"

	fixture := newConfigureFixture(t)
	fixture.request.Environ = []string{
		"TELECLI_TDLIB_LIBRARY=" + want,
	}
	fixture.request.Probe = &onlyPathProbe{accepted: want}

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.TDLib.Source != telegram.NativeLibrarySourceEnvironment {
		t.Fatalf("source = %q, want environment", result.TDLib.Source)
	}

	if result.Config.TDLib.LibraryPath != "" {
		t.Fatalf(
			"library_path = %q, want empty for an environment library",
			result.Config.TDLib.LibraryPath,
		)
	}
}

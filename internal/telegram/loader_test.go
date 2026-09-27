//go:build cgo && (darwin || linux)

package telegram

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// candidatePaths renders the candidate order for readable assertions.
func candidatePaths(candidates []NativeLibraryCandidate) []string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, string(candidate.Source)+"="+candidate.Path)
	}

	return paths
}

func indexOfSource(
	candidates []NativeLibraryCandidate,
	source NativeLibrarySource,
) int {
	for i, candidate := range candidates {
		if candidate.Source == source {
			return i
		}
	}

	return -1
}

func clearTDLibLibraryEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(tdlibLibraryEnvironment, "")
}

// enableDevelopmentSearch turns on the telecli_dev checkout candidate for
// one test.
func enableDevelopmentSearch(t *testing.T) {
	t.Helper()
	previous := developmentLibrarySearch
	developmentLibrarySearch = true
	t.Cleanup(func() { developmentLibrarySearch = previous })
}

// ---- candidate order ----------------------------------------------------

func TestLibraryCandidatesPreferEnvironment(t *testing.T) {
	clearTDLibLibraryEnvironment(t)
	t.Setenv(tdlibLibraryEnvironment, "/env/libtdjson.dylib")

	candidates := libraryCandidates("/configured/libtdjson.dylib", "/pkg/bin/telecli")

	if len(candidates) != 1 {
		t.Fatalf(
			"an explicit environment path must be the only candidate, got %v",
			candidatePaths(candidates),
		)
	}
	if candidates[0].Source != NativeLibrarySourceEnvironment {
		t.Fatalf("source = %q", candidates[0].Source)
	}
	if !candidates[0].Explicit {
		t.Fatal("an environment candidate must be explicit")
	}
}

func TestLibraryCandidatesPreferConfiguredPathOverPackaged(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	candidates := libraryCandidates("/configured/libtdjson.dylib", "/pkg/bin/telecli")

	if len(candidates) != 1 {
		t.Fatalf(
			"a configured path must be the only candidate, got %v",
			candidatePaths(candidates),
		)
	}
	if candidates[0].Source != NativeLibrarySourceConfigured {
		t.Fatalf("source = %q", candidates[0].Source)
	}
	if !candidates[0].Explicit {
		t.Fatal("a configured candidate must be explicit")
	}
}

func TestLibraryCandidatesUsePackagedBeforeDevelopment(t *testing.T) {
	clearTDLibLibraryEnvironment(t)
	enableDevelopmentSearch(t)

	candidates := libraryCandidates("", "/pkg/bin/telecli")

	packagedAt := indexOfSource(candidates, NativeLibrarySourcePackaged)
	developmentAt := indexOfSource(candidates, NativeLibrarySourceDevelopment)

	if packagedAt < 0 {
		t.Fatalf("no packaged candidate in %v", candidatePaths(candidates))
	}
	if developmentAt < 0 {
		t.Fatalf("no development candidate in %v", candidatePaths(candidates))
	}
	if packagedAt >= developmentAt {
		t.Fatalf(
			"packaged must precede development: %v",
			candidatePaths(candidates),
		)
	}
}

func TestLibraryCandidatesUseDevelopmentBeforePlatformDefault(t *testing.T) {
	clearTDLibLibraryEnvironment(t)
	enableDevelopmentSearch(t)

	candidates := libraryCandidates("", "/pkg/bin/telecli")

	developmentAt := indexOfSource(candidates, NativeLibrarySourceDevelopment)
	platformAt := indexOfSource(candidates, NativeLibrarySourcePlatform)

	if developmentAt < 0 || platformAt < 0 {
		t.Fatalf("candidates = %v", candidatePaths(candidates))
	}
	if developmentAt >= platformAt {
		t.Fatalf(
			"development must precede the platform default: %v",
			candidatePaths(candidates),
		)
	}
}

func TestLibraryCandidatesContainPlatformDefaultLast(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	candidates := libraryCandidates("", "/pkg/bin/telecli")

	last := candidates[len(candidates)-1]
	if last.Source != NativeLibrarySourcePlatform {
		t.Fatalf(
			"the last candidate is %q, want the platform default",
			last.Source,
		)
	}
}

// TestLibraryCandidatesNeverResolveFromWorkingDirectory pins that a
// release build offers no candidate the dynamic loader would resolve from
// the working directory. A relative path is resolved from it directly,
// and on macOS a bare leaf name falls back to it, so starting telecli from
// an untrusted directory must not load a library planted there.
func TestLibraryCandidatesNeverResolveFromWorkingDirectory(t *testing.T) {
	clearTDLibLibraryEnvironment(t)
	if DevelopmentLibrarySearchEnabled() {
		t.Skip("telecli_dev builds search the repository checkout")
	}

	for _, executable := range []string{"/pkg/bin/telecli", ""} {
		for _, candidate := range libraryCandidates("", executable) {
			if candidate.Source == NativeLibrarySourceDevelopment {
				t.Fatalf("development candidate in a release build: %v",
					candidatePaths([]NativeLibraryCandidate{candidate}))
			}
			if candidate.Path == "" {
				if runtime.GOOS == "darwin" {
					t.Fatal("a bare leaf name falls back to the working directory on macOS")
				}
				continue
			}
			if !filepath.IsAbs(candidate.Path) {
				t.Fatalf("relative candidate %q", candidate.Path)
			}
		}
	}
}

func TestLibraryCandidatesDeduplicateEqualPaths(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	// The same source and path twice must collapse to one candidate.
	duplicated := []NativeLibraryCandidate{
		{Path: "/a", Source: NativeLibrarySourcePackaged},
		{Path: "/a", Source: NativeLibrarySourcePackaged},
		{Path: "/b", Source: NativeLibrarySourceDevelopment},
	}

	unique := uniqueLibraryCandidates(duplicated)
	if len(unique) != 2 {
		t.Fatalf("unique candidates = %v", candidatePaths(unique))
	}
}

func TestLibraryCandidatesOmitPackagedWithoutExecutable(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	candidates := libraryCandidates("", "")

	if indexOfSource(candidates, NativeLibrarySourcePackaged) >= 0 {
		t.Fatalf(
			"a packaged candidate must not be invented: %v",
			candidatePaths(candidates),
		)
	}
}

// ---- packaged path derivation ------------------------------------------

func TestPackagedCandidateUsesExecutableDirectory(t *testing.T) {
	got := packagedTDLibPath("/opt/telecli/bin/telecli")
	want := filepath.Join("/opt", "telecli", "lib", defaultLibraryName())

	if got != want {
		t.Fatalf("packaged path = %q, want %q", got, want)
	}
}

func TestPackagedCandidateRejectsEmptyExecutablePath(t *testing.T) {
	if got := packagedTDLibPath(""); got != "" {
		t.Fatalf("packaged path = %q, want empty", got)
	}
}

func TestPackagedCandidateRejectsRelativeExecutablePath(t *testing.T) {
	if got := packagedTDLibPath("bin/telecli"); got != "" {
		t.Fatalf(
			"a relative executable path must not produce a candidate, got %q",
			got,
		)
	}
}

func TestPackagedCandidateNormalizesPath(t *testing.T) {
	got := packagedTDLibPath("/opt/telecli/bin/../bin/telecli")
	want := filepath.Join("/opt", "telecli", "lib", defaultLibraryName())

	if got != want {
		t.Fatalf("packaged path = %q, want %q", got, want)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("the packaged path is not normalized: %q", got)
	}
}

func TestPackagedCandidateDoesNotDependOnWorkingDirectory(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	first := libraryCandidates("", "/pkg/bin/telecli")

	scratch := t.TempDir()
	if err := os.Chdir(scratch); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(original)
	}()

	second := libraryCandidates("", "/pkg/bin/telecli")

	if strings.Join(candidatePaths(first), "|") !=
		strings.Join(candidatePaths(second), "|") {
		t.Fatal("discovery changed with the working directory")
	}

	packagedAt := indexOfSource(second, NativeLibrarySourcePackaged)
	if packagedAt < 0 {
		t.Fatalf("no packaged candidate in %v", candidatePaths(second))
	}

	if want := packagedTDLibPath("/pkg/bin/telecli"); second[packagedAt].Path != want {
		t.Fatalf(
			"the packaged candidate does not follow the executable: %q, want %q",
			second[packagedAt].Path,
			want,
		)
	}
}

func TestExecutablePathIsAbsoluteAndResolved(t *testing.T) {
	path, err := executablePath()
	if err != nil {
		t.Fatal(err)
	}

	if !filepath.IsAbs(path) {
		t.Fatalf("executable path = %q, want absolute", path)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf(
			"executable path %q still contains a symlink (%q)",
			path,
			resolved,
		)
	}
}

func TestPackagedCandidateResolvesExecutableSymlink(t *testing.T) {
	scratch := t.TempDir()

	realDir := filepath.Join(scratch, "real", "bin")
	linkDir := filepath.Join(scratch, "link", "bin")

	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}

	realBinary := filepath.Join(realDir, "telecli")
	if err := os.WriteFile(realBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	linkBinary := filepath.Join(linkDir, "telecli")
	if err := os.Symlink(realBinary, linkBinary); err != nil {
		t.Fatal(err)
	}

	resolved, err := filepath.EvalSymlinks(linkBinary)
	if err != nil {
		t.Fatal(err)
	}

	resolvedAbsolute, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}

	// The real binary is resolved the same way the loader resolves it,
	// otherwise a symlinked temp directory (/var -> /private/var) would
	// look like a different package.
	resolvedReal, err := filepath.EvalSymlinks(realBinary)
	if err != nil {
		t.Fatal(err)
	}

	resolvedRealAbsolute, err := filepath.Abs(resolvedReal)
	if err != nil {
		t.Fatal(err)
	}

	viaSymlink := packagedTDLibPath(resolvedAbsolute)
	viaReal := packagedTDLibPath(resolvedRealAbsolute)

	if viaSymlink != viaReal {
		t.Fatalf(
			"a symlinked executable resolved to a different package: %q vs %q",
			viaSymlink,
			viaReal,
		)
	}

	want := packagedTDLibPath(resolvedRealAbsolute)
	if viaSymlink != want {
		t.Fatalf("packaged path = %q, want %q", viaSymlink, want)
	}
}

func TestPackagedCandidateDoesNotEscapePackageRoot(t *testing.T) {
	// The packaged candidate is always <root>/lib/<name>, so it can
	// never point outside the directory that contains bin/ and lib/.
	got := packagedTDLibPath("/root/bin/telecli")
	want := filepath.Join("/root", "lib", defaultLibraryName())

	if got != want {
		t.Fatalf("packaged path = %q, want %q", got, want)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("the packaged path escapes its package: %q", got)
	}
}

// ---- fail-closed behaviour ---------------------------------------------

func TestLoadNativeRejectsExplicitMissingPath(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	missing := filepath.Join(t.TempDir(), "absent.dylib")

	if _, err := LoadNative(missing); err == nil {
		t.Fatal("an explicit missing path must fail")
	} else if !strings.Contains(err.Error(), ErrNativeUnavailable.Error()) {
		t.Fatalf("error = %v, want it to wrap ErrNativeUnavailable", err)
	}
}

func TestLoadNativeRejectsExplicitMissingEnvironmentPath(t *testing.T) {
	t.Setenv(tdlibLibraryEnvironment, "/nonexistent/libtdjson.dylib")

	if _, err := LoadNative(""); err == nil {
		t.Fatal("an explicit missing environment path must fail")
	}
}

func TestLoadNativeDoesNotFallbackAfterEnvironmentPathFailure(t *testing.T) {
	// A packaged and development candidate exist, but an explicit
	// environment path must not silently fall through to them.
	scratch := t.TempDir()
	packageLib := filepath.Join(scratch, "pkg", "lib")
	if err := os.MkdirAll(packageLib, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(
		tdlibLibraryEnvironment,
		filepath.Join(scratch, "absent.dylib"),
	)

	candidates := libraryCandidates("", filepath.Join(scratch, "pkg", "bin", "telecli"))
	if len(candidates) != 1 {
		t.Fatalf(
			"a failing explicit candidate must be the only candidate, got %v",
			candidatePaths(candidates),
		)
	}

	_, err := LoadNative("")
	if err == nil {
		t.Fatal("startup must fail rather than use another library")
	}
	if !strings.Contains(err.Error(), "absent.dylib") {
		t.Fatalf("the error should name the rejected path: %v", err)
	}
}

func TestLoadNativeDoesNotFallbackAfterConfiguredPathFailure(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	missing := filepath.Join(t.TempDir(), "absent.dylib")

	candidates := libraryCandidates(missing, "/pkg/bin/telecli")
	if len(candidates) != 1 || !candidates[0].Explicit {
		t.Fatalf(
			"a failing configured path must be the only explicit candidate, got %v",
			candidatePaths(candidates),
		)
	}

	if _, err := LoadNative(missing); err == nil {
		t.Fatal("startup must fail rather than use another library")
	}
}

// ---- error hygiene ------------------------------------------------------

// TestLoaderErrorNamesTheRequestedPath pins the fail-closed contract:
// the error must name the library that was asked for, so the operator can
// see which path failed.
func TestLoaderErrorNamesTheRequestedPath(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	requested := filepath.Join(t.TempDir(), "absent", "libtdjson.dylib")

	_, err := LoadNative(requested)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), requested) {
		t.Fatalf("the error should name the requested path: %v", err)
	}
}

func TestLoaderErrorDoesNotContainCredentialValues(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	// The loader never receives a credential, so a value that only
	// exists in the environment must not appear in its error.
	const (
		secretHash  = "loader-canary-hash-0123456789"
		secretPhone = "+15550009999"
	)

	t.Setenv(
		tdlibLibraryEnvironment,
		filepath.Join(t.TempDir(), "absent.dylib"),
	)
	t.Setenv("TELECLI_TDLIB_API_HASH", secretHash)
	t.Setenv("TELECLI_TDLIB_PHONE", secretPhone)

	_, err := LoadNative("")
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, secret := range []string{secretHash, secretPhone} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("the loader error leaked a credential value")
		}
	}
}

// ---- packaged discovery in a real layout --------------------------------

func TestPackagedCandidateIsFoundBesideTheExecutable(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	pkg := t.TempDir()
	libDir := filepath.Join(pkg, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}

	executable := filepath.Join(pkg, "bin", "telecli")
	candidates := libraryCandidates("", executable)

	packagedAt := indexOfSource(candidates, NativeLibrarySourcePackaged)
	if packagedAt < 0 {
		t.Fatalf("no packaged candidate in %v", candidatePaths(candidates))
	}

	want := filepath.Join(libDir, defaultLibraryName())
	if candidates[packagedAt].Path != want {
		t.Fatalf(
			"packaged candidate = %q, want %q",
			candidates[packagedAt].Path,
			want,
		)
	}
	if candidates[packagedAt].Explicit {
		t.Fatal("a packaged candidate is discovered, not requested")
	}
}

func TestPackagedCandidateSurvivesDirectoryMove(t *testing.T) {
	clearTDLibLibraryEnvironment(t)

	first := t.TempDir()
	second := t.TempDir()

	firstPath := packagedTDLibPath(filepath.Join(first, "bin", "telecli"))
	secondPath := packagedTDLibPath(filepath.Join(second, "bin", "telecli"))

	if firstPath == secondPath {
		t.Fatal("the packaged path must follow the executable")
	}
	for _, path := range []string{firstPath, secondPath} {
		if !filepath.IsAbs(path) {
			t.Fatalf("packaged path %q is not absolute", path)
		}
	}
}

package archdeps

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// repositoryRoot walks up from the package directory to go.mod.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// packageImports returns every import of every Go file, including test
// files and files for other platforms, keyed by package directory.
func packageImports(t *testing.T, root string) map[string]map[string][]string {
	t.Helper()
	result := make(map[string]map[string][]string)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") ||
				name == "testdata" || name == "vendor" ||
				name == "dist" || name == "third_party") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		if result[pkg] == nil {
			result[pkg] = make(map[string][]string)
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			result[pkg][imported] = append(
				result[pkg][imported],
				filepath.Base(path),
			)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func isStandardLibrary(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".") && first != modulePath
}

// TestArchImports enforces the import policy in policy.go.
func TestArchImports(t *testing.T) {
	imports := packageImports(t, repositoryRoot(t))

	packages := make([]string, 0, len(imports))
	for pkg := range imports {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)

	for _, pkg := range packages {
		allowed, known := allowedInternalImports[pkg]
		if !known {
			t.Errorf("package %s is not in the import policy; add it to allowedInternalImports", pkg)
			continue
		}
		for imported, files := range imports[pkg] {
			if standardLibraryOnly[pkg] && !isStandardLibrary(imported) {
				t.Errorf("%s may import the standard library only, but %v import %s",
					pkg, files, imported)
			}
			internal, ok := strings.CutPrefix(imported, modulePath+"/")
			if !ok || internal == pkg {
				continue
			}
			if !slices.Contains(allowed, internal) {
				t.Errorf("%s may not import %s (imported by %v)", pkg, imported, files)
			}
		}
	}
}

// TestArchPolicyHasNoStalePackages keeps the policy honest: an entry for
// a package that no longer exists would hide a later reuse of its name.
func TestArchPolicyHasNoStalePackages(t *testing.T) {
	imports := packageImports(t, repositoryRoot(t))
	for pkg := range allowedInternalImports {
		if _, exists := imports[pkg]; !exists {
			t.Errorf("policy lists %s, which has no Go files", pkg)
		}
	}
}

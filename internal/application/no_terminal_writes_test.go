package application

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing under internal/ may write to the terminal on its own.
//
// While `telecli tui` runs, the terminal belongs to the renderer. A log
// line written over a running interface shifts every row below it by one,
// and the component that wrote it is never the component that looks
// broken: the reconciler that logs one line per confirmed message is
// working perfectly, and the screen is what breaks.
//
// The two doors to the terminal that a component can walk through without
// being handed anything are slog.Default() and the standard library's log
// package. Neither is needed by any code this repository owns, and both
// are exactly the thing that has to be impossible rather than merely
// avoided, so this test makes it impossible and fails on a new use.

// compositionRootFiles are the files allowed to install the process-wide
// log destinations.
//
// This is a list of files and not of packages, which matters: the
// composition root shares its package with components that must not reach
// for a default at all. Allowing the package would have allowed the
// reconciler — the very component whose one line per confirmed message
// broke the owner's screen — and the test would have passed over it.
//
// These three are where the program decides what a reason is and where it
// goes. Everything else is a component, and a component is handed a
// logger.
var compositionRootFiles = map[string]bool{
	"cmd/telecli/main.go":             true,
	"internal/application/app.go":     true,
	"internal/application/tui_log.go": true,
}

// testFilesMayLog is whether a _test.go file is allowed to use the
// defaults.
//
// It is: a test that does not care where a logger writes can take the
// default, and several of the fakes here do. The rule is about the
// shipped program.
const testFilesMayLog = true

// terminalReachers are the calls that can put a line on a terminal this
// program did not ask for.
var terminalReachers = map[string]string{
	// The process-wide slog destination, which is stderr unless the
	// composition root changed it.
	"slog.Default": "the process-wide slog destination is the terminal",
	// The standard library's logger, which is stderr unless something
	// called log.SetOutput.
	"log.Print":   "the standard log package writes to the terminal",
	"log.Printf":  "the standard log package writes to the terminal",
	"log.Println": "the standard log package writes to the terminal",
	"log.Fatal":   "the standard log package writes to the terminal",
	"log.Fatalf":  "the standard log package writes to the terminal",
	"log.Fatalln": "the standard log package writes to the terminal",
	"log.Panic":   "the standard log package writes to the terminal",
	"log.Panicf":  "the standard log package writes to the terminal",
	"log.Panicln": "the standard log package writes to the terminal",
}

func TestNoComponentUnderInternalWritesToTheTerminalOnItsOwn(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)

	var offences []string
	err := filepath.WalkDir(
		root,
		func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			if compositionRootFiles[filepath.ToSlash(rel)] {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") && testFilesMayLog {
				return nil
			}

			found, scanErr := terminalWritersIn(path)
			if scanErr != nil {
				return scanErr
			}
			offences = append(offences, found...)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	if len(offences) > 0 {
		t.Fatalf(
			"these reach the terminal the interface owns:\n\n%s\n\n"+
				"A component is handed a logger instead. A nil logger "+
				"discards, which is quiet; the default is the terminal, "+
				"which breaks a running screen.",
			strings.Join(offences, "\n\n"),
		)
	}
}

// terminalWritersIn returns a description of every terminal-reaching call
// in one file.
func terminalWritersIn(path string) ([]string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		return nil, err
	}

	// Which names this file imported as the two log packages, so that a
	// file that aliases the import is caught too.
	slogName, stdLogName := "", ""
	for _, spec := range parsed.Imports {
		imported := strings.Trim(spec.Path.Value, `"`)
		local := ""
		if spec.Name != nil {
			local = spec.Name.Name
		} else {
			local = imported
			if index := strings.LastIndex(imported, "/"); index >= 0 {
				local = imported[index+1:]
			}
		}

		switch imported {
		case "log/slog":
			slogName = local
		case "log":
			stdLogName = local
		}
	}

	var found []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}

		name := pkg.Name + "." + selector.Sel.Name
		reason, reachable := terminalReachers[name]
		if !reachable {
			return true
		}
		if (name == "slog.Default" && pkg.Name != slogName) ||
			(strings.HasPrefix(name, "log.") && pkg.Name != stdLogName) {
			// A different package that happens to have the same shape.
			return true
		}

		found = append(found, describe(
			fileSet, path, call, name, reason,
		))
		return true
	})

	return found, nil
}

func describe(
	fileSet *token.FileSet,
	path string,
	call *ast.CallExpr,
	name, reason string,
) string {
	position := fileSet.Position(call.Pos())

	return position.String() + " calls " + name + " — " + reason
}

// repositoryRoot is the directory holding go.mod, which is where the
// packages this test is about start.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// The composition root is where the decision is made, and this test says
// what it decided: the two process-wide destinations are redirected, not
// left alone.
func TestTheCompositionRootInstallsBothProcessWideDestinations(t *testing.T) {
	t.Parallel()

	// The composition root decides, and it decides by installing.
	source, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	if !strings.Contains(string(source), "installUILog(") {
		t.Fatal(
			"the composition root never installs the log destinations: " +
				"anything that reaches for a default still writes to " +
				"the terminal",
		)
	}

	// And installing means redirecting both doors, not one of them.
	installer, err := os.ReadFile("tui_log.go")
	if err != nil {
		t.Fatalf("read tui_log.go: %v", err)
	}
	for _, want := range []string{
		"slog.SetDefault(", "stdLog.SetOutput(",
	} {
		if !strings.Contains(string(installer), want) {
			t.Fatalf(
				"the installer does not contain %q: one of the two "+
					"process-wide destinations is still the terminal",
				want,
			)
		}
	}
}

// The interface is told where its reasons go, rather than being left with
// os.Stderr by default.
func TestTheInterfaceIsNotLeftWritingToStandardError(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}

	// diagnosticsWriter is what the TUI's Diagnostics field is set from.
	//
	// The check is per line rather than one string over the file, because a
	// block of fields is aligned by gofmt and the spaces in the middle of it
	// are the gofmt's business: a test that reads the alignment fails on
	// the next field added to the block and proves nothing about the wire.
	text := string(source)
	diagnostics := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Diagnostics:") {
			diagnostics = line

			break
		}
	}
	if diagnostics == "" || !strings.Contains(diagnostics, "a.diagnosticsWriter()") {
		t.Fatalf(
			"the interface's diagnostics writer is not wired: %q",
			diagnostics,
		)
	}
	if !strings.Contains(text, "WithLog(") {
		t.Fatal("the app is never given a log destination")
	}

	// The default is still os.Stderr for the commands that want it; what
	// must not happen is the interface path using it.
	if strings.Contains(text, "Diagnostics:  os.Stderr") {
		t.Fatal("the interface is wired to standard error directly")
	}
}

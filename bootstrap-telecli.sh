#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  telecli bootstrap
#
#  Modes:
#    init [ROOT] [MODULE]
#        create go.mod and PROJECT_FACTS.md for a new repository.
#        does NOT create any PR-01 files.
#
#    apply-pr02 [ROOT]
#        apply the PR-02 CLI/TUI baseline.
#        requires completed PR-01 (recorder package present).
#
#  Defaults:
#    ROOT   = "."
#    MODULE = "telecli"  (init mode only)
# ============================================================

# ---- self-check: no HTML entities in this file ----
if grep -nE '&(gt|lt|amp);' "$0" >/dev/null 2>&1; then
  printf 'bootstrap script contains HTML entities; fix the file\n' >&2
  exit 1
fi

usage() {
  cat >&2 <<'USAGE'
Usage:
  bootstrap-telecli.sh init [ROOT] [MODULE]
  bootstrap-telecli.sh apply-pr02 [ROOT]

  init        create go.mod and PROJECT_FACTS.md for a new repository.
              does NOT create any PR-01 files.
  apply-pr02  apply the PR-02 CLI/TUI baseline.
              requires completed PR-01 (recorder package present).

  ROOT    defaults to current directory.
  MODULE  defaults to "telecli" (init mode only).
USAGE
}

require_go() {
  command -v go >/dev/null 2>&1 || {
    printf 'Go toolchain is required\n' >&2
    exit 1
  }
}

detect_go_version() {
  go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/'
}

# ============================================================
#  init
# ============================================================

init_repository() {
  local root="${1:-.}"
  local module="${2:-telecli}"

  mkdir -p "$root"
  cd "$root"

  printf '== init in: %s\n' "$(pwd)"
  require_go

  local go_version
  go_version="$(detect_go_version)"
  if [[ -z "$go_version" ]]; then
    printf 'cannot detect Go version\n' >&2
    exit 1
  fi

  if [[ -f go.mod ]]; then
    printf 'go.mod already exists; refusing to overwrite\n' >&2
    exit 1
  fi
  if [[ -f PROJECT_FACTS.md ]]; then
    printf 'PROJECT_FACTS.md already exists; refusing to overwrite\n' >&2
    exit 1
  fi

  cat > go.mod <<EOF
module $module

go $go_version
EOF

  cat > PROJECT_FACTS.md <<EOF
# telecli project facts

## Repository
- Repository: local
- Default branch: main
- Go module: $module
- Minimum Go version: $go_version
- License: TBD

## Supported platforms
- Linux: yes
- macOS: yes
- Windows: TBD
- CPU architectures: amd64, arm64
- CGO requirements: none for PR-02

## CLI and TUI
- CLI framework: standard library flag
- TUI framework: github.com/charmbracelet/bubbletea
- Styling library: none yet
- Configuration library: github.com/BurntSushi/toml
- Configuration format: TOML
- Logging library: TBD

## TDLib
- Binding: TBD
- Pinned repository: TBD
- Pinned commit: TBD
- Build method: TBD
- Runtime library discovery: TBD
- Compatibility manifest: TBD
- Verified behaviors: TBD
- Unsupported behaviors: TBD

## Storage
- Embedded database: TBD
- Payload encryption: TBD
- Schema migration: TBD
- Data directory: ~/.local/share/telecli
- Config directory: TBD
- Cache directory: TBD

## Secrets
- Keyring backend: TBD
- Headless fallback: TBD
- Secret logging policy: never log secrets

## Commands
- telecli --help
- telecli version
- telecli doctor
- telecli tui

## Architecture
- Architecture policy: internal/archdeps/policy.go (if present)
- Recorder contract: internal/telemetry/recorder
- Recorder dependency rule: standard library only

## Current roadmap status
- PR-01: required before PR-02
- PR-02: current
- PR-03: pending
- First usable TUI checkpoint: PR-02
- First TDLib login checkpoint: PR-05
- First message-send checkpoint: PR-07

## Open ADRs
- None
EOF

  printf 'init complete: go.mod (module=%s, go=%s), PROJECT_FACTS.md\n' \
    "$module" "$go_version"
  printf '\nNext: apply PR-01 (recorder + archdeps), then run apply-pr02.\n'
}

# ============================================================
#  apply-pr02
# ============================================================

apply_pr02() {
  local root="${1:-.}"
  cd "$root"

  printf '== apply-pr02 in: %s\n' "$(pwd)"
  require_go

  # ---------- Phase 1: read-only preflight ----------

  # go.mod
  [[ -f go.mod ]] || { printf 'go.mod missing\n' >&2; exit 1; }
  local module_path
  module_path="$(awk '$1=="module"{print $2; exit}' go.mod)"
  if [[ -z "$module_path" ]]; then
    printf 'cannot determine module path from go.mod:\n' >&2
    cat go.mod >&2
    exit 1
  fi

  # PROJECT_FACTS.md
  [[ -f PROJECT_FACTS.md ]] || { printf 'PROJECT_FACTS.md missing\n' >&2; exit 1; }

  grep -Fq -- "- Go module: $module_path" PROJECT_FACTS.md || {
    printf 'module path conflict:\n  go.mod=%s\n  PROJECT_FACTS.md=%s\n' \
      "$module_path" \
      "$(grep -F -- '- Go module:' PROJECT_FACTS.md || echo '(missing)')" >&2
    exit 1
  }

  grep -Fq -- "- TUI framework: github.com/charmbracelet/bubbletea" PROJECT_FACTS.md || {
    printf 'PROJECT_FACTS.md does not select github.com/charmbracelet/bubbletea\n' >&2
    exit 1
  }

  grep -Fq -- "- Configuration library: github.com/BurntSushi/toml" PROJECT_FACTS.md || {
    printf 'PROJECT_FACTS.md does not select github.com/BurntSushi/toml\n' >&2
    exit 1
  }

  grep -Fq -- "- Configuration format: TOML" PROJECT_FACTS.md || {
    printf 'PROJECT_FACTS.md does not select TOML format\n' >&2
    exit 1
  }

  # PR-01 recorder prerequisite
  local recorder_dir="internal/telemetry/recorder"
  local required_recorder=(
    component.go
    capabilities.go
    counters.go
    errors.go
    operations.go
    noop.go
  )
  local missing=0
  local f
  for f in "${required_recorder[@]}"; do
    if [[ ! -f "$recorder_dir/$f" ]]; then
      printf 'PR-01 incomplete: missing %s/%s\n' "$recorder_dir" "$f" >&2
      missing=1
    fi
  done
  if [[ "$missing" -ne 0 ]]; then
    printf 'apply PR-01 before apply-pr02; PR-02 must not synthesize it.\n' >&2
    exit 1
  fi

  # forbidden TUI deps
  if grep -Eq 'github.com/(gdamore/tcell|rivo/tview)' go.mod 2>/dev/null; then
    printf 'existing TUI dependency conflicts with Bubble Tea\n' >&2
    exit 1
  fi

  # strict: refuse to overwrite PR-02 target files
  local target_files=(
    cmd/telecli/main.go
    internal/buildinfo/buildinfo.go
    internal/buildinfo/buildinfo_test.go
    internal/config/config.go
    internal/config/config_test.go
    internal/application/app.go
    internal/application/app_test.go
    internal/tui/model.go
    internal/tui/model_test.go
    internal/tui/program.go
  )
  local path
  for path in "${target_files[@]}"; do
    if [[ -e "$path" ]]; then
      printf 'refusing to overwrite existing PR-02 file: %s\n' "$path" >&2
      exit 1
    fi
  done

  # ---------- Phase 2: write ----------

  mkdir -p \
    cmd/telecli \
    internal/buildinfo \
    internal/config \
    internal/application \
    internal/tui

  cat > cmd/telecli/main.go <<EOF
package main

import (
	"os"

	"$module_path/internal/application"
	"$module_path/internal/tui"
)

func main() {
	os.Exit(application.Main(os.Args, application.Environment{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		RunTUI: tui.Run,
	}))
}
EOF

  cat > internal/buildinfo/buildinfo.go <<'EOF'
package buildinfo

import "fmt"

// Values can be overridden through go build -ldflags -X.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

type Info struct {
	Version string
	Commit  string
	Date    string
}

func Get() Info {
	return Info{Version: Version, Commit: Commit, Date: Date}
}

func (i Info) String() string {
	return fmt.Sprintf("telecli %s (commit %s, built %s)",
		i.Version, i.Commit, i.Date)
}
EOF

  cat > internal/buildinfo/buildinfo_test.go <<'EOF'
package buildinfo

import "testing"

func TestGetReturnsLinkTimeValues(t *testing.T) {
	got := Get()
	if got.Version == "" || got.Commit == "" || got.Date == "" {
		t.Fatalf("expected non-empty link-time values, got %+v", got)
	}
}

func TestStringStable(t *testing.T) {
	prevVersion, prevCommit, prevDate := Version, Commit, Date
	t.Cleanup(func() {
		Version = prevVersion
		Commit = prevCommit
		Date = prevDate
	})

	Version = "1.2.3"
	Commit = "abc123"
	Date = "2026-09-24"

	want := "telecli 1.2.3 (commit abc123, built 2026-09-24)"
	if got := Get().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
EOF

  cat > internal/config/config.go <<'EOF'
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	LogLevel string `toml:"log_level"`
	DataDir  string `toml:"data_dir"`
}

func Default() Config {
	return Config{
		LogLevel: "info",
		DataDir:  defaultDataDir(),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown config keys: %v", undecoded)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.LogLevel == "" {
		return fmt.Errorf("log_level must not be empty")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir must not be empty")
	}
	return nil
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecli"
	}
	return filepath.Join(home, ".local", "share", "telecli")
}
EOF

  cat > internal/config/config_test.go <<'EOF'
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestExplicitTOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("log_level = \"debug\"\ndata_dir = \"/tmp/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" || cfg.DataDir != "/tmp/x" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("log_levle = \"debug\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestEmptyLogLevelRejected(t *testing.T) {
	cfg := Config{LogLevel: "", DataDir: "/tmp"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyDataDirRejected(t *testing.T) {
	cfg := Config{LogLevel: "info", DataDir: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}
EOF

  cat > internal/tui/model.go <<'EOF'
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	chatList []string
	history  []string
	composer []rune
	width    int
	height   int
	quitting bool
}

func NewModel() Model {
	return Model{
		chatList: []string{"(no chats yet)", "Placeholder chat 1", "Placeholder chat 2"},
		history:  []string{"(no messages yet)", "History placeholder"},
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit

		case tea.KeyBackspace:
			if len(m.composer) > 0 {
				m.composer = m.composer[:len(m.composer)-1]
			}

		case tea.KeyRunes:
			m.composer = append(m.composer, msg.Runes...)

		case tea.KeySpace:
			m.composer = append(m.composer, ' ')

		case tea.KeyEnter:
			// PR-02: no-op; sending arrives in PR-07.
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString("telecli TUI shell\n")
	b.WriteString("----------------\n")

	b.WriteString("Chats:\n")
	for _, c := range m.chatList {
		b.WriteString("  " + c + "\n")
	}

	b.WriteString("\nHistory:\n")
	for _, h := range m.history {
		b.WriteString("  " + h + "\n")
	}

	b.WriteString("\nComposer: " + string(m.composer) + "\n")
	b.WriteString("\nEsc/Ctrl+C: quit\n")
	return b.String()
}

func (m Model) Composer() string { return string(m.composer) }
EOF

  cat > internal/tui/model_test.go <<'EOF'
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func assertQuitCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", msg)
	}
}

func TestInitialViewContainsSections(t *testing.T) {
	v := NewModel().View()
	for _, want := range []string{"telecli TUI shell", "Chats:", "History:", "Composer:"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestWindowSizeMsgUpdatesDimensions(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := updated.(Model)
	if mm.width != 120 || mm.height != 40 {
		t.Fatalf("dims not updated: %+v", mm)
	}
}

func TestRunesAppendedToComposer(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("привет")})
	mm := updated.(Model)
	if mm.Composer() != "привет" {
		t.Fatalf("composer=%q", mm.Composer())
	}
}

func TestBackspaceRemovesOneRune(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("аб")})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := updated.(Model).Composer(); got != "а" {
		t.Fatalf("composer=%q", got)
	}
}

func TestEscQuits(t *testing.T) {
	_, cmd := NewModel().Update(tea.KeyMsg{Type: tea.KeyEsc})
	assertQuitCommand(t, cmd)
}

func TestCtrlCQuits(t *testing.T) {
	_, cmd := NewModel().Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assertQuitCommand(t, cmd)
}

func TestViewAfterQuitIsEmpty(t *testing.T) {
	updated, _ := NewModel().Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v := updated.(Model).View(); v != "" {
		t.Fatalf("expected empty view, got %q", v)
	}
}
EOF

  cat > internal/tui/program.go <<'EOF'
package tui

import tea "github.com/charmbracelet/bubbletea"

// Run starts the TUI in alt-screen mode and returns when the user exits.
func Run() error {
	p := tea.NewProgram(NewModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
EOF

  cat > internal/application/app.go <<EOF
package application

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"$module_path/internal/buildinfo"
	"$module_path/internal/config"
	"$module_path/internal/telemetry/recorder"
)

// Environment is the injection point for tests.
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	RunTUI func() error
}

var errMissingTUIRunner = errors.New("TUI runner is not configured")

// App holds the composition root state.
//
// cfg and recorder are retained for the application lifecycle introduced
// by later vertical slices (PR-04+); they are wired here so the DI graph
// exists from PR-02 onward.
type App struct {
	cfg      config.Config
	recorder recorder.ComponentRecorder
	runTUI   func() error
}

func New(cfg config.Config, rec recorder.ComponentRecorder, runTUI func() error) *App {
	return &App{cfg: cfg, recorder: rec, runTUI: runTUI}
}

func (a *App) RunTUI() error {
	if a.runTUI == nil {
		return errMissingTUIRunner
	}
	return a.runTUI()
}

// Main is the CLI entry point.
//
// Exit codes:
//
//	0  success
//	1  runtime error
//	2  usage error
func Main(args []string, env Environment) int {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if env.RunTUI == nil {
		env.RunTUI = func() error { return errMissingTUIRunner }
	}

	if len(args) < 2 {
		printHelp(env.Stdout)
		return 0
	}

	switch args[1] {
	case "--help", "-h", "help":
		printHelp(env.Stdout)
		return 0
	case "version":
		fmt.Fprintln(env.Stdout, buildinfo.Get().String())
		return 0
	case "doctor":
		return runDoctor(args[2:], env)
	case "tui":
		return runTUI(args[2:], env)
	default:
		fmt.Fprintf(env.Stderr, "unknown command: %s\n\n", args[1])
		printHelp(env.Stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, \`telecli - terminal Telegram client

Usage:
  telecli --help
  telecli version
  telecli doctor [--config path]
  telecli tui    [--config path]
\`)
}

func runDoctor(args []string, env Environment) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	cfgPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	fmt.Fprintln(env.Stdout, "telecli doctor")
	fmt.Fprintln(env.Stdout, buildinfo.Get().String())
	fmt.Fprintf(env.Stdout, "Go: %s %s/%s\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(env.Stdout, "Config: OK (data_dir=%s, log_level=%s)\n",
		cfg.DataDir, cfg.LogLevel)
	fmt.Fprintln(env.Stdout, "TDLib: not linked in this build")
	return 0
}

func runTUI(args []string, env Environment) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	cfgPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	app := New(cfg, recorder.NewNoop(), env.RunTUI)
	if err := app.RunTUI(); err != nil {
		fmt.Fprintf(env.Stderr, "tui error: %v\n", err)
		return 1
	}
	return 0
}
EOF

  cat > internal/application/app_test.go <<'EOF'
package application

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newEnv(stdout, stderr *bytes.Buffer, tuiCalls *int) Environment {
	return Environment{
		Stdout: stdout,
		Stderr: stderr,
		RunTUI: func() error { *tuiCalls++; return nil },
	}
}

func TestNoArgsPrintsHelpExit0(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli"}, newEnv(&out, &errb, &calls))
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help not printed: %q", out.String())
	}
}

func TestHelpExit0(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "--help"}, newEnv(&out, &errb, &calls)); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestVersionExit0(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "version"}, newEnv(&out, &errb, &calls)); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "telecli ") {
		t.Fatalf("no version line: %q", out.String())
	}
}

func TestUnknownCommandExit2(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "nope"}, newEnv(&out, &errb, &calls)); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestDoctorValidDefaultsExit0(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "doctor"}, newEnv(&out, &errb, &calls)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "TDLib: not linked") {
		t.Fatalf("stdout=%q", out.String())
	}
}

func TestDoctorInvalidConfigExit1(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte("log_levle=\"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "doctor", "--config", p}, newEnv(&out, &errb, &calls))
	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func TestTUIInvalidConfigDoesNotRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte("log_level=\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "tui", "--config", p}, newEnv(&out, &errb, &calls))
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if calls != 0 {
		t.Fatalf("RunTUI was called %d times", calls)
	}
}

func TestTUIValidConfigRunsOnce(t *testing.T) {
	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "tui"}, newEnv(&out, &errb, &calls))
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if calls != 1 {
		t.Fatalf("RunTUI calls=%d", calls)
	}
}

func TestTUIMissingRunnerExit1(t *testing.T) {
	var out, errb bytes.Buffer
	code := Main(
		[]string{"telecli", "tui"},
		Environment{
			Stdout: &out,
			Stderr: &errb,
		},
	)
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not configured") {
		t.Fatalf("stderr=%q", errb.String())
	}
}
EOF

  printf '== go mod tidy\n'
  go mod tidy

  printf '\napply-pr02 complete.\n'
  printf 'Next:\n'
  printf '  gofmt -w .\n'
  printf '  go build ./...\n'
  printf '  go test ./...\n'
  printf '  go test -race ./...\n'
  printf '  go vet ./...\n'
  printf '  go run ./cmd/telecli --help\n'
  printf '  go run ./cmd/telecli version\n'
  printf '  go run ./cmd/telecli doctor\n'
  printf '  go run ./cmd/telecli tui\n'
}

# ============================================================
#  dispatch
# ============================================================

case "${1:-}" in
  init)
    init_repository "${2:-.}" "${3:-telecli}"
    ;;
  apply-pr02)
    apply_pr02 "${2:-.}"
    ;;
  ""|"-h"|"--help"|"help")
    usage
    exit 0
    ;;
  *)
    printf 'unknown mode: %s\n\n' "$1" >&2
    usage
    exit 2
    ;;
esac

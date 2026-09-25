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

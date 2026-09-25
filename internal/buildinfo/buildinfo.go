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

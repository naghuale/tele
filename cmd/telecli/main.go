package main

import (
	"os"

	"telecli/internal/application"
	"telecli/internal/tui"
)

func main() {
	os.Exit(application.Main(os.Args, application.Environment{
		Stdout:              os.Stdout,
		Stderr:              os.Stderr,
		RunTUI:              tui.RunWithSource,
		RunTUIWithSubmitter: tui.RunWithDependencies,
	}))
}

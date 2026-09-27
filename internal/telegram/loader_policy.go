package telegram

import "path/filepath"

// The search policy lives outside any cgo build tag so the runtime loader
// and the configuration wizard answer the same question the same way.

// developmentLibrarySearch enables the repository checkout candidate.
//
// The checkout path is relative, so the dynamic loader resolves it from
// the working directory. A release binary started from an untrusted
// directory would then load whatever library that directory contains.
// The candidate is therefore compiled in only with the telecli_dev build
// tag; tests may flip the variable.
var developmentLibrarySearch = developmentBuild

// DevelopmentLibrarySearchEnabled reports whether this build looks for a
// TDLib library in a repository checkout.
func DevelopmentLibrarySearchEnabled() bool {
	return developmentLibrarySearch
}

// DevelopmentLibraryPath is the repository checkout location, relative to
// the source tree root.
func DevelopmentLibraryPath() string {
	return filepath.Join("third_party", "tdlib", "lib", defaultLibraryName())
}

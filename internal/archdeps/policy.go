// Package archdeps holds the repository import policy.
//
// TestArchImports enforces it over every Go file, whatever its build
// tags, so a platform-specific file cannot slip past the rule on the
// platform that happens to run the tests.
package archdeps

// modulePath is the module prefix of internal imports.
const modulePath = "telecli"

// standardLibraryOnly lists packages that may import the standard
// library and nothing else, not even third-party modules.
var standardLibraryOnly = map[string]bool{
	// The recorder contract is shared by every component, so it must
	// not pull any dependency into them.
	"internal/telemetry/recorder": true,
}

// allowedInternalImports lists, for every package, the telecli packages
// it may import. A package missing from this map fails the test, so a
// new package has to state its place in the layering.
//
// Layering, bottom to top:
//
//	recorder, tdjson, buildinfo, config, authstore, secretinput,
//	outbox, tui/theme              leaf packages
//	tui                             interface components
//	telegram                          TDLib binding and session
//	application                       composition root
//	cmd/telecli                       entry point
var allowedInternalImports = map[string][]string{
	"cmd/telecli": {
		"internal/application",
		"internal/tui",
	},
	"internal/application": {
		"internal/authstore",
		"internal/buildinfo",
		"internal/config",
		"internal/outbox",
		"internal/secretinput",
		"internal/telegram",
		"internal/telemetry/recorder",
		"internal/tui",
		"internal/tui/theme",
	},
	"internal/archdeps":           {},
	"internal/authstore":          {},
	"internal/buildinfo":          {},
	"internal/config":             {},
	"internal/outbox":             {},
	"internal/secretinput":        {},
	"internal/telegram":           {"internal/telegram/tdjson", "internal/telemetry/recorder"},
	"internal/telegram/tdjson":    {},
	"internal/telemetry/recorder": {},
	"internal/tui":                {"internal/tui/theme"},
	"internal/tui/theme":          {},
}

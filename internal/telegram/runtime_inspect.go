package telegram

import "context"

// Inspect returns version, commit, and compatibility information for
// the loaded TDLib runtime.
//
// Inspect uses the modern JSON API's synchronous getOption support,
// which is allowed before authorization.
func (r *Runtime) Inspect(
	ctx context.Context,
	expected CompatibilityManifest,
) (RuntimeInfo, error) {
	if r.State() == LifecycleClosed {
		return RuntimeInfo{}, ErrClosed
	}

	return InspectRuntime(
		ctx,
		r.native,
		expected,
	)
}

// RuntimeCompatibility returns the compile-time compatibility manifest.
//
// An empty commit intentionally results in CompatibilityUnverified
// rather than falsely claiming verification.
func RuntimeCompatibility() CompatibilityManifest {
	return CompatibilityManifest{
		Version: expectedTDLibVersion,
		Commit:  expectedTDLibCommit,
	}
}

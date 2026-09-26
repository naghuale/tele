// Package authstore stores Telegram credential profiles in the platform
// secret store.
//
// The package owns every platform detail: the wire envelope, the item
// identity and the Security.framework bridge. Callers exchange only the
// Profile value, so a plaintext fallback or a second backend can never be
// introduced by accident.
package authstore

import (
	"context"
	"errors"
	"regexp"
)

// Profile is the secret part of a Telegram credential profile.
//
// The API ID is deliberately absent: it is not a secret and lives in the
// configuration file.
type Profile struct {
	APIHash string
	Phone   string
}

// Store persists credential profiles.
//
// Create and Replace are separate on purpose. A single Save with an
// overwrite flag hides the security policy in a boolean; splitting them
// makes "never silently replace" and "never create over an existing
// profile" explicit at every call site.
type Store interface {
	// Load returns the profile or ErrProfileUnavailable.
	Load(ctx context.Context, profile string) (Profile, error)
	// Create stores a new profile and returns ErrProfileExists when the
	// profile is already present. It never overwrites.
	Create(ctx context.Context, profile string, credentials Profile) error
	// Replace overwrites an existing profile and returns
	// ErrProfileUnavailable when it is missing.
	Replace(ctx context.Context, profile string, credentials Profile) error
	// Delete removes exactly one profile and returns
	// ErrProfileUnavailable when it is missing.
	Delete(ctx context.Context, profile string) error
}

var (
	// ErrProfileUnavailable reports a missing profile.
	ErrProfileUnavailable = errors.New(
		"Telegram credential profile unavailable",
	)
	// ErrProfileExists reports a create attempt against an existing
	// profile.
	ErrProfileExists = errors.New(
		"Telegram credential profile already exists",
	)
	// ErrInvalidProfile reports a rejected profile name or payload.
	ErrInvalidProfile = errors.New("Telegram credential profile invalid")
	// ErrStoreUnavailable reports that the platform has no usable
	// credential store.
	ErrStoreUnavailable = errors.New(
		"Telegram credential store unavailable",
	)
)

// credentialProfilePattern restricts profile names to a safe, unambiguous
// character set.
//
// The name becomes a Keychain account, so allowing spaces, control
// characters, path separators or unbounded length would create
// ambiguous or surprising items.
var credentialProfilePattern = regexp.MustCompile(
	`^[A-Za-z0-9._-]{1,64}$`,
)

// validateProfileName rejects unusable profile names before any platform
// call happens.
func validateProfileName(profile string) error {
	if profile == "" {
		return errors.Join(
			ErrInvalidProfile,
			errors.New("profile name is empty"),
		)
	}

	if !credentialProfilePattern.MatchString(profile) {
		return errors.Join(
			ErrInvalidProfile,
			errors.New(
				"profile name must be 1-64 characters of letters, "+
					"digits, dot, dash or underscore",
			),
		)
	}

	return nil
}

// validateProfile rejects an incomplete secret set before it can reach the
// platform store.
func validateProfile(credentials Profile) error {
	if credentials.APIHash == "" {
		return errors.Join(
			ErrInvalidProfile,
			errors.New("API hash is empty"),
		)
	}

	if credentials.Phone == "" {
		return errors.Join(
			ErrInvalidProfile,
			errors.New("phone number is empty"),
		)
	}

	return nil
}

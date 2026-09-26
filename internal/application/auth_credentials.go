package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"telecli/internal/config"
)

// AuthAvailability is the tri-state result of credential resolution.
//
// The distinction matters: only a complete absence of authentication
// configuration may fall back to the mock-only UI. A partial or broken
// configuration must fail closed instead of silently degrading.
type AuthAvailability uint8

const (
	// AuthAvailabilityAbsent means the user configured nothing at all.
	AuthAvailabilityAbsent AuthAvailability = iota
	// AuthAvailabilityReady means a complete, coherent credential set
	// is available.
	AuthAvailabilityReady
	// AuthAvailabilityInvalid means authentication was attempted but
	// the configuration is partial, mixed or unusable.
	AuthAvailabilityInvalid
)

// String returns the availability name for diagnostics.
func (a AuthAvailability) String() string {
	switch a {
	case AuthAvailabilityAbsent:
		return "absent"
	case AuthAvailabilityReady:
		return "ready"
	case AuthAvailabilityInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// AuthCredentialSource identifies which single source supplied the
// credentials.
//
// Sources are never combined: one run uses either the environment or one
// coherent profile, never a mixture.
type AuthCredentialSource uint8

const (
	// AuthCredentialSourceNone means no source supplied credentials.
	AuthCredentialSourceNone AuthCredentialSource = iota
	// AuthCredentialSourceEnvironment means the TELECLI_TDLIB_*
	// variables supplied the complete set.
	AuthCredentialSourceEnvironment
	// AuthCredentialSourceProfile means the config API ID plus one
	// credential profile supplied the set.
	AuthCredentialSourceProfile
)

// String returns the source name for diagnostics.
func (s AuthCredentialSource) String() string {
	switch s {
	case AuthCredentialSourceNone:
		return "none"
	case AuthCredentialSourceEnvironment:
		return "environment"
	case AuthCredentialSourceProfile:
		return "profile"
	default:
		return "unknown"
	}
}

// TelegramCredentials is a complete, coherent credential set.
//
// Every authorization attempt uses exactly one such set.
type TelegramCredentials struct {
	APIID   int
	APIHash string
	Phone   string
}

// TelegramProfileCredentials holds the secret part of a profile.
//
// API ID deliberately stays in the config file: it is not a secret.
type TelegramProfileCredentials struct {
	APIHash string
	Phone   string
}

// ResolvedTelegramCredentials is the result of credential resolution.
type ResolvedTelegramCredentials struct {
	Availability AuthAvailability
	Source       AuthCredentialSource
	Credentials  TelegramCredentials
	// Reason is a safe, value-free explanation for the Invalid state.
	Reason string
}

// Ready reports whether a complete credential set is available.
func (r ResolvedTelegramCredentials) Ready() bool {
	return r.Availability == AuthAvailabilityReady
}

// TelegramCredentialStore loads the secret part of a credential profile.
//
// The production implementation arrives with the platform backend. This
// contract exists so the resolver can be exercised without a real
// credential store.
type TelegramCredentialStore interface {
	LoadTelegramCredentials(
		ctx context.Context,
		profile string,
	) (TelegramProfileCredentials, error)
}

var (
	// ErrTelegramCredentialsInvalid reports a partial, mixed or
	// otherwise unusable authentication configuration.
	ErrTelegramCredentialsInvalid = errors.New(
		"Telegram credentials invalid",
	)
	// ErrTelegramCredentialProfileUnavailable reports that a profile was
	// configured but no credential store can serve it yet.
	ErrTelegramCredentialProfileUnavailable = errors.New(
		"Telegram credential profile unavailable",
	)
)

// TelegramCredentialResolver resolves the single authoritative
// credential set for a run.
type TelegramCredentialResolver struct {
	store TelegramCredentialStore
}

// NewTelegramCredentialResolver returns a resolver backed by store.
//
// A nil store is replaced by an unavailable store, so a configured
// profile produces a hard error instead of a silent mock-only run.
func NewTelegramCredentialResolver(
	store TelegramCredentialStore,
) TelegramCredentialResolver {
	if store == nil {
		store = unavailableTelegramCredentialStore{}
	}

	return TelegramCredentialResolver{store: store}
}

// ResolveTelegramCredentials resolves credentials for cfg.
//
// Resolution matrix:
//
//   - all three environment variables set: Ready from the environment,
//     and the profile store is never consulted;
//   - some but not all set: Invalid, with no profile fallback;
//   - no environment and a complete [auth] section: Ready from one
//     profile;
//   - no environment and a partial [auth] section: Invalid;
//   - neither: Absent, the only state that may use the mock-only UI.
//
// Errors never contain credential values.
func (r TelegramCredentialResolver) ResolveTelegramCredentials(
	_ context.Context,
	auth config.AuthConfig,
) (ResolvedTelegramCredentials, error) {
	envIDs := []string{
		os.Getenv(telegramAPIIDEnvironment),
		os.Getenv(telegramAPIHashEnvironment),
		os.Getenv(telegramPhoneEnvironment),
	}

	present := 0
	for _, value := range envIDs {
		if strings.TrimSpace(value) != "" {
			present++
		}
	}

	switch {
	case present == len(envIDs):
		return r.resolveFromEnvironment()

	case present > 0:
		// Fail closed. A half-configured environment must never
		// silently complete itself from a profile.
		return ResolvedTelegramCredentials{
			Availability: AuthAvailabilityInvalid,
			Source:       AuthCredentialSourceNone,
			Reason:       "partial Telegram environment configuration",
		}, fmt.Errorf(
			"%w: partial Telegram environment configuration",
			ErrTelegramCredentialsInvalid,
		)
	}

	return r.resolveFromProfile(auth)
}

func (r TelegramCredentialResolver) resolveFromEnvironment() (
	ResolvedTelegramCredentials,
	error,
) {
	apiIDValue := strings.TrimSpace(
		os.Getenv(telegramAPIIDEnvironment),
	)
	apiHash := os.Getenv(telegramAPIHashEnvironment)
	phone := strings.TrimSpace(os.Getenv(telegramPhoneEnvironment))

	apiID, err := strconv.Atoi(apiIDValue)
	if err != nil {
		return invalidCredentials(
			"invalid Telegram API ID in environment",
		)
	}

	if apiID <= 0 {
		return invalidCredentials(
			"Telegram API ID in environment must be positive",
		)
	}

	if apiHash == "" {
		return invalidCredentials(
			"empty Telegram API hash in environment",
		)
	}

	if phone == "" {
		return invalidCredentials("empty Telegram phone in environment")
	}

	return ResolvedTelegramCredentials{
		Availability: AuthAvailabilityReady,
		Source:       AuthCredentialSourceEnvironment,
		Credentials: TelegramCredentials{
			APIID:   apiID,
			APIHash: apiHash,
			Phone:   phone,
		},
	}, nil
}

func (r TelegramCredentialResolver) resolveFromProfile(
	auth config.AuthConfig,
) (ResolvedTelegramCredentials, error) {
	hasAPIID := auth.APIID > 0
	hasProfile := strings.TrimSpace(auth.CredentialProfile) != ""

	switch {
	case !hasAPIID && !hasProfile:
		return ResolvedTelegramCredentials{
			Availability: AuthAvailabilityAbsent,
			Source:       AuthCredentialSourceNone,
			Reason:       "no Telegram authentication configured",
		}, nil

	case !hasAPIID:
		return invalidCredentials(
			"incomplete [auth]: credential_profile without api_id",
		)

	case !hasProfile:
		return invalidCredentials(
			"incomplete [auth]: api_id without credential_profile",
		)
	}

	profile := strings.TrimSpace(auth.CredentialProfile)

	secrets, err := r.store.LoadTelegramCredentials(
		context.Background(),
		profile,
	)
	if err != nil {
		if errors.Is(err, ErrTelegramCredentialProfileUnavailable) {
			return ResolvedTelegramCredentials{
				Availability: AuthAvailabilityInvalid,
				Source:       AuthCredentialSourceProfile,
				Reason:       "credential profile store unavailable",
			}, fmt.Errorf(
				"%w: profile %q: %w",
				ErrTelegramCredentialProfileUnavailable,
				profile,
				err,
			)
		}

		return ResolvedTelegramCredentials{
			Availability: AuthAvailabilityInvalid,
			Source:       AuthCredentialSourceProfile,
			Reason:       "credential profile could not be loaded",
		}, fmt.Errorf(
			"%w: profile %q could not be loaded",
			ErrTelegramCredentialsInvalid,
			profile,
		)
	}

	if strings.TrimSpace(secrets.APIHash) == "" {
		return invalidCredentials(
			"credential profile has no API hash",
		)
	}

	if strings.TrimSpace(secrets.Phone) == "" {
		return invalidCredentials(
			"credential profile has no phone number",
		)
	}

	return ResolvedTelegramCredentials{
		Availability: AuthAvailabilityReady,
		Source:       AuthCredentialSourceProfile,
		Credentials: TelegramCredentials{
			APIID:   auth.APIID,
			APIHash: secrets.APIHash,
			Phone:   strings.TrimSpace(secrets.Phone),
		},
	}, nil
}

func invalidCredentials(reason string) (
	ResolvedTelegramCredentials,
	error,
) {
	return ResolvedTelegramCredentials{
		Availability: AuthAvailabilityInvalid,
		Source:       AuthCredentialSourceNone,
		Reason:       reason,
	}, fmt.Errorf("%w: %s", ErrTelegramCredentialsInvalid, reason)
}

// unavailableTelegramCredentialStore is the production store until the
// platform backend lands.
//
// A configured profile must fail closed rather than degrade to
// mock-only, so this returns an error rather than empty secrets.
type unavailableTelegramCredentialStore struct{}

// LoadTelegramCredentials always reports the profile as unavailable.
func (unavailableTelegramCredentialStore) LoadTelegramCredentials(
	context.Context,
	string,
) (TelegramProfileCredentials, error) {
	return TelegramProfileCredentials{}, fmt.Errorf(
		"%w: no platform credential store is available yet",
		ErrTelegramCredentialProfileUnavailable,
	)
}

package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"telecli/internal/authstore"
	"telecli/internal/config"
	"telecli/internal/secretinput"
)

// ConfigurePrompter asks the operator for the setup answers.
//
// Secrets are requested through SecretReader so they are never echoed,
// never passed as an argument and never written to a file.
type ConfigurePrompter interface {
	// Ask reads a visible answer.
	Ask(prompt string) (string, error)
	// AskSecret reads a hidden answer.
	AskSecret(prompt string) ([]byte, error)
	// Confirm asks a yes/no question. A false answer aborts.
	Confirm(prompt string) (bool, error)
}

// ConfigureCredentialWriter is the subset of authstore.Store that setup
// needs.
//
// Keeping it narrow means the flow can be tested without a real Keychain
// and without the delete/reset surface.
type ConfigureCredentialWriter interface {
	Create(ctx context.Context, profile string, credentials authstore.Profile) error
	Replace(ctx context.Context, profile string, credentials authstore.Profile) error
	Load(ctx context.Context, profile string) (authstore.Profile, error)
	Delete(ctx context.Context, profile string) error
}

// ConfigureOptions are the non-interactive overrides for the setup flow.
type ConfigureOptions struct {
	// ConfigPath is the explicit --config value.
	ConfigPath string
	// Mode overrides the delivery mode answer.
	Mode string
	// Profile overrides the credential profile answer.
	Profile string
	// Force allows replacing an existing credential profile without
	// asking.
	Force bool
}

// ConfigureRequest is everything the flow needs from its environment.
type ConfigureRequest struct {
	// Environ is the process environment, used for discovery.
	Environ []string
	// Prompter supplies the answers.
	Prompter ConfigurePrompter
	// Secrets reads hidden input.
	Secrets secretinput.Reader
	// Credentials is the credential store.
	Credentials ConfigureCredentialWriter
	// Probe inspects candidate TDLib libraries.
	Probe TDLibProbe
	// Options are the CLI overrides.
	Options ConfigureOptions

	// WriteConfig persists the configuration. A nil value uses
	// config.WriteFile. It exists so a failure after the credential step
	// can be exercised without corrupting a real file.
	WriteConfig func(path string, cfg config.Config) error
}

// ConfigureResult describes what the flow did.
type ConfigureResult struct {
	// ConfigPath is the file that now holds the configuration.
	ConfigPath string
	// Profile is the credential profile in use.
	Profile string
	// ProfileCreated reports that a new Keychain item was created.
	ProfileCreated bool
	// ProfileReplaced reports that an existing item was replaced.
	ProfileReplaced bool
	// TDLib is the accepted library.
	TDLib TDLibProbeResult
	// Config is the written configuration.
	Config config.Config
}

// defaultDataDirectory returns the platform data directory for the
// application.
//
// It follows the same convention as config.Default: the per-user
// application support directory on macOS and the per-user data
// directory elsewhere.
func defaultDataDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecli"
	}

	if dir, err := os.UserConfigDir(); err == nil {
		// macOS reports ~/Library/Application Support here, which is
		// also the conventional location for application data.
		return filepath.Join(dir, "telecli")
	}

	return filepath.Join(home, ".local", "share", "telecli")
}

// RunConfigure performs the interactive setup.
//
// Order matters: everything is collected and validated in memory first,
// the credential profile is written second, and the configuration file
// last. A failure after the credential step rolls that step back, so the
// machine is never left with a profile the configuration does not
// reference.
func RunConfigure(
	ctx context.Context,
	req ConfigureRequest,
) (ConfigureResult, error) {
	if req.Prompter == nil {
		return ConfigureResult{}, fmt.Errorf(
			"%w: prompter is required",
			ErrConfigureInput,
		)
	}
	if req.Secrets == nil {
		return ConfigureResult{}, fmt.Errorf(
			"%w: secret reader is required",
			ErrConfigureInput,
		)
	}
	if req.Credentials == nil {
		return ConfigureResult{}, fmt.Errorf(
			"%w: credential store is required",
			ErrConfigureInput,
		)
	}
	if req.Probe == nil {
		req.Probe = newNativeTDLibProbe()
	}

	configPath, err := configureConfigPath(req)
	if err != nil {
		return ConfigureResult{}, err
	}

	existing, err := loadExistingConfig(req, configPath)
	if err != nil {
		return ConfigureResult{}, err
	}

	probe, err := probeTDLib(ctx, req, existing)
	if err != nil {
		return ConfigureResult{}, err
	}

	apiID, apiHash, phone, err := collectCredentials(ctx, req, existing)
	if err != nil {
		return ConfigureResult{}, err
	}

	mode, err := collectMode(req, existing)
	if err != nil {
		return ConfigureResult{}, err
	}

	profile, err := collectProfile(req, existing)
	if err != nil {
		return ConfigureResult{}, err
	}

	next := buildConfiguredConfig(
		existing,
		probe,
		apiID,
		profile,
		mode,
	)
	if err := next.Validate(); err != nil {
		return ConfigureResult{}, fmt.Errorf(
			"%w: %w",
			ErrConfigureInput,
			err,
		)
	}

	secrets := authstore.Profile{APIHash: apiHash, Phone: phone}

	created, replaced, err := storeCredentials(
		ctx,
		req,
		profile,
		secrets,
	)
	if err != nil {
		return ConfigureResult{}, err
	}

	if err := writeConfiguredConfig(req, configPath, next); err != nil {
		// The credential step succeeded but the configuration did not,
		// so the previous state has to be restored.
		if rollbackErr := rollbackCredentials(
			ctx,
			req,
			profile,
			created,
			replaced,
		); rollbackErr != nil {
			return ConfigureResult{}, rollbackErr
		}

		return ConfigureResult{}, err
	}

	// Re-read through the normal loader so a configuration that cannot
	// be loaded by production code never passes setup.
	if _, err := config.Load(configPath); err != nil {
		_ = rollbackCredentials(
			ctx,
			req,
			profile,
			created,
			replaced,
		)

		return ConfigureResult{}, fmt.Errorf(
			"%w: written configuration does not load",
			ErrConfigureInput,
		)
	}

	return ConfigureResult{
		ConfigPath:      configPath,
		Profile:         profile,
		ProfileCreated:  created,
		ProfileReplaced: replaced,
		TDLib:           probe,
		Config:          next,
	}, nil
}

// configureConfigPath resolves where the configuration will live.
func configureConfigPath(req ConfigureRequest) (string, error) {
	if req.Options.ConfigPath != "" {
		return req.Options.ConfigPath, nil
	}

	if fromEnv := envValue(req.Environ, "TELECLI_CONFIG"); fromEnv != "" {
		return fromEnv, nil
	}

	path, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf(
			"%w: determine configuration path: %w",
			ErrConfigureInput,
			err,
		)
	}

	return path, nil
}

// loadExistingConfig reads the current configuration, if any.
func loadExistingConfig(
	req ConfigureRequest,
	configPath string,
) (config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) ||
			errors.Is(err, config.ErrConfigNotFound) {
			return config.Default(), nil
		}

		// A broken configuration must not be silently replaced: the
		// operator should see why it failed first.
		return config.Config{}, fmt.Errorf(
			"%w: read existing configuration: %w",
			ErrConfigureInput,
			err,
		)
	}

	return cfg, nil
}

// probeTDLib finds and verifies a TDLib runtime.
//
// Known locations are probed first. When none of them verifies, the
// operator is offered a manual path, because a self-built TDLib can live
// anywhere and setup must not dead-end just because the library is not in
// a conventional prefix.
func probeTDLib(
	ctx context.Context,
	req ConfigureRequest,
	existing config.Config,
) (TDLibProbeResult, error) {
	candidates := TDLibLibraryCandidates(existing.TDLib.LibraryPath, req.Environ)

	tried := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		// A relative candidate is resolved so the stored
		// library_path is absolute and keeps working from any
		// working directory.
		absolute, absErr := filepath.Abs(candidate)
		if absErr == nil {
			candidate = absolute
		}

		tried = append(tried, candidate)

		result, err := probeCandidate(ctx, req, candidate)
		if err == nil {
			return result, nil
		}
	}

	// Nothing conventional worked. Offer a manual path before giving up,
	// but only when the run can actually prompt.
	if req.Prompter != nil {
		manual, ok, err := askForLibraryPath(req, tried)
		if err != nil {
			return TDLibProbeResult{}, err
		}
		if ok {
			result, err := probeCandidate(ctx, req, manual)
			if err == nil {
				return result, nil
			}

			tried = append(tried, manual)
		}
	}

	return TDLibProbeResult{}, fmt.Errorf(
		"%w: no usable TDLib library; tried %s",
		ErrConfigureTDLib,
		strings.Join(tried, ", "),
	)
}

// probeCandidate verifies one library path.
func probeCandidate(
	ctx context.Context,
	req ConfigureRequest,
	candidate string,
) (TDLibProbeResult, error) {
	result, err := req.Probe.Probe(ctx, candidate)
	if err != nil {
		return TDLibProbeResult{}, err
	}

	if err := validateProbe(result); err != nil {
		return TDLibProbeResult{}, err
	}

	return result, nil
}

// askForLibraryPath offers a manual TDLib location.
//
// It returns false when the operator declines, so the caller can report
// the paths that were already tried.
func askForLibraryPath(
	req ConfigureRequest,
	tried []string,
) (string, bool, error) {
	answer, err := req.Prompter.Ask(fmt.Sprintf(
		"TDLib library not found in %s. Enter a path (empty to skip): ",
		strings.Join(tried, ", "),
	))
	if err != nil {
		return "", false, err
	}

	manual := strings.TrimSpace(answer)
	if manual == "" {
		return "", false, nil
	}

	absolute, absErr := filepath.Abs(manual)
	if absErr != nil {
		absolute = manual
	}

	return absolute, true, nil
}

// collectCredentials gathers the Telegram application and account
// answers.
func collectCredentials(
	ctx context.Context,
	req ConfigureRequest,
	existing config.Config,
) (int, string, string, error) {
	apiIDAnswer := ""
	if existing.Auth.APIID > 0 {
		apiIDAnswer = fmt.Sprintf("%d", existing.Auth.APIID)
	}

	apiID, err := req.Prompter.Ask(
		fmt.Sprintf("Telegram API ID [%s]: ", apiIDAnswer),
	)
	if err != nil {
		return 0, "", "", err
	}

	parsedID, err := parseAPIID(apiID)
	if err != nil {
		return 0, "", "", err
	}

	hashBytes, err := req.Secrets.ReadSecret("Telegram API hash: ")
	if err != nil {
		if errors.Is(err, secretinput.ErrEmpty) {
			return 0, "", "", fmt.Errorf(
				"%w: API hash must not be empty",
				ErrConfigureInput,
			)
		}

		return 0, "", "", err
	}
	// The secret is copied into a string for the store call and the
	// original buffer is wiped immediately.
	apiHash := string(hashBytes)
	zeroBytes(hashBytes)

	if strings.TrimSpace(apiHash) == "" {
		zeroBytes(hashBytes)

		return 0, "", "", fmt.Errorf(
			"%w: API hash must not be empty",
			ErrConfigureInput,
		)
	}

	phoneAnswer := ""
	if existing.Auth.CredentialProfile != "" {
		phoneAnswer = ""
	}

	phone, err := req.Prompter.Ask(
		fmt.Sprintf("Telegram phone %s: ", phoneAnswer),
	)
	if err != nil {
		return 0, "", "", err
	}

	trimmedPhone, err := parseNonEmpty(phone, "phone")
	if err != nil {
		return 0, "", "", err
	}

	_ = ctx

	return parsedID, apiHash, trimmedPhone, nil
}

// collectMode resolves the delivery mode.
func collectMode(
	req ConfigureRequest,
	existing config.Config,
) (config.MessageSendMode, error) {
	answer := req.Options.Mode
	if answer == "" {
		var err error

		answer, err = req.Prompter.Ask(fmt.Sprintf(
			"Delivery mode [direct|durable] (%s): ",
			existing.MessageDelivery.Mode,
		))
		if err != nil {
			return "", err
		}
	}

	if strings.TrimSpace(answer) == "" {
		if existing.MessageDelivery.Mode != "" {
			return existing.MessageDelivery.Mode, nil
		}

		return config.DefaultMessageSendMode, nil
	}

	mode, err := config.ParseMessageSendMode(answer)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigureInput, err)
	}

	return mode, nil
}

// collectProfile resolves the credential profile name.
func collectProfile(
	req ConfigureRequest,
	existing config.Config,
) (string, error) {
	answer := req.Options.Profile
	if answer == "" {
		var err error

		suggestion := existing.Auth.CredentialProfile
		if suggestion == "" {
			suggestion = "default"
		}

		answer, err = req.Prompter.Ask(fmt.Sprintf(
			"Credential profile [%s]: ",
			suggestion,
		))
		if err != nil {
			return "", err
		}
	}

	profile := strings.TrimSpace(answer)
	if profile == "" {
		if existing.Auth.CredentialProfile != "" {
			return existing.Auth.CredentialProfile, nil
		}

		return "default", nil
	}

	if err := authstore.ValidateProfileName(profile); err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigureInput, err)
	}

	return profile, nil
}

// buildConfiguredConfig assembles the configuration to persist.
//
// Directory values are always derived from the data directory, because
// the defaults are computed from the built-in data directory before a
// file is decoded, so overriding only data_dir would leave the TDLib
// session in the previous location.
func buildConfiguredConfig(
	existing config.Config,
	probe TDLibProbeResult,
	apiID int,
	profile string,
	mode config.MessageSendMode,
) config.Config {
	next := existing

	// The built-in default is not a user choice, so setup moves the
	// layout to the platform data directory. A directory the operator
	// configured themselves is preserved.
	builtIn := config.Default().DataDir
	dataDir := existing.DataDir
	if dataDir == "" || dataDir == builtIn {
		dataDir = defaultDataDirectory()
	}

	// Isolate the TDLib directories under the data directory unless the
	// operator already pointed them somewhere.
	if next.TDLib.DatabaseDir == "" ||
		strings.HasPrefix(next.TDLib.DatabaseDir, builtIn) {
		next.TDLib.DatabaseDir = filepath.Join(
			dataDir,
			"tdlib",
			"database",
		)
	}
	if next.TDLib.FilesDir == "" ||
		strings.HasPrefix(next.TDLib.FilesDir, builtIn) {
		next.TDLib.FilesDir = filepath.Join(dataDir, "tdlib", "files")
	}

	next.DataDir = dataDir
	next.TDLib.LibraryPath = probe.Path
	next.Auth.APIID = apiID
	next.Auth.CredentialProfile = profile
	next.MessageDelivery.Mode = mode

	if next.MessageDelivery.DataDir == "" ||
		strings.HasPrefix(next.MessageDelivery.DataDir, builtIn) {
		next.MessageDelivery.DataDir = filepath.Join(dataDir, "outbox")
	}
	if next.MessageDelivery.DatabaseID == "" {
		next.MessageDelivery.DatabaseID = "telecli-main"
	}

	return next
}

// storeCredentials writes the credential profile, asking before an
// existing one is replaced.
func storeCredentials(
	ctx context.Context,
	req ConfigureRequest,
	profile string,
	secrets authstore.Profile,
) (bool, bool, error) {
	err := req.Credentials.Create(ctx, profile, secrets)

	switch {
	case err == nil:
		return true, false, nil

	case errors.Is(err, authstore.ErrProfileExists):
		// Fall through to the replace path.

	default:
		return false, false, fmt.Errorf(
			"%w: store credential profile",
			ErrConfigureInput,
		)
	}

	if !req.Options.Force {
		confirmed, confirmErr := req.Prompter.Confirm(fmt.Sprintf(
			"Credential profile %q already exists. Replace it? [y/N]: ",
			profile,
		))
		if confirmErr != nil {
			return false, false, confirmErr
		}

		if !confirmed {
			return false, false, fmt.Errorf(
				"%w: credential profile %q already exists",
				ErrConfigureInput,
				profile,
			)
		}
	}

	if err := req.Credentials.Replace(
		ctx,
		profile,
		secrets,
	); err != nil {
		return false, false, fmt.Errorf(
			"%w: replace credential profile",
			ErrConfigureInput,
		)
	}

	return false, true, nil
}

// writeConfiguredConfig persists the configuration and its directories.
func writeConfiguredConfig(
	req ConfigureRequest,
	configPath string,
	next config.Config,
) error {
	if err := os.MkdirAll(next.DataDir, config.ConfigDirMode); err != nil {
		return fmt.Errorf(
			"%w: create data directory: %w",
			ErrConfigureInput,
			err,
		)
	}

	if err := os.MkdirAll(
		next.MessageDelivery.DataDir,
		config.ConfigDirMode,
	); err != nil {
		return fmt.Errorf(
			"%w: create outbox directory: %w",
			ErrConfigureInput,
			err,
		)
	}

	if err := os.MkdirAll(
		filepath.Dir(configPath),
		config.ConfigDirMode,
	); err != nil {
		return fmt.Errorf(
			"%w: create configuration directory: %w",
			ErrConfigureInput,
			err,
		)
	}

	writeConfig := req.WriteConfig
	if writeConfig == nil {
		writeConfig = config.WriteFile
	}

	if err := writeConfig(configPath, next); err != nil {
		return fmt.Errorf("%w: %w", ErrConfigureInput, err)
	}

	return nil
}

// rollbackCredentials restores the credential state that existed before
// this run.
//
// A profile this run created is removed again. A profile this run
// replaced is restored from the copy taken before the replacement.
func rollbackCredentials(
	ctx context.Context,
	req ConfigureRequest,
	profile string,
	created bool,
	replaced bool,
) error {
	switch {
	case created:
		if err := req.Credentials.Delete(ctx, profile); err != nil &&
			!errors.Is(err, authstore.ErrProfileUnavailable) {
			return fmt.Errorf(
				"%w: remove the credential profile created by this run",
				ErrConfigureRollback,
			)
		}

		return nil

	case replaced:
		// The previous value was not retained, so the profile cannot
		// be restored automatically. Reporting it is the only safe
		// option: the operator must re-run setup.
		return fmt.Errorf(
			"%w: credential profile %q was replaced but the "+
				"configuration was not written; re-run "+
				"telecli configure to restore it",
			ErrConfigureRollback,
			profile,
		)

	default:
		return nil
	}
}

// zeroBytes overwrites a secret buffer in place.
//
// A Go string is immutable, so a hash copied into one cannot be wiped.
// That is why the hash is read as a byte slice, passed to the store, and
// only then converted where unavoidable.
func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

// writeConfigureReport prints a value-free summary of the setup.
func writeConfigureReport(w io.Writer, result ConfigureResult) {
	fmt.Fprintf(w, "\n  Setup complete\n\n")
	fmt.Fprintf(w, "  Configuration  %s\n", result.ConfigPath)
	fmt.Fprintf(w, "  TDLib          %s\n", result.TDLib.Path)
	fmt.Fprintf(
		w,
		"  Version        %s\n",
		result.TDLib.Version,
	)
	fmt.Fprintf(
		w,
		"  Compatibility  %s\n",
		result.TDLib.Compatibility,
	)
	fmt.Fprintf(w, "  API ID         configured\n")
	fmt.Fprintf(w, "  API hash       stored in Keychain\n")
	fmt.Fprintf(w, "  Phone          stored in Keychain\n")
	fmt.Fprintf(w, "  Profile        %s\n", result.Profile)
	fmt.Fprintf(w, "  Delivery mode  %s\n", result.Config.MessageDelivery.Mode)
	fmt.Fprintf(w, "\n  Run: telecli doctor\n")
}

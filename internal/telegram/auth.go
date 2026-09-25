package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrInvalidAuthParameters is returned when an auth request cannot be
// built from the given input.
var ErrInvalidAuthParameters = errors.New("auth: invalid parameters")

// ErrUnsupportedAuthState is returned when TDLib reaches an
// authorization state that PR-05 does not implement, or when an
// authorization_state type is not recognized at all.
var ErrUnsupportedAuthState = errors.New("auth: unsupported authorization state")

// AuthState is the TDLib authorization state relevant to telecli.
type AuthState string

const (
	AuthStateClosed              AuthState = "closed"
	AuthStateClosing             AuthState = "closing"
	AuthStateWaitTdlibParameters AuthState = "wait_tdlib_parameters"
	AuthStateWaitPhoneNumber     AuthState = "wait_phone_number"
	AuthStateWaitCode            AuthState = "wait_code"
	AuthStateWaitPassword        AuthState = "wait_password"
	AuthStateWaitEmailAddress    AuthState = "wait_email_address"
	AuthStateWaitEmailCode       AuthState = "wait_email_code"
	AuthStateWaitOtherDevice     AuthState = "wait_other_device"
	AuthStateWaitRegistration    AuthState = "wait_registration"
	AuthStateWaitPremiumPurchase AuthState = "wait_premium_purchase"
	AuthStateReady               AuthState = "ready"
	AuthStateLoggingOut          AuthState = "logging_out"
	AuthStateUnknown             AuthState = "unknown"
)

// Valid reports whether s is a known AuthState.
func (s AuthState) Valid() bool {
	switch s {
	case AuthStateClosed, AuthStateClosing,
		AuthStateWaitTdlibParameters,
		AuthStateWaitPhoneNumber, AuthStateWaitCode, AuthStateWaitPassword,
		AuthStateWaitEmailAddress, AuthStateWaitEmailCode,
		AuthStateWaitOtherDevice, AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase,
		AuthStateReady, AuthStateLoggingOut, AuthStateUnknown:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (s AuthState) String() string { return string(s) }

type authEnvelope struct {
	Type               string          `json:"@type"`
	AuthorizationState json.RawMessage `json:"authorization_state"`
}

type authStateEnvelope struct {
	Type string `json:"@type"`
}

var tdlibAuthStateTypes = map[string]AuthState{
	"authorizationStateClosed":                      AuthStateClosed,
	"authorizationStateClosing":                     AuthStateClosing,
	"authorizationStateWaitTdlibParameters":         AuthStateWaitTdlibParameters,
	"authorizationStateWaitPhoneNumber":             AuthStateWaitPhoneNumber,
	"authorizationStateWaitCode":                    AuthStateWaitCode,
	"authorizationStateWaitPassword":                AuthStateWaitPassword,
	"authorizationStateWaitEmailAddress":            AuthStateWaitEmailAddress,
	"authorizationStateWaitEmailCode":               AuthStateWaitEmailCode,
	"authorizationStateWaitOtherDeviceConfirmation": AuthStateWaitOtherDevice,
	"authorizationStateWaitRegistration":            AuthStateWaitRegistration,
	"authorizationStateWaitPremiumPurchase":         AuthStateWaitPremiumPurchase,
	"authorizationStateReady":                       AuthStateReady,
	"authorizationStateLoggingOut":                  AuthStateLoggingOut,
}

// ParseAuthUpdate extracts AuthState from either an
// updateAuthorizationState envelope or a direct authorization-state
// response object.
//
// Direct response (from getAuthorizationState):
//
//	{"@type":"authorizationStateWaitTdlibParameters"}
//
// Envelope form (from updateAuthorizationState):
//
//	{"@type":"updateAuthorizationState","authorization_state":{"@type":"..."}}
//
// Semantics:
//
//   - unrelated update:            (AuthStateUnknown, nil)
//   - unknown authorizationState*: (AuthStateUnknown, ErrUnsupportedAuthState)
//   - malformed JSON:              (AuthStateUnknown, error)
func ParseAuthUpdate(raw []byte) (AuthState, error) {
	var env authEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return AuthStateUnknown, fmt.Errorf("decode update envelope: %w", err)
	}

	if env.Type == "updateAuthorizationState" {
		if len(env.AuthorizationState) == 0 {
			return AuthStateUnknown, errors.New(
				"updateAuthorizationState without state",
			)
		}
		var s authStateEnvelope
		if err := json.Unmarshal(env.AuthorizationState, &s); err != nil {
			return AuthStateUnknown, fmt.Errorf(
				"decode authorization_state: %w", err,
			)
		}
		if state, ok := tdlibAuthStateTypes[s.Type]; ok {
			return state, nil
		}
		return AuthStateUnknown, fmt.Errorf(
			"%w: %s", ErrUnsupportedAuthState, s.Type,
		)
	}

	// Direct authorization-state response from getAuthorizationState.
	if state, ok := tdlibAuthStateTypes[env.Type]; ok {
		return state, nil
	}
	if strings.HasPrefix(env.Type, "authorizationState") {
		return AuthStateUnknown, fmt.Errorf(
			"%w: %s", ErrUnsupportedAuthState, env.Type,
		)
	}

	return AuthStateUnknown, nil
}

// TdlibParameters is the parameter set needed to initialize TDLib.
type TdlibParameters struct {
	APIID                 int
	APIHash               string
	DatabaseDirectory     string
	FilesDirectory        string
	DatabaseEncryptionKey []byte
	SystemLanguageCode    string
	DeviceModel           string
	SystemVersion         string
	ApplicationVersion    string
}

// AuthInput carries user-provided values for the current auth step.
type AuthInput struct {
	PhoneNumber string
	Code        string
	Password    string
}

// AuthSession is a pure-Go authorization state machine.
type AuthSession struct {
	mu     sync.Mutex
	state  AuthState
	params TdlibParameters
}

// NewAuthSession returns a session in AuthStateUnknown.
func NewAuthSession(params TdlibParameters) *AuthSession {
	return &AuthSession{state: AuthStateUnknown, params: params}
}

// State returns the current AuthState.
func (s *AuthSession) State() AuthState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Step handles a state transition and returns zero or one request to
// send to TDLib.
func (s *AuthSession) Step(next AuthState, input AuthInput) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = next

	switch next {
	case AuthStateWaitTdlibParameters:
		return buildSetTdlibParameters(s.params)

	case AuthStateWaitPhoneNumber:
		if input.PhoneNumber == "" {
			return nil, nil
		}
		return buildSetPhoneNumber(input.PhoneNumber)

	case AuthStateWaitCode:
		if input.Code == "" {
			return nil, nil
		}
		return buildCheckCode(input.Code)

	case AuthStateWaitPassword:
		if input.Password == "" {
			return nil, nil
		}
		return buildCheckPassword(input.Password)

	case AuthStateWaitEmailAddress,
		AuthStateWaitEmailCode,
		AuthStateWaitOtherDevice,
		AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAuthState, next)

	case AuthStateClosed, AuthStateClosing,
		AuthStateReady, AuthStateLoggingOut, AuthStateUnknown:
		return nil, nil
	}
	return nil, nil
}

// NeedsInput reports whether the current state requires user input.
func (s *AuthSession) NeedsInput() bool {
	switch s.State() {
	case AuthStateWaitPhoneNumber, AuthStateWaitCode, AuthStateWaitPassword:
		return true
	}
	return false
}

// Done reports whether authorization has completed.
func (s *AuthSession) Done() bool { return s.State() == AuthStateReady }

// ---- request builders ----

type setTdlibParametersRequest struct {
	Type                  string `json:"@type"`
	UseTestDc             bool   `json:"use_test_dc"`
	DatabaseDirectory     string `json:"database_directory"`
	FilesDirectory        string `json:"files_directory"`
	DatabaseEncryptionKey []byte `json:"database_encryption_key"`
	UseFileDatabase       bool   `json:"use_file_database"`
	UseChatInfoDatabase   bool   `json:"use_chat_info_database"`
	UseMessageDatabase    bool   `json:"use_message_database"`
	UseSecretChats        bool   `json:"use_secret_chats"`
	APIID                 int    `json:"api_id"`
	APIHash               string `json:"api_hash"`
	SystemLanguageCode    string `json:"system_language_code"`
	DeviceModel           string `json:"device_model"`
	SystemVersion         string `json:"system_version"`
	ApplicationVersion    string `json:"application_version"`
}

type setPhoneNumberRequest struct {
	Type        string `json:"@type"`
	PhoneNumber string `json:"phone_number"`
	Settings    any    `json:"settings"`
}

type checkCodeRequest struct {
	Type string `json:"@type"`
	Code string `json:"code"`
}

type checkPasswordRequest struct {
	Type     string `json:"@type"`
	Password string `json:"password"`
}

func buildSetTdlibParameters(p TdlibParameters) ([]byte, error) {
	if p.APIID <= 0 ||
		p.APIHash == "" ||
		p.DatabaseDirectory == "" ||
		p.FilesDirectory == "" ||
		p.SystemLanguageCode == "" ||
		p.DeviceModel == "" ||
		p.ApplicationVersion == "" {
		return nil, ErrInvalidAuthParameters
	}

	return json.Marshal(setTdlibParametersRequest{
		Type:                  "setTdlibParameters",
		UseTestDc:             false,
		DatabaseDirectory:     p.DatabaseDirectory,
		FilesDirectory:        p.FilesDirectory,
		DatabaseEncryptionKey: p.DatabaseEncryptionKey,
		UseFileDatabase:       true,
		UseChatInfoDatabase:   true,
		UseMessageDatabase:    true,
		UseSecretChats:        false,
		APIID:                 p.APIID,
		APIHash:               p.APIHash,
		SystemLanguageCode:    p.SystemLanguageCode,
		DeviceModel:           p.DeviceModel,
		SystemVersion:         p.SystemVersion,
		ApplicationVersion:    p.ApplicationVersion,
	})
}

func buildSetPhoneNumber(phone string) ([]byte, error) {
	if phone == "" {
		return nil, ErrInvalidAuthParameters
	}
	return json.Marshal(setPhoneNumberRequest{
		Type:        "setAuthenticationPhoneNumber",
		PhoneNumber: phone,
		Settings:    nil,
	})
}

func buildCheckCode(code string) ([]byte, error) {
	if code == "" {
		return nil, ErrInvalidAuthParameters
	}
	return json.Marshal(checkCodeRequest{
		Type: "checkAuthenticationCode",
		Code: code,
	})
}

func buildCheckPassword(password string) ([]byte, error) {
	if password == "" {
		return nil, ErrInvalidAuthParameters
	}
	return json.Marshal(checkPasswordRequest{
		Type:     "checkAuthenticationPassword",
		Password: password,
	})
}

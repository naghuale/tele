package authstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// telegramCredentialEnvelopeVersion is the only supported wire version.
//
// Bumping it forces an explicit migration instead of guessing how to read
// an older or newer payload.
const telegramCredentialEnvelopeVersion = 1

// telegramCredentialEnvelope is the on-disk shape of a stored profile.
//
// It stays inside this package: the application contract only ever sees
// Profile.
type telegramCredentialEnvelope struct {
	Version int    `json:"version"`
	APIHash string `json:"api_hash"`
	Phone   string `json:"phone"`
}

// encodeTelegramCredentialEnvelope renders a profile for storage.
func encodeTelegramCredentialEnvelope(
	credentials Profile,
) ([]byte, error) {
	if err := validateProfile(credentials); err != nil {
		return nil, err
	}

	raw, err := json.Marshal(telegramCredentialEnvelope{
		Version: telegramCredentialEnvelopeVersion,
		APIHash: credentials.APIHash,
		Phone:   credentials.Phone,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%w: encode credential envelope",
			ErrInvalidProfile,
		)
	}

	return raw, nil
}

// decodeTelegramCredentialEnvelope parses a stored profile.
//
// Unknown fields, an unsupported version and trailing data are all
// rejected. The payload is never included in an error, because it holds
// the credentials themselves.
func decodeTelegramCredentialEnvelope(
	data []byte,
) (telegramCredentialEnvelope, error) {
	if len(data) == 0 {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("credential envelope is empty"),
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var envelope telegramCredentialEnvelope

	if err := decoder.Decode(&envelope); err != nil {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("decode credential envelope"),
		)
	}

	// A second Decode must hit EOF. More() alone does not prove the
	// input ended, so the extra decode is what makes trailing data an
	// error rather than silently ignored bytes.
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("credential envelope has trailing data"),
		)
	}

	if envelope.Version != telegramCredentialEnvelopeVersion {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("unsupported credential envelope version"),
		)
	}

	if envelope.APIHash == "" {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("credential envelope has no API hash"),
		)
	}

	if envelope.Phone == "" {
		return telegramCredentialEnvelope{}, errors.Join(
			ErrInvalidProfile,
			errors.New("credential envelope has no phone number"),
		)
	}

	return envelope, nil
}

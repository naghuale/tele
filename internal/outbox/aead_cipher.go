package outbox

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// payloadEnvelopeVersion is the current encrypted payload envelope
	// version.
	payloadEnvelopeVersion byte = 1

	// payloadAlgorithmXChaCha20Poly1305 identifies the encryption
	// algorithm used by this implementation.
	payloadAlgorithmXChaCha20Poly1305 byte = 1

	// payloadEnvelopeHeaderSize contains:
	//
	//	version       1 byte
	//	algorithm     1 byte
	//	nonce length  1 byte
	payloadEnvelopeHeaderSize = 3

	// payloadAADVersion identifies the binary associated-data encoding.
	//
	// It is independent from payloadEnvelopeVersion so the associated
	// data format and envelope format can evolve separately.
	payloadAADVersion byte = 1

	// payloadBindingVersion identifies the application-level binding
	// between an encrypted message and its outbox entry.
	//
	// It is deliberately independent from sqliteSchemaVersion.
	// Ordinary SQLite migrations must not invalidate existing
	// encrypted payloads: changing sqliteSchemaVersion alone must not
	// change associated data.
	//
	// Bump this only when the meaning of an outbox entry's identity
	// changes, and treat it as a full re-encryption event for existing
	// rows.
	payloadBindingVersion uint32 = 1
)

var (
	// ErrCipherInvalidKey is returned when the supplied payload
	// encryption key has an invalid length.
	ErrCipherInvalidKey = errors.New(
		"outbox: invalid cipher key",
	)

	// ErrCipherInvalidEnvelope is returned when an encrypted payload is
	// truncated, malformed, or internally inconsistent.
	ErrCipherInvalidEnvelope = errors.New(
		"outbox: invalid cipher envelope",
	)

	// ErrCipherUnsupportedVersion is returned when the envelope version
	// is not supported by this implementation.
	ErrCipherUnsupportedVersion = errors.New(
		"outbox: unsupported cipher version",
	)

	// ErrCipherUnsupportedAlgorithm is returned when the envelope names
	// an encryption algorithm that this implementation does not
	// support.
	ErrCipherUnsupportedAlgorithm = errors.New(
		"outbox: unsupported cipher algorithm",
	)

	// ErrCipherAuthentication is returned when ciphertext
	// authentication fails.
	//
	// The underlying AEAD error is intentionally not exposed. Callers
	// must not use error text to distinguish a wrong key from modified
	// ciphertext or mismatched associated data.
	ErrCipherAuthentication = errors.New(
		"outbox: cipher authentication failed",
	)

	// ErrCipherAssociatedDataTooLarge is returned when an associated
	// data string cannot be represented by the binary encoding.
	ErrCipherAssociatedDataTooLarge = errors.New(
		"outbox: cipher associated data too large",
	)
)

// AEADCipher encrypts message payloads using XChaCha20-Poly1305.
//
// Each encryption operation generates a new random nonce. Associated
// data binds the ciphertext to:
//
//   - the associated-data format version;
//   - the payload binding version;
//   - the outbox entry ID;
//   - the account key;
//   - the Telegram chat ID.
//
// Copying ciphertext to another entry, account, chat, or incompatible
// binding therefore causes authentication to fail.
type AEADCipher struct {
	aead           cipher.AEAD
	random         io.Reader
	bindingVersion uint32
}

// NewAEADCipher creates a PayloadCipher using XChaCha20-Poly1305.
//
// key must contain exactly chacha20poly1305.KeySize bytes. The caller
// retains ownership of key and may clear its input buffer after this
// function returns.
//
// Nonces are generated with crypto/rand.Reader.
func NewAEADCipher(
	key []byte,
) (PayloadCipher, error) {
	return newAEADCipher(
		key,
		rand.Reader,
		payloadBindingVersion,
	)
}

// newAEADCipher is the internal constructor used by tests.
//
// random must provide cryptographically secure random bytes in
// production. Tests may inject a deterministic or failing reader.
//
// bindingVersion is included in associated data. It is independent
// from sqliteSchemaVersion: ordinary SQLite migrations must not
// invalidate existing encrypted payloads.
func newAEADCipher(
	key []byte,
	random io.Reader,
	bindingVersion uint32,
) (*AEADCipher, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf(
			"%w: got %d bytes, want %d",
			ErrCipherInvalidKey,
			len(key),
			chacha20poly1305.KeySize,
		)
	}

	if random == nil {
		return nil, fmt.Errorf(
			"%w: nil random source",
			ErrCipherInvalidEnvelope,
		)
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox: create XChaCha20-Poly1305: %w",
			err,
		)
	}

	if aead.NonceSize() != chacha20poly1305.NonceSizeX {
		return nil, fmt.Errorf(
			"%w: AEAD nonce size %d, want %d",
			ErrCipherInvalidEnvelope,
			aead.NonceSize(),
			chacha20poly1305.NonceSizeX,
		)
	}

	if aead.NonceSize() > math.MaxUint8 {
		return nil, fmt.Errorf(
			"%w: nonce size %d cannot be encoded",
			ErrCipherInvalidEnvelope,
			aead.NonceSize(),
		)
	}

	return &AEADCipher{
		aead:           aead,
		random:         random,
		bindingVersion: bindingVersion,
	}, nil
}

// EncryptMessage implements PayloadCipher.
//
// The returned envelope has this binary layout:
//
//	version       1 byte
//	algorithm     1 byte
//	nonce length  1 byte
//	nonce         nonce length bytes
//	ciphertext    remaining bytes, including authentication tag
func (c *AEADCipher) EncryptMessage(
	ctx context.Context,
	entryID ID,
	accountKey string,
	chatID int64,
	plaintext []byte,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if c == nil || c.aead == nil {
		return nil, fmt.Errorf(
			"%w: uninitialized cipher",
			ErrCipherInvalidKey,
		)
	}

	if c.random == nil {
		return nil, fmt.Errorf(
			"%w: nil random source",
			ErrCipherInvalidEnvelope,
		)
	}

	aad, err := encodePayloadAssociatedData(
		c.bindingVersion,
		entryID,
		accountKey,
		chatID,
	)
	if err != nil {
		return nil, err
	}

	nonce := make(
		[]byte,
		c.aead.NonceSize(),
	)

	if _, err := io.ReadFull(
		c.random,
		nonce,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: generate payload nonce: %w",
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ciphertext := c.aead.Seal(
		nil,
		nonce,
		plaintext,
		aad,
	)

	envelope := make(
		[]byte,
		0,
		payloadEnvelopeHeaderSize+
			len(nonce)+
			len(ciphertext),
	)

	envelope = append(
		envelope,
		payloadEnvelopeVersion,
		payloadAlgorithmXChaCha20Poly1305,
		byte(len(nonce)),
	)
	envelope = append(
		envelope,
		nonce...,
	)
	envelope = append(
		envelope,
		ciphertext...,
	)

	return envelope, nil
}

// DecryptMessage implements PayloadCipher.
//
// It validates the envelope before invoking AEAD.Open. Authentication
// failure does not reveal whether the cause was a wrong key, modified
// ciphertext, modified nonce, or mismatched associated data.
func (c *AEADCipher) DecryptMessage(
	ctx context.Context,
	entryID ID,
	accountKey string,
	chatID int64,
	envelope []byte,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if c == nil || c.aead == nil {
		return nil, fmt.Errorf(
			"%w: uninitialized cipher",
			ErrCipherInvalidKey,
		)
	}

	if len(envelope) < payloadEnvelopeHeaderSize {
		return nil, fmt.Errorf(
			"%w: envelope is shorter than header",
			ErrCipherInvalidEnvelope,
		)
	}

	version := envelope[0]
	if version != payloadEnvelopeVersion {
		return nil, fmt.Errorf(
			"%w: got %d, want %d",
			ErrCipherUnsupportedVersion,
			version,
			payloadEnvelopeVersion,
		)
	}

	algorithm := envelope[1]
	if algorithm !=
		payloadAlgorithmXChaCha20Poly1305 {
		return nil, fmt.Errorf(
			"%w: algorithm id %d",
			ErrCipherUnsupportedAlgorithm,
			algorithm,
		)
	}

	nonceLength := int(envelope[2])
	if nonceLength != c.aead.NonceSize() {
		return nil, fmt.Errorf(
			"%w: nonce length %d, want %d",
			ErrCipherInvalidEnvelope,
			nonceLength,
			c.aead.NonceSize(),
		)
	}

	nonceEnd := payloadEnvelopeHeaderSize +
		nonceLength

	if nonceEnd > len(envelope) {
		return nil, fmt.Errorf(
			"%w: truncated nonce",
			ErrCipherInvalidEnvelope,
		)
	}

	ciphertext := envelope[nonceEnd:]
	if len(ciphertext) < c.aead.Overhead() {
		return nil, fmt.Errorf(
			"%w: ciphertext length %d is smaller than authentication tag %d",
			ErrCipherInvalidEnvelope,
			len(ciphertext),
			c.aead.Overhead(),
		)
	}

	nonce := envelope[payloadEnvelopeHeaderSize:nonceEnd]

	aad, err := encodePayloadAssociatedData(
		c.bindingVersion,
		entryID,
		accountKey,
		chatID,
	)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	plaintext, err := c.aead.Open(
		nil,
		nonce,
		ciphertext,
		aad,
	)
	if err != nil {
		return nil, ErrCipherAuthentication
	}

	if err := ctx.Err(); err != nil {
		// Decryption has completed, but the caller canceled the
		// operation before the result was returned. Do not expose the
		// plaintext to a canceled operation.
		clear(plaintext)
		return nil, err
	}

	return plaintext, nil
}

// encodePayloadAssociatedData builds an unambiguous binary
// representation of the fields bound to the ciphertext.
//
// Layout:
//
//	AAD version              1 byte
//	payload binding version  4 bytes, big-endian
//	entry ID length          4 bytes, big-endian
//	entry ID                 variable
//	account key length       4 bytes, big-endian
//	account key              variable
//	chat ID                  8 bytes, big-endian signed representation
//
// bindingVersion is the payload binding version, not the SQLite schema
// version: ordinary migrations must not invalidate existing encrypted
// payloads.
func encodePayloadAssociatedData(
	bindingVersion uint32,
	entryID ID,
	accountKey string,
	chatID int64,
) ([]byte, error) {
	entryIDBytes := []byte(entryID)
	accountKeyBytes := []byte(accountKey)

	if uint64(len(entryIDBytes)) >
		uint64(math.MaxUint32) {
		return nil, fmt.Errorf(
			"%w: entry ID",
			ErrCipherAssociatedDataTooLarge,
		)
	}

	if uint64(len(accountKeyBytes)) >
		uint64(math.MaxUint32) {
		return nil, fmt.Errorf(
			"%w: account key",
			ErrCipherAssociatedDataTooLarge,
		)
	}

	var buffer bytes.Buffer

	// Preallocate the exact encoded size:
	//
	// 1 byte AAD version
	// 4 bytes payload binding version
	// 4 bytes entry ID length
	// entry ID bytes
	// 4 bytes account key length
	// account key bytes
	// 8 bytes chat ID
	buffer.Grow(
		1 +
			4 +
			4 +
			len(entryIDBytes) +
			4 +
			len(accountKeyBytes) +
			8,
	)

	if err := buffer.WriteByte(
		payloadAADVersion,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: encode AAD version: %w",
			err,
		)
	}

	if err := binary.Write(
		&buffer,
		binary.BigEndian,
		bindingVersion,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: encode payload binding version: %w",
			err,
		)
	}

	if err := writeLengthPrefixedAADField(
		&buffer,
		entryIDBytes,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: encode entry ID: %w",
			err,
		)
	}

	if err := writeLengthPrefixedAADField(
		&buffer,
		accountKeyBytes,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: encode account key: %w",
			err,
		)
	}

	if err := binary.Write(
		&buffer,
		binary.BigEndian,
		chatID,
	); err != nil {
		return nil, fmt.Errorf(
			"outbox: encode chat ID: %w",
			err,
		)
	}

	return buffer.Bytes(), nil
}

func writeLengthPrefixedAADField(
	buffer *bytes.Buffer,
	value []byte,
) error {
	if uint64(len(value)) >
		uint64(math.MaxUint32) {
		return ErrCipherAssociatedDataTooLarge
	}

	if err := binary.Write(
		buffer,
		binary.BigEndian,
		uint32(len(value)),
	); err != nil {
		return err
	}

	if _, err := buffer.Write(value); err != nil {
		return err
	}

	return nil
}

// Compile-time assertion.
var _ PayloadCipher = (*AEADCipher)(nil)

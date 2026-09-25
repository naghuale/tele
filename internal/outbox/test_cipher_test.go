package outbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

// fakeCipher is a deterministic, non-cryptographic PayloadCipher used
// by tests.
//
// It is intentionally not secure: it exists so the SQLite store can be
// tested end-to-end without real AEAD. Its output does not contain the
// plaintext as a substring, which lets tests assert that the store is
// not writing plaintext by accident.
//
// The envelope is:
//
//	magic     1 byte  (0xFA)
//	keylen    1 byte
//	key       keylen bytes
//	body      plaintext[i] XOR key[i % keylen]
//
// The key is derived deterministically from the entry's associated
// data, so tampering with the entry identity changes the key and
// breaks the round-trip.
type fakeCipher struct {
	// failOnEncrypt forces EncryptMessage to fail.
	failOnEncrypt error

	// failOnDecrypt forces DecryptMessage to fail.
	failOnDecrypt error
}

func (c *fakeCipher) EncryptMessage(
	_ context.Context,
	entryID ID,
	accountKey string,
	chatID int64,
	plaintext []byte,
) ([]byte, error) {
	if c.failOnEncrypt != nil {
		return nil, c.failOnEncrypt
	}

	key := fakeCipherKey(entryID, accountKey, chatID)

	out := make([]byte, 0, 2+len(key)+len(plaintext))
	out = append(out, 0xFA, byte(len(key)))
	out = append(out, key...)
	for i, b := range plaintext {
		out = append(out, b^key[i%len(key)])
	}
	return out, nil
}

func (c *fakeCipher) DecryptMessage(
	_ context.Context,
	entryID ID,
	accountKey string,
	chatID int64,
	ciphertext []byte,
) ([]byte, error) {
	if c.failOnDecrypt != nil {
		return nil, c.failOnDecrypt
	}

	if len(ciphertext) < 2 || ciphertext[0] != 0xFA {
		return nil, errors.New("fake cipher: bad envelope magic")
	}
	keyLen := int(ciphertext[1])
	if len(ciphertext) < 2+keyLen {
		return nil, errors.New("fake cipher: truncated envelope")
	}
	envelopeKey := ciphertext[2 : 2+keyLen]
	expectedKey := fakeCipherKey(entryID, accountKey, chatID)
	if len(envelopeKey) != len(expectedKey) {
		return nil, errors.New("fake cipher: key length mismatch")
	}
	for i := range envelopeKey {
		if envelopeKey[i] != expectedKey[i] {
			return nil, errors.New(
				"fake cipher: envelope key does not match entry",
			)
		}
	}

	body := ciphertext[2+keyLen:]
	plaintext := make([]byte, len(body))
	for i, b := range body {
		plaintext[i] = b ^ expectedKey[i%len(expectedKey)]
	}
	return plaintext, nil
}

// fakeCipherKey derives a stable key from the entry's associated
// data. Any change to entry ID, account key, or chat ID changes the
// key and breaks authentication.
func fakeCipherKey(entryID ID, accountKey string, chatID int64) []byte {
	raw := fmt.Sprintf("%s|%s|%d", entryID, accountKey, chatID)
	sum := sha256.Sum256([]byte(raw))
	return sum[:16]
}

// Compile-time assertion.
var _ PayloadCipher = (*fakeCipher)(nil)

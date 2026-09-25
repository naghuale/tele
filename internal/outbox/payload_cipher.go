package outbox

import "context"

// PayloadCipher encrypts and decrypts user-controlled payload fields
// before they are passed to a durable Store.
//
// Associated data binds the ciphertext to its outbox entry, so an
// envelope cannot be silently relocated to another entry.
//
// PR-08C1 ships only the interface and an in-memory test double. The
// real AEAD implementation and its KeyProvider are PR-08C2 and
// PR-08C3.
type PayloadCipher interface {
	EncryptMessage(
		ctx context.Context,
		entryID ID,
		accountKey string,
		chatID int64,
		plaintext []byte,
	) ([]byte, error)

	DecryptMessage(
		ctx context.Context,
		entryID ID,
		accountKey string,
		chatID int64,
		ciphertext []byte,
	) ([]byte, error)
}

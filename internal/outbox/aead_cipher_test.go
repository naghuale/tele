package outbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"math"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func newTestCipher(t *testing.T) PayloadCipher {
	t.Helper()
	c, err := NewAEADCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewAEADCipher: %v", err)
	}
	return c
}

// ---- Constructor ----

func TestNewAEADCipherRejectsShortKey(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31} {
		_, err := NewAEADCipher(make([]byte, size))
		if !errors.Is(err, ErrCipherInvalidKey) {
			t.Fatalf("size=%d: err = %v, want ErrCipherInvalidKey",
				size, err)
		}
	}
}

func TestNewAEADCipherRejectsLongKey(t *testing.T) {
	for _, size := range []int{33, 64} {
		_, err := NewAEADCipher(make([]byte, size))
		if !errors.Is(err, ErrCipherInvalidKey) {
			t.Fatalf("size=%d: err = %v, want ErrCipherInvalidKey",
				size, err)
		}
	}
}

func TestNewAEADCipherAcceptsExactKey(t *testing.T) {
	c, err := NewAEADCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewAEADCipher: %v", err)
	}
	if c == nil {
		t.Fatal("cipher is nil")
	}
}

func TestNewAEADCipherRejectsNilRandom(t *testing.T) {
	_, err := newAEADCipher(testKey(t), nil, payloadBindingVersion)
	if !errors.Is(err, ErrCipherInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrCipherInvalidEnvelope", err)
	}
}

// ---- Round trip ----

func TestAEADCipherRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("hello world")

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatalf("EncryptMessage: %v", err)
	}

	got, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext = %q, want %q", got, plaintext)
	}
}

func TestAEADCipherEmptyPlaintextRoundTrip(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, nil,
	)
	if err != nil {
		t.Fatalf("EncryptMessage: %v", err)
	}

	got, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("plaintext = %q, want empty", got)
	}
}

func TestAEADCipherUnicodeRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("привет мир 🌍 äöü 你好")

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatalf("EncryptMessage: %v", err)
	}

	got, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch")
	}
}

func TestAEADCipherNegativeChatIDRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("hello")

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", -1001, plaintext,
	)
	if err != nil {
		t.Fatalf("EncryptMessage: %v", err)
	}

	got, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", -1001, envelope,
	)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch")
	}
}

// ---- Random nonce ----

func TestAEADCipherSamePlaintextEncryptsDifferently(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("identical input")

	first, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same plaintext are identical")
	}
}

// ---- Envelope structure ----

func TestAEADCipherEnvelopeStructure(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("hello")

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(envelope) < payloadEnvelopeHeaderSize {
		t.Fatalf("envelope too short: %d", len(envelope))
	}
	if envelope[0] != payloadEnvelopeVersion {
		t.Fatalf("version = %d, want %d",
			envelope[0], payloadEnvelopeVersion)
	}
	if envelope[1] != payloadAlgorithmXChaCha20Poly1305 {
		t.Fatalf("algorithm = %d, want %d",
			envelope[1], payloadAlgorithmXChaCha20Poly1305)
	}
	if envelope[2] != 24 {
		t.Fatalf("nonce length = %d, want 24", envelope[2])
	}

	// header + nonce + plaintext + tag
	wantLen := payloadEnvelopeHeaderSize + 24 + len(plaintext) + 16
	if len(envelope) != wantLen {
		t.Fatalf("envelope length = %d, want %d", len(envelope), wantLen)
	}
}

func TestAEADCipherPlaintextAbsentFromEnvelope(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("top-secret-message-body")

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, plaintext,
	)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(envelope, plaintext) {
		t.Fatal("envelope contains plaintext as a substring")
	}
}

// ---- Authentication failures ----

func TestAEADCipherWrongKeyRejected(t *testing.T) {
	encryptor := newTestCipher(t)
	decryptor := newTestCipher(t)

	envelope, err := encryptor.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = decryptor.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

func TestAEADCipherModifiedCiphertextRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 0x01

	_, err = c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, tampered,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

func TestAEADCipherModifiedNonceRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), envelope...)
	// First nonce byte follows the 3-byte header.
	tampered[payloadEnvelopeHeaderSize] ^= 0x01

	_, err = c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, tampered,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

func TestAEADCipherDifferentEntryIDRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.DecryptMessage(
		context.Background(), "op-2", "primary", 42, envelope,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

func TestAEADCipherDifferentAccountKeyRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.DecryptMessage(
		context.Background(), "op-1", "secondary", 42, envelope,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

func TestAEADCipherDifferentChatIDRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.DecryptMessage(
		context.Background(), "op-1", "primary", 43, envelope,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

// Binding version is part of associated data and must be
// authenticated independently from the SQLite schema version.
func TestAEADCipherPayloadBindingVersionAuthenticated(t *testing.T) {
	key := testKey(t)

	encryptor, err := newAEADCipher(key, rand.Reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	decryptor, err := newAEADCipher(key, rand.Reader, 2)
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := encryptor.EncryptMessage(
		context.Background(), "op-1", "primary", 42, []byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = decryptor.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if !errors.Is(err, ErrCipherAuthentication) {
		t.Fatalf("err = %v, want ErrCipherAuthentication", err)
	}
}

// ---- Envelope validation ----

func TestAEADCipherTruncatedEnvelopeRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, trim := range []int{1, 2, 3, 10, len(envelope)} {
		if trim <= 0 || trim > len(envelope) {
			continue
		}
		truncated := envelope[:len(envelope)-trim]
		_, err := c.DecryptMessage(
			context.Background(), "op-1", "primary", 42, truncated,
		)
		if err == nil {
			t.Fatalf("trim=%d: expected error", trim)
		}
	}
}

func TestAEADCipherInvalidNonceLengthRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []byte{0, 1, 12, 23, 25, 255} {
		tampered := append([]byte(nil), envelope...)
		tampered[2] = bad

		_, err := c.DecryptMessage(
			context.Background(), "op-1", "primary", 42, tampered,
		)
		if !errors.Is(err, ErrCipherInvalidEnvelope) {
			t.Fatalf("nonceLength=%d: err = %v, want ErrCipherInvalidEnvelope",
				bad, err)
		}
	}
}

func TestAEADCipherUnknownVersionRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range []byte{0, 2, 255} {
		tampered := append([]byte(nil), envelope...)
		tampered[0] = v

		_, err := c.DecryptMessage(
			context.Background(), "op-1", "primary", 42, tampered,
		)
		if !errors.Is(err, ErrCipherUnsupportedVersion) {
			t.Fatalf("version=%d: err = %v, want ErrCipherUnsupportedVersion",
				v, err)
		}
	}
}

func TestAEADCipherUnknownAlgorithmRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, alg := range []byte{0, 2, 255} {
		tampered := append([]byte(nil), envelope...)
		tampered[1] = alg

		_, err := c.DecryptMessage(
			context.Background(), "op-1", "primary", 42, tampered,
		)
		if !errors.Is(err, ErrCipherUnsupportedAlgorithm) {
			t.Fatalf("algorithm=%d: err = %v, want ErrCipherUnsupportedAlgorithm",
				alg, err)
		}
	}
}

func TestAEADCipherHeaderOnlyEnvelopeRejected(t *testing.T) {
	c := newTestCipher(t)

	envelope := []byte{
		payloadEnvelopeVersion,
		payloadAlgorithmXChaCha20Poly1305,
		24,
	}

	_, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, envelope,
	)
	if !errors.Is(err, ErrCipherInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrCipherInvalidEnvelope", err)
	}
}

func TestAEADCipherNilEnvelopeRejected(t *testing.T) {
	c := newTestCipher(t)

	_, err := c.DecryptMessage(
		context.Background(), "op-1", "primary", 42, nil,
	)
	if !errors.Is(err, ErrCipherInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrCipherInvalidEnvelope", err)
	}
}

// ---- Context cancellation ----

func TestAEADCipherEncryptRejectsCanceledContext(t *testing.T) {
	c := newTestCipher(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.EncryptMessage(ctx, "op-1", "primary", 42, []byte("hello"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAEADCipherDecryptRejectsCanceledContext(t *testing.T) {
	c := newTestCipher(t)

	envelope, err := c.EncryptMessage(
		context.Background(), "op-1", "primary", 42,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.DecryptMessage(ctx, "op-1", "primary", 42, envelope)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// ---- Nonce source failure ----

type failingReader struct {
	err error
}

func (r failingReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestAEADCipherNonceGenerationFailure(t *testing.T) {
	nonceErr := errors.New("random source unavailable")

	c, err := newAEADCipher(
		testKey(t),
		failingReader{err: nonceErr},
		payloadBindingVersion,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.EncryptMessage(
		context.Background(), "op-1", "primary", 42, []byte("hello"),
	)
	if !errors.Is(err, nonceErr) {
		t.Fatalf("err = %v, want nonce source error", err)
	}
}

// ---- Associated data encoding ----

func TestEncodePayloadAssociatedDataDistinguishesFields(t *testing.T) {
	a, err := encodePayloadAssociatedData(
		payloadBindingVersion, "ab", "c", 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodePayloadAssociatedData(
		payloadBindingVersion, "a", "bc", 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("length-prefix encoding does not distinguish different field splits")
	}
}

func TestEncodePayloadAssociatedDataIncludesBindingVersionAndChatID(t *testing.T) {
	base, err := encodePayloadAssociatedData(
		payloadBindingVersion, "op-1", "primary", 42,
	)
	if err != nil {
		t.Fatal(err)
	}
	other, err := encodePayloadAssociatedData(
		payloadBindingVersion+1, "op-1", "primary", 42,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(base, other) {
		t.Fatal("payload binding version is not part of AAD")
	}

	other, err = encodePayloadAssociatedData(
		payloadBindingVersion, "op-1", "primary", 43,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(base, other) {
		t.Fatal("chat ID is not part of AAD")
	}
}

func TestEncodePayloadAssociatedDataAcceptsLargeEntryID(t *testing.T) {
	large := ID(strings.Repeat("x", int(math.MaxUint16)+1))

	encoded, err := encodePayloadAssociatedData(
		payloadBindingVersion,
		large,
		"primary",
		42,
	)
	if err != nil {
		t.Fatalf("encode large entry ID: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded AAD is empty")
	}
}

// ---- Interface assertion ----

func TestAEADCipherImplementsPayloadCipher(t *testing.T) {
	var _ PayloadCipher = (*AEADCipher)(nil)
}

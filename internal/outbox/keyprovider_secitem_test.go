package outbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type fakeSecItemClient struct {
	copyValue  []byte
	copyResult secItemResult
	addResult  secItemResult

	copyCalls int
	addCalls  int

	gotService string
	gotAccount string
	gotValue   []byte
}

func (c *fakeSecItemClient) CopyPassword(
	service string,
	account string,
) ([]byte, secItemResult) {
	c.copyCalls++
	c.gotService = service
	c.gotAccount = account

	return append(
		[]byte(nil),
		c.copyValue...,
	), c.copyResult
}

func (c *fakeSecItemClient) AddPassword(
	service string,
	account string,
	value []byte,
) secItemResult {
	c.addCalls++
	c.gotService = service
	c.gotAccount = account
	c.gotValue = append(
		[]byte(nil),
		value...,
	)

	return c.addResult
}

type fixedReader struct {
	value byte
}

func (r fixedReader) Read(
	buffer []byte,
) (int, error) {
	for index := range buffer {
		buffer[index] = r.value
	}

	return len(buffer), nil
}

type failingKeyReader struct {
	err error
}

func (r failingKeyReader) Read(
	_ []byte,
) (int, error) {
	return 0, r.err
}

func TestDarwinKeyProviderLoadKey(t *testing.T) {
	expected := bytes.Repeat(
		[]byte{0x42},
		outboxDataEncryptionKeySize,
	)

	client := &fakeSecItemClient{
		copyValue:  expected,
		copyResult: secItemSuccess,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{value: 0x11},
	)

	got, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if err != nil {
		t.Fatalf(
			"LoadKey: %v",
			err,
		)
	}

	if !bytes.Equal(
		got,
		expected,
	) {
		t.Fatal(
			"loaded key differs",
		)
	}

	if client.copyCalls != 1 {
		t.Fatalf(
			"copy calls = %d, want 1",
			client.copyCalls,
		)
	}

	if client.gotService !=
		keychainOutboxService {
		t.Fatalf(
			"service = %q",
			client.gotService,
		)
	}

	if client.gotAccount !=
		"database-1" {
		t.Fatalf(
			"account = %q",
			client.gotAccount,
		)
	}
}

func TestDarwinKeyProviderLoadMissing(
	t *testing.T,
) {
	client := &fakeSecItemClient{
		copyResult: secItemNotFound,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	_, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyUnavailable,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyUnavailable",
			err,
		)
	}
}

// A locked or denied Keychain is not the same as a missing key, and the
// user is told different things, so the two must not collapse into one
// sentinel.
func TestDarwinKeyProviderLoadAccessDenied(t *testing.T) {
	client := &fakeSecItemClient{
		copyResult: secItemUnavailable,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	_, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(err, ErrOutboxKeyAccessDenied) {
		t.Fatalf("error = %v, want ErrOutboxKeyAccessDenied", err)
	}
	if errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatal("a denied Keychain must not be reported as a missing key")
	}
}

func TestDarwinKeyProviderLoadNotFound(t *testing.T) {
	client := &fakeSecItemClient{
		copyResult: secItemNotFound,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	_, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatalf("error = %v, want ErrOutboxKeyUnavailable", err)
	}
	if errors.Is(err, ErrOutboxKeyAccessDenied) {
		t.Fatal("a missing key must not be reported as a denied Keychain")
	}
}

func TestDarwinKeyProviderRejectsMalformedKey(
	t *testing.T,
) {
	client := &fakeSecItemClient{
		copyValue:  []byte("too short"),
		copyResult: secItemSuccess,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	_, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyUnavailable,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyUnavailable",
			err,
		)
	}
}

func TestDarwinKeyProviderCreateKey(
	t *testing.T,
) {
	client := &fakeSecItemClient{
		addResult: secItemSuccess,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{
			value: 0x7A,
		},
	)

	key, err := provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if err != nil {
		t.Fatalf(
			"CreateKey: %v",
			err,
		)
	}

	if len(key) !=
		outboxDataEncryptionKeySize {
		t.Fatalf(
			"key length = %d",
			len(key),
		)
	}

	if !bytes.Equal(
		key,
		bytes.Repeat(
			[]byte{0x7A},
			outboxDataEncryptionKeySize,
		),
	) {
		t.Fatal(
			"unexpected generated key",
		)
	}

	if !bytes.Equal(
		client.gotValue,
		key,
	) {
		t.Fatal(
			"stored key differs",
		)
	}
}

func TestDarwinKeyProviderCreateDuplicate(
	t *testing.T,
) {
	client := &fakeSecItemClient{
		addResult: secItemDuplicate,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{
			value: 0x7A,
		},
	)

	_, err := provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyExists,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyExists",
			err,
		)
	}
}

func TestDarwinKeyProviderCreateUnavailable(
	t *testing.T,
) {
	client := &fakeSecItemClient{
		addResult: secItemUnavailable,
	}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{
			value: 0x7A,
		},
	)

	_, err := provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyCreationFailed,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyCreationFailed",
			err,
		)
	}
}

func TestDarwinKeyProviderRandomFailure(
	t *testing.T,
) {
	randomErr := errors.New(
		"random unavailable",
	)

	client := &fakeSecItemClient{
		addResult: secItemSuccess,
	}

	provider := newDarwinKeyProvider(
		client,
		failingKeyReader{
			err: randomErr,
		},
	)

	_, err := provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyCreationFailed,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyCreationFailed",
			err,
		)
	}

	if client.addCalls != 0 {
		t.Fatalf(
			"add calls = %d, want 0",
			client.addCalls,
		)
	}
}

func TestDarwinKeyProviderValidationBeforeTransport(
	t *testing.T,
) {
	client := &fakeSecItemClient{}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	if _, err := provider.LoadKey(
		context.Background(),
		" ",
	); !errors.Is(
		err,
		ErrOutboxKeyInvalidDatabaseID,
	) {
		t.Fatalf(
			"LoadKey error = %v",
			err,
		)
	}

	if _, err := provider.CreateKey(
		context.Background(),
		" ",
	); !errors.Is(
		err,
		ErrOutboxKeyInvalidDatabaseID,
	) {
		t.Fatalf(
			"CreateKey error = %v",
			err,
		)
	}

	if client.copyCalls != 0 ||
		client.addCalls != 0 {
		t.Fatal(
			"transport called for invalid database ID",
		)
	}
}

func TestDarwinKeyProviderCanceledContext(
	t *testing.T,
) {
	client := &fakeSecItemClient{}

	provider := newDarwinKeyProvider(
		client,
		fixedReader{},
	)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	if _, err := provider.LoadKey(
		ctx,
		"database-1",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"LoadKey error = %v",
			err,
		)
	}

	if _, err := provider.CreateKey(
		ctx,
		"database-1",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"CreateKey error = %v",
			err,
		)
	}

	if client.copyCalls != 0 ||
		client.addCalls != 0 {
		t.Fatal(
			"transport called for canceled context",
		)
	}
}

func TestDarwinKeyProviderNilClient(
	t *testing.T,
) {
	provider := newDarwinKeyProvider(
		nil,
		fixedReader{},
	)

	_, err := provider.LoadKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyProviderUnsupported,
	) {
		t.Fatalf(
			"LoadKey error = %v",
			err,
		)
	}

	_, err = provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyProviderUnsupported,
	) {
		t.Fatalf(
			"CreateKey error = %v",
			err,
		)
	}
}

func TestDarwinKeyProviderNilRandom(
	t *testing.T,
) {
	client := &fakeSecItemClient{}

	provider := newDarwinKeyProvider(
		client,
		nil,
	)

	_, err := provider.CreateKey(
		context.Background(),
		"database-1",
	)
	if !errors.Is(
		err,
		ErrOutboxKeyCreationFailed,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyCreationFailed",
			err,
		)
	}
}

func TestDarwinKeyProviderImplementsInterface(
	t *testing.T,
) {
	var _ KeyProvider = (*darwinKeyProvider)(nil)
	var _ secItemClient = (*fakeSecItemClient)(nil)
	var _ io.Reader = fixedReader{}
}

// A missing key is the only condition that may be reported as missing.
// Anything else risks an offered reset throwing away a working queue.
func TestDarwinKeyProviderMissingKeyComesOnlyFromNotFound(t *testing.T) {
	results := []secItemResult{
		secItemUnavailable,
		secItemFailure,
	}

	for _, result := range results {
		client := &fakeSecItemClient{copyResult: result}
		provider := newDarwinKeyProvider(client, fixedReader{})

		_, err := provider.LoadKey(context.Background(), "database-1")
		if err == nil {
			t.Fatalf("result %d: expected an error", result)
		}
		if errors.Is(err, ErrOutboxKeyUnavailable) {
			t.Fatalf(
				"result %d produced ErrOutboxKeyUnavailable; only "+
					"secItemNotFound may",
				result,
			)
		}
	}
}

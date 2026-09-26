//go:build linux

package outbox

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/godbus/dbus/v5"
)

const (
	secretServiceBusName = "org.freedesktop.secrets"

	secretServiceObjectPath dbus.ObjectPath = "/org/freedesktop/secrets"

	secretServiceInterface = "org.freedesktop.Secret.Service"

	secretCollectionInterface = "org.freedesktop.Secret.Collection"

	secretItemInterface = "org.freedesktop.Secret.Item"

	secretItemLabelProperty = "org.freedesktop.Secret.Item.Label"

	secretItemAttributesProperty = "org.freedesktop.Secret.Item.Attributes"
)

// unsupportedLinuxKeyProvider keeps durable outbox activation disabled
// when no lock directory can be created, so create-if-absent cannot be
// serialized.
type unsupportedLinuxKeyProvider struct{}

func (unsupportedLinuxKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	return nil, ErrOutboxKeyProviderUnsupported
}

func (unsupportedLinuxKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	return nil, ErrOutboxKeyProviderUnsupported
}

// NewPlatformKeyProvider returns the Linux Secret Service-backed
// provider.
//
// If the lock directory cannot be prepared, the fail-closed stub is
// returned instead: without the per-database lock, telecli cannot
// guarantee that a cooperating process will not create a second key.
func NewPlatformKeyProvider() KeyProvider {
	locks, err := NewFileLockFactory()
	if err != nil {
		return unsupportedLinuxKeyProvider{}
	}
	return newLinuxKeyProvider(
		newDBusSecretServiceClient(),
		rand.Reader,
		locks,
	)
}

type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type dbusSecretServiceClient struct {
	conn       *dbus.Conn
	session    dbus.ObjectPath
	collection dbus.ObjectPath
}

func newDBusSecretServiceClient() *dbusSecretServiceClient {
	client := &dbusSecretServiceClient{}
	connection, err := dbus.SessionBus()
	if err != nil {
		return client
	}
	client.conn = connection
	return client
}

func (c *dbusSecretServiceClient) initialize(
	ctx context.Context,
) secretServiceResult {
	if c == nil || c.conn == nil {
		return secretServiceUnavailable
	}
	if c.session != "" && c.collection != "" {
		return secretServiceSuccess
	}

	call := c.conn.BusObject().CallWithContext(
		ctx,
		"org.freedesktop.DBus.GetNameOwner",
		0,
		secretServiceBusName,
	)
	if call.Err != nil {
		return mapDBusError(call.Err)
	}

	var (
		output  dbus.Variant
		session dbus.ObjectPath
	)

	call = c.conn.Object(
		secretServiceBusName,
		secretServiceObjectPath,
	).CallWithContext(
		ctx,
		secretServiceInterface+".OpenSession",
		0,
		"plain",
		dbus.MakeVariant(""),
	)
	if call.Err != nil {
		return mapDBusError(call.Err)
	}
	if err := call.Store(&output, &session); err != nil {
		return secretServiceFailure
	}

	var collection dbus.ObjectPath

	call = c.conn.Object(
		secretServiceBusName,
		secretServiceObjectPath,
	).CallWithContext(
		ctx,
		secretServiceInterface+".ReadAlias",
		0,
		"default",
	)
	if call.Err != nil {
		return mapDBusError(call.Err)
	}
	if err := call.Store(&collection); err != nil {
		return secretServiceFailure
	}

	if session == "" || session == "/" ||
		collection == "" || collection == "/" {
		return secretServiceUnavailable
	}

	c.session = session
	c.collection = collection
	return secretServiceSuccess
}

func (c *dbusSecretServiceClient) Lookup(
	ctx context.Context,
	attributes map[string]string,
) ([]byte, secretServiceResult) {
	if err := ctx.Err(); err != nil {
		return nil, secretServiceUnavailable
	}
	if result := c.initialize(ctx); result != secretServiceSuccess {
		return nil, result
	}

	var (
		unlocked []dbus.ObjectPath
		locked   []dbus.ObjectPath
	)

	call := c.conn.Object(
		secretServiceBusName,
		secretServiceObjectPath,
	).CallWithContext(
		ctx,
		secretServiceInterface+".SearchItems",
		0,
		attributes,
	)
	if call.Err != nil {
		return nil, mapDBusError(call.Err)
	}
	if err := call.Store(&unlocked, &locked); err != nil {
		return nil, secretServiceFailure
	}

	if len(locked) > 0 {
		return nil, secretServiceUnavailable
	}
	if len(unlocked) == 0 {
		return nil, secretServiceNotFound
	}
	if len(unlocked) > 1 {
		// Duplicate items violate initialization invariants. Never
		// select one arbitrarily.
		return nil, secretServiceFailure
	}

	item := unlocked[0]

	var secrets map[dbus.ObjectPath]secretValue

	call = c.conn.Object(
		secretServiceBusName,
		secretServiceObjectPath,
	).CallWithContext(
		ctx,
		secretServiceInterface+".GetSecrets",
		0,
		[]dbus.ObjectPath{item},
		c.session,
	)
	if call.Err != nil {
		return nil, mapDBusError(call.Err)
	}
	if err := call.Store(&secrets); err != nil {
		return nil, secretServiceFailure
	}

	secret, ok := secrets[item]
	if !ok || len(secret.Value) == 0 {
		return nil, secretServiceFailure
	}

	return append([]byte(nil), secret.Value...), secretServiceSuccess
}

func (c *dbusSecretServiceClient) Store(
	ctx context.Context,
	attributes map[string]string,
	label string,
	value []byte,
) secretServiceResult {
	if err := ctx.Err(); err != nil {
		return secretServiceUnavailable
	}
	if len(value) == 0 {
		return secretServiceFailure
	}
	if result := c.initialize(ctx); result != secretServiceSuccess {
		return result
	}

	properties := map[string]dbus.Variant{
		secretItemLabelProperty: dbus.MakeVariant(label),
		secretItemAttributesProperty: dbus.MakeVariant(
			attributes,
		),
	}

	secret := secretValue{
		Session:     c.session,
		Parameters:  []byte{},
		Value:       append([]byte(nil), value...),
		ContentType: "application/octet-stream",
	}
	defer clearBytes(secret.Value)

	var (
		item   dbus.ObjectPath
		prompt dbus.ObjectPath
	)

	call := c.conn.Object(
		secretServiceBusName,
		c.collection,
	).CallWithContext(
		ctx,
		secretCollectionInterface+".CreateItem",
		0,
		properties,
		secret,
		false, // replace=false: never overwrite
	)
	if call.Err != nil {
		return mapDBusError(call.Err)
	}
	if err := call.Store(&item, &prompt); err != nil {
		return secretServiceFailure
	}

	if prompt != "" && prompt != "/" {
		// A prompt means user interaction is required. Do not launch
		// it; report unavailable.
		return secretServiceUnavailable
	}
	if item == "" || item == "/" {
		return secretServiceFailure
	}
	return secretServiceSuccess
}

func (c *dbusSecretServiceClient) Delete(
	ctx context.Context,
	attributes map[string]string,
) secretServiceResult {
	if err := ctx.Err(); err != nil {
		return secretServiceUnavailable
	}
	if result := c.initialize(ctx); result != secretServiceSuccess {
		return result
	}

	var (
		unlocked []dbus.ObjectPath
		locked   []dbus.ObjectPath
	)

	call := c.conn.Object(
		secretServiceBusName,
		secretServiceObjectPath,
	).CallWithContext(
		ctx,
		secretServiceInterface+".SearchItems",
		0,
		attributes,
	)
	if call.Err != nil {
		return mapDBusError(call.Err)
	}
	if err := call.Store(&unlocked, &locked); err != nil {
		return secretServiceFailure
	}

	if len(unlocked) == 0 && len(locked) == 0 {
		return secretServiceNotFound
	}
	if len(locked) > 0 {
		return secretServiceUnavailable
	}

	for _, item := range unlocked {
		var prompt dbus.ObjectPath

		call = c.conn.Object(
			secretServiceBusName,
			item,
		).CallWithContext(
			ctx,
			secretItemInterface+".Delete",
			0,
		)
		if call.Err != nil {
			return mapDBusError(call.Err)
		}
		if err := call.Store(&prompt); err != nil {
			return secretServiceFailure
		}
		if prompt != "" && prompt != "/" {
			return secretServiceUnavailable
		}
	}
	return secretServiceSuccess
}

func mapDBusError(err error) secretServiceResult {
	if err == nil {
		return secretServiceSuccess
	}

	var dbusError dbus.Error
	if !errors.As(err, &dbusError) {
		return secretServiceFailure
	}

	switch dbusError.Name {
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.NoReply",
		"org.freedesktop.DBus.Error.Disconnected",
		"org.freedesktop.DBus.Error.NotSupported",
		"org.freedesktop.Secret.Error.IsLocked":
		return secretServiceUnavailable
	default:
		return secretServiceFailure
	}
}

var _ KeyProvider = unsupportedLinuxKeyProvider{}
var _ secretServiceClient = (*dbusSecretServiceClient)(nil)

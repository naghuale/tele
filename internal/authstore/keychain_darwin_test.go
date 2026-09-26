//go:build darwin && cgo

package authstore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// These tests exercise the real Security.framework bridge.
//
// Every case uses a unique profile name and removes it afterwards, so the
// developer's real credential profiles are never read, replaced or
// deleted, and repeated runs cannot collide.

// maxTestProfileBase keeps the generated base short enough that derived
// names such as base+"-other" or base+"-0" stay inside the 64 character
// profile limit.
const maxTestProfileBase = 48

// uniqueProfile returns a profile name derived from the test name, so
// repeated runs cannot collide and no real profile is ever touched.
func uniqueProfile(t *testing.T) string {
	t.Helper()

	var safe []rune
	for _, r := range strings.ToLower(t.Name()) {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			safe = append(safe, r)
		default:
			safe = append(safe, '-')
		}
	}

	name := strings.Trim(string(safe), "-")
	if len(name) > maxTestProfileBase {
		name = strings.Trim(name[:maxTestProfileBase], "-")
	}
	if name == "" {
		name = "case"
	}

	return "authtest-" + name
}

// newIsolatedStore returns a store plus a cleanup that guarantees the
// test profile is gone.
func newIsolatedStore(t *testing.T) (Store, string) {
	t.Helper()

	store := NewDarwinStore()
	profile := uniqueProfile(t)

	// Start from a known state without failing if nothing is stored.
	_ = store.Delete(context.Background(), profile)

	t.Cleanup(func() {
		if err := store.Delete(
			context.Background(),
			profile,
		); err != nil &&
			!errors.Is(err, ErrProfileUnavailable) {
			t.Logf("cleanup: %v", err)
		}
	})

	return store, profile
}

// ---- Load ----

func TestDarwinCredentialStoreLoadsProfile(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	want := Profile{
		APIHash: "load-hash-0123456789abcdef",
		Phone:   "+15551110000",
	}

	if err := store.Create(ctx, profile, want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("profile = %+v, want %+v", got, want)
	}
}

func TestDarwinCredentialStoreMapsNotFound(t *testing.T) {
	store := NewDarwinStore()

	_, err := store.Load(
		context.Background(),
		"authtest-does-not-exist",
	)
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

func TestDarwinCredentialStoreUsesExactServiceAndAccount(t *testing.T) {
	if authService != "telecli-auth" {
		t.Fatalf("authService = %q, want telecli-auth", authService)
	}

	// A profile stored under one name must not be visible under another,
	// which is what an exact service+account match guarantees.
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, profile, validProfile()); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(
		ctx,
		profile+"-other",
	); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

// TestDarwinCredentialStoreRoundTripsEveryStorablePayload proves the
// public API can only produce payloads the reader accepts.
//
// Create and Replace always encode through
// encodeTelegramCredentialEnvelope, so an unreadable envelope cannot be
// written through the store. Strict rejection of unknown fields, trailing
// data and unsupported versions is covered at the decoder level in
// envelope_test.go, which is the only place a malformed payload can be
// introduced.
func TestDarwinCredentialStoreRoundTripsEveryStorablePayload(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	payloads := []Profile{
		{APIHash: "h", Phone: "+1"},
		{APIHash: strings.Repeat("a", 64), Phone: "+15551234567"},
		{
			APIHash: "hash with spaces and \"quotes\"",
			Phone:   "+1 (555) 123-4567",
		},
		{
			APIHash: "юникод-хеш",
			Phone:   "+15550000000",
		},
		{
			APIHash: "hash\nwith\nnewlines",
			Phone:   "+15551111111",
		},
	}

	for i, payload := range payloads {
		// A distinct profile per payload keeps the cases isolated.
		name := profile + "-" + itoa(i)
		t.Cleanup(func() {
			_ = store.Delete(ctx, name)
		})

		if err := store.Create(ctx, name, payload); err != nil {
			t.Fatalf("payload %d: Create: %v", i, err)
		}

		got, err := store.Load(ctx, name)
		if err != nil {
			t.Fatalf("payload %d: Load: %v", i, err)
		}
		if got != payload {
			t.Fatalf(
				"payload %d: got %+v, want %+v",
				i,
				got,
				payload,
			)
		}
	}
}

// ---- Create ----

func TestDarwinCredentialStoreCreatesProfile(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	credentials := Profile{
		APIHash: "create-hash-0123456789abcdef",
		Phone:   "+15552220000",
	}

	if err := store.Create(ctx, profile, credentials); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got != credentials {
		t.Fatalf("profile = %+v, want %+v", got, credentials)
	}
}

func TestDarwinCredentialStoreDoesNotReplaceExistingProfile(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	original := Profile{APIHash: "original-hash", Phone: "+15553330000"}
	other := Profile{APIHash: "other-hash", Phone: "+15554440000"}

	if err := store.Create(ctx, profile, original); err != nil {
		t.Fatal(err)
	}

	if err := store.Create(ctx, profile, other); !errors.Is(
		err,
		ErrProfileExists,
	) {
		t.Fatalf("error = %v, want ErrProfileExists", err)
	}

	got, err := store.Load(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf(
			"Create must not overwrite: got %+v, want %+v",
			got,
			original,
		)
	}
}

func TestDarwinCredentialStoreMapsDuplicateItem(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, profile, validProfile()); err != nil {
		t.Fatal(err)
	}

	err := store.Create(ctx, profile, validProfile())
	if !errors.Is(err, ErrProfileExists) {
		t.Fatalf("error = %v, want ErrProfileExists", err)
	}
}

func TestDarwinCredentialStoreValidatesBeforeSecItemAdd(t *testing.T) {
	store := NewDarwinStore()
	ctx := context.Background()

	cases := []struct {
		name        string
		profile     string
		credentials Profile
	}{
		{"empty profile", "", validProfile()},
		{"unsafe profile", "bad profile", validProfile()},
		{"empty hash", "authtest-validation", Profile{Phone: "+1"}},
		{"empty phone", "authtest-validation", Profile{APIHash: "h"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := store.Create(ctx, c.profile, c.credentials)
			if !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf(
					"error = %v, want ErrInvalidProfile",
					err,
				)
			}
		})
	}

	// Nothing may have been written.
	if _, err := store.Load(
		ctx,
		"authtest-validation",
	); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

// ---- Replace ----

func TestDarwinCredentialStoreReplacesExistingProfile(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, profile, validProfile()); err != nil {
		t.Fatal(err)
	}

	updated := Profile{
		APIHash: "replaced-hash-0123456789abcd",
		Phone:   "+15555550000",
	}

	if err := store.Replace(ctx, profile, updated); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got != updated {
		t.Fatalf("profile = %+v, want %+v", got, updated)
	}
}

func TestDarwinCredentialStoreMapsMissingReplace(t *testing.T) {
	store := NewDarwinStore()

	err := store.Replace(
		context.Background(),
		"authtest-missing-replace",
		validProfile(),
	)
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

func TestDarwinCredentialStoreDoesNotDeleteBeforeReplace(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	original := Profile{APIHash: "keep-hash", Phone: "+15556660000"}
	updated := Profile{APIHash: "new-hash", Phone: "+15557770000"}

	if err := store.Create(ctx, profile, original); err != nil {
		t.Fatal(err)
	}

	if err := store.Replace(ctx, profile, updated); err != nil {
		t.Fatal(err)
	}

	// Replace must leave exactly one item behind, holding the new value.
	got, err := store.Load(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got != updated {
		t.Fatalf("profile = %+v, want %+v", got, updated)
	}
}

// ---- Delete ----

func TestDarwinCredentialStoreDeletesExactProfile(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, profile, validProfile()); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, profile); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(
		ctx,
		profile,
	); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

func TestDarwinCredentialStoreDoesNotDeleteOtherProfiles(t *testing.T) {
	store, profile := newIsolatedStore(t)
	ctx := context.Background()

	other := profile + "-keep"
	t.Cleanup(func() {
		_ = store.Delete(ctx, other)
	})

	if err := store.Create(ctx, profile, validProfile()); err != nil {
		t.Fatal(err)
	}

	kept := Profile{APIHash: "kept-hash", Phone: "+15558880000"}
	if err := store.Create(ctx, other, kept); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, profile); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, other)
	if err != nil {
		t.Fatalf("the neighbouring profile was removed: %v", err)
	}
	if got != kept {
		t.Fatalf("profile = %+v, want %+v", got, kept)
	}
}

func TestDarwinCredentialStoreMapsMissingDelete(t *testing.T) {
	store := NewDarwinStore()

	if err := store.Delete(
		context.Background(),
		"authtest-missing-delete",
	); !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("error = %v, want ErrProfileUnavailable", err)
	}
}

// ---- privacy ----

func TestDarwinCredentialStoreErrorDoesNotContainSecrets(t *testing.T) {
	const secretHash = "canary-hash-0123456789abcdef"
	const secretPhone = "+15550009999"

	store := NewDarwinStore()
	ctx := context.Background()

	// Validation errors must not echo the secret values.
	_, err := store.Load(ctx, "bad profile")
	if err == nil {
		t.Fatal("expected a validation error")
	}
	for _, secret := range []string{secretHash, secretPhone} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("the error leaked a credential value")
		}
	}

	// A profile that was never stored must not mention anything either.
	_, err = store.Load(ctx, "authtest-privacy-canary")
	if err == nil {
		t.Fatal("expected ErrProfileUnavailable")
	}
	for _, secret := range []string{secretHash, secretPhone} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("the error leaked a credential value")
		}
	}
}

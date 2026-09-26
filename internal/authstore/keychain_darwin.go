//go:build darwin && cgo

package authstore

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// The bridge returns an opaque status code and, for reads, a length. The
// secret bytes are copied into a malloc'd buffer that the caller owns and
// releases, so no credential ever travels through a Go string that
// outlives the call.

enum {
    TELECLI_AUTHSTORE_OK = 0,
    TELECLI_AUTHSTORE_NOT_FOUND = 1,
    TELECLI_AUTHSTORE_DUPLICATE = 2,
    TELECLI_AUTHSTORE_FAILURE = 3
};

static CFStringRef authstore_cf_string(const char *value, size_t length) {
    if (value == NULL) {
        return NULL;
    }

    return CFStringCreateWithBytes(
        kCFAllocatorDefault,
        (const UInt8 *)value,
        (CFIndex)length,
        kCFStringEncodingUTF8,
        false
    );
}

static void authstore_release_strings(
    CFStringRef service,
    CFStringRef account
) {
    if (service != NULL) {
        CFRelease(service);
    }
    if (account != NULL) {
        CFRelease(account);
    }
}

static int authstore_add(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength,
    const uint8_t *bytes,
    size_t length
) {
    CFStringRef serviceValue = authstore_cf_string(service, serviceLength);
    CFStringRef accountValue = authstore_cf_string(account, accountLength);
    CFDataRef secret = CFDataCreate(
        kCFAllocatorDefault,
        bytes,
        (CFIndex)length
    );

    if (serviceValue == NULL || accountValue == NULL || secret == NULL) {
        authstore_release_strings(serviceValue, accountValue);
        if (secret != NULL) {
            CFRelease(secret);
        }
        return TELECLI_AUTHSTORE_FAILURE;
    }

    const void *keys[] = {
        kSecClass,
        kSecAttrService,
        kSecAttrAccount,
        kSecValueData
    };
    const void *values[] = {
        kSecClassGenericPassword,
        serviceValue,
        accountValue,
        secret
    };

    CFDictionaryRef item = CFDictionaryCreate(
        kCFAllocatorDefault,
        keys,
        values,
        4,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );

    OSStatus status = TELECLI_AUTHSTORE_FAILURE;
    if (item != NULL) {
        status = SecItemAdd(item, NULL);
        CFRelease(item);
    }

    CFRelease(secret);
    authstore_release_strings(serviceValue, accountValue);

    if (status == errSecSuccess) {
        return TELECLI_AUTHSTORE_OK;
    }
    if (status == errSecDuplicateItem) {
        return TELECLI_AUTHSTORE_DUPLICATE;
    }
    return TELECLI_AUTHSTORE_FAILURE;
}

static int authstore_update(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength,
    const uint8_t *bytes,
    size_t length
) {
    CFStringRef serviceValue = authstore_cf_string(service, serviceLength);
    CFStringRef accountValue = authstore_cf_string(account, accountLength);
    CFDataRef secret = CFDataCreate(
        kCFAllocatorDefault,
        bytes,
        (CFIndex)length
    );

    if (serviceValue == NULL || accountValue == NULL || secret == NULL) {
        authstore_release_strings(serviceValue, accountValue);
        if (secret != NULL) {
            CFRelease(secret);
        }
        return TELECLI_AUTHSTORE_FAILURE;
    }

    const void *queryKeys[] = {
        kSecClass,
        kSecAttrService,
        kSecAttrAccount
    };
    const void *queryValues[] = {
        kSecClassGenericPassword,
        serviceValue,
        accountValue
    };

    CFDictionaryRef query = CFDictionaryCreate(
        kCFAllocatorDefault,
        queryKeys,
        queryValues,
        3,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );

    const void *attrKeys[] = {kSecValueData};
    const void *attrValues[] = {secret};

    CFDictionaryRef attributes = CFDictionaryCreate(
        kCFAllocatorDefault,
        attrKeys,
        attrValues,
        1,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );

    OSStatus status = TELECLI_AUTHSTORE_FAILURE;
    if (query != NULL && attributes != NULL) {
        status = SecItemUpdate(query, attributes);
    }

    if (query != NULL) {
        CFRelease(query);
    }
    if (attributes != NULL) {
        CFRelease(attributes);
    }
    CFRelease(secret);
    authstore_release_strings(serviceValue, accountValue);

    if (status == errSecSuccess) {
        return TELECLI_AUTHSTORE_OK;
    }
    if (status == errSecItemNotFound) {
        return TELECLI_AUTHSTORE_NOT_FOUND;
    }
    return TELECLI_AUTHSTORE_FAILURE;
}

static int authstore_delete(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength
) {
    CFStringRef serviceValue = authstore_cf_string(service, serviceLength);
    CFStringRef accountValue = authstore_cf_string(account, accountLength);

    if (serviceValue == NULL || accountValue == NULL) {
        authstore_release_strings(serviceValue, accountValue);
        return TELECLI_AUTHSTORE_FAILURE;
    }

    const void *keys[] = {
        kSecClass,
        kSecAttrService,
        kSecAttrAccount
    };
    const void *values[] = {
        kSecClassGenericPassword,
        serviceValue,
        accountValue
    };

    CFDictionaryRef query = CFDictionaryCreate(
        kCFAllocatorDefault,
        keys,
        values,
        3,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );

    OSStatus status = TELECLI_AUTHSTORE_FAILURE;
    if (query != NULL) {
        status = SecItemDelete(query);
        CFRelease(query);
    }

    authstore_release_strings(serviceValue, accountValue);

    if (status == errSecSuccess) {
        return TELECLI_AUTHSTORE_OK;
    }
    if (status == errSecItemNotFound) {
        return TELECLI_AUTHSTORE_NOT_FOUND;
    }
    return TELECLI_AUTHSTORE_FAILURE;
}

// authstore_read writes the secret into a freshly allocated buffer. The
// caller must release it with authstore_free. A missing item allocates
// nothing.
static int authstore_read(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength,
    uint8_t **outBytes,
    size_t *outLength
) {
    if (outBytes == NULL || outLength == NULL) {
        return TELECLI_AUTHSTORE_FAILURE;
    }

    *outBytes = NULL;
    *outLength = 0;

    CFStringRef serviceValue = authstore_cf_string(service, serviceLength);
    CFStringRef accountValue = authstore_cf_string(account, accountLength);

    if (serviceValue == NULL || accountValue == NULL) {
        authstore_release_strings(serviceValue, accountValue);
        return TELECLI_AUTHSTORE_FAILURE;
    }

    const void *keys[] = {
        kSecClass,
        kSecAttrService,
        kSecAttrAccount,
        kSecReturnData,
        kSecMatchLimit
    };
    const void *values[] = {
        kSecClassGenericPassword,
        serviceValue,
        accountValue,
        kCFBooleanTrue,
        kSecMatchLimitOne
    };

    CFDictionaryRef query = CFDictionaryCreate(
        kCFAllocatorDefault,
        keys,
        values,
        5,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );

    if (query == NULL) {
        authstore_release_strings(serviceValue, accountValue);
        return TELECLI_AUTHSTORE_FAILURE;
    }

    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    authstore_release_strings(serviceValue, accountValue);

    if (status == errSecItemNotFound) {
        return TELECLI_AUTHSTORE_NOT_FOUND;
    }

    if (status != errSecSuccess) {
        if (result != NULL) {
            CFRelease(result);
        }
        return TELECLI_AUTHSTORE_FAILURE;
    }

    if (result == NULL) {
        return TELECLI_AUTHSTORE_FAILURE;
    }

    CFDataRef secret = (CFDataRef)result;
    CFIndex length = CFDataGetLength(secret);
    if (length <= 0) {
        CFRelease(secret);
        return TELECLI_AUTHSTORE_FAILURE;
    }

    const UInt8 *source = CFDataGetBytePtr(secret);
    uint8_t *buffer = (uint8_t *)malloc((size_t)length);
    if (buffer == NULL) {
        CFRelease(secret);
        return TELECLI_AUTHSTORE_FAILURE;
    }

    memcpy(buffer, source, (size_t)length);
    CFRelease(secret);

    *outBytes = buffer;
    *outLength = (size_t)length;

    return TELECLI_AUTHSTORE_OK;
}

static void authstore_free(uint8_t *bytes, size_t length) {
    if (bytes != NULL) {
        // Overwrite before release so a freed page is less likely to
        // expose the secret through reuse. The length is passed in
        // because the payload is a JSON document, not a C string.
        if (length > 0) {
            memset(bytes, 0, length);
        }
        free(bytes);
    }
}
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"
)

// Status codes returned by the C bridge.
//
// They mirror the TELECLI_AUTHSTORE_* enum in the preamble. cgo does not
// expose enum constants here, so the values are mirrored on the Go side
// and must stay in sync with the enum.
const (
	authStoreStatusOK        = 0
	authStoreStatusNotFound  = 1
	authStoreStatusDuplicate = 2
	authStoreStatusFailure   = 3
)

// authService is the generic-password service that holds Telegram
// credential profiles.
//
// It is intentionally different from the durable outbox service so the
// two secret lifecycles stay independent.
const authService = "telecli-auth"

// darwinStore keeps one versioned envelope per credential profile.
type darwinStore struct{}

// NewDarwinStore returns a Store backed by the macOS Keychain.
func NewDarwinStore() Store {
	return darwinStore{}
}

// NewPlatformStore returns the credential store for the current build.
func NewPlatformStore() Store {
	return NewDarwinStore()
}

// Compile-time assertion: darwinStore implements Store.
var _ Store = darwinStore{}

// Load returns the profile stored for profile.
//
// Errors are safe: the OS status is mapped to a sentinel and the payload
// is never mentioned.
func (darwinStore) Load(
	_ context.Context,
	profile string,
) (Profile, error) {
	if err := validateProfileName(profile); err != nil {
		return Profile{}, err
	}

	var (
		raw    *C.uint8_t
		length C.size_t
	)

	status := C.authstore_read(
		cString(authService),
		cServiceLength(authService),
		cString(profile),
		cServiceLength(profile),
		&raw,
		&length,
	)

	switch status {
	case authStoreStatusNotFound:
		return Profile{}, ErrProfileUnavailable

	case authStoreStatusOK:
		defer C.authstore_free(raw, length)

		envelope, err := decodeTelegramCredentialEnvelope(
			C.GoBytes(unsafe.Pointer(raw), C.int(length)),
		)
		if err != nil {
			return Profile{}, err
		}

		return Profile{
			APIHash: envelope.APIHash,
			Phone:   envelope.Phone,
		}, nil

	default:
		return Profile{}, errors.Join(
			ErrStoreUnavailable,
			errors.New("read credential profile"),
		)
	}
}

// Create stores a new profile.
//
// It never overwrites: an existing item is reported as
// ErrProfileExists, so replacing is always an explicit decision.
func (darwinStore) Create(
	_ context.Context,
	profile string,
	credentials Profile,
) error {
	if err := validateProfileName(profile); err != nil {
		return err
	}

	encoded, err := encodeTelegramCredentialEnvelope(credentials)
	if err != nil {
		return err
	}

	status := C.authstore_add(
		cString(authService),
		cServiceLength(authService),
		cString(profile),
		cServiceLength(profile),
		bytesPointer(encoded),
		C.size_t(len(encoded)),
	)

	switch status {
	case authStoreStatusOK:
		return nil
	case authStoreStatusDuplicate:
		return ErrProfileExists
	default:
		return errors.Join(
			ErrStoreUnavailable,
			errors.New("create credential profile"),
		)
	}
}

// Replace overwrites an existing profile.
//
// It uses a single SecItemUpdate. A delete-then-add sequence could lose
// the profile if the process died between the two calls, so it is not
// used here.
func (darwinStore) Replace(
	_ context.Context,
	profile string,
	credentials Profile,
) error {
	if err := validateProfileName(profile); err != nil {
		return err
	}

	encoded, err := encodeTelegramCredentialEnvelope(credentials)
	if err != nil {
		return err
	}

	status := C.authstore_update(
		cString(authService),
		cServiceLength(authService),
		cString(profile),
		cServiceLength(profile),
		bytesPointer(encoded),
		C.size_t(len(encoded)),
	)

	switch status {
	case authStoreStatusOK:
		return nil
	case authStoreStatusNotFound:
		return ErrProfileUnavailable
	default:
		return errors.Join(
			ErrStoreUnavailable,
			errors.New("replace credential profile"),
		)
	}
}

// Delete removes exactly one profile.
func (darwinStore) Delete(
	_ context.Context,
	profile string,
) error {
	if err := validateProfileName(profile); err != nil {
		return err
	}

	status := C.authstore_delete(
		cString(authService),
		cServiceLength(authService),
		cString(profile),
		cServiceLength(profile),
	)

	switch status {
	case authStoreStatusOK:
		return nil
	case authStoreStatusNotFound:
		return ErrProfileUnavailable
	default:
		return errors.Join(
			ErrStoreUnavailable,
			errors.New("delete credential profile"),
		)
	}
}

// cString returns a NUL-terminated copy of value.
//
// The C copies are only used to compute lengths and to keep the pointer
// valid for the duration of a single call.
func cString(value string) *C.char {
	return C.CString(value)
}

// cServiceLength returns the byte length of value without the
// terminator.
func cServiceLength(value string) C.size_t {
	return C.size_t(len(value))
}

// bytesPointer returns a pointer to the first byte of data.
//
// The Go slice stays reachable across the C call, so the pointer remains
// valid for its duration.
func bytesPointer(data []byte) *C.uint8_t {
	if len(data) == 0 {
		return nil
	}

	return (*C.uint8_t)(unsafe.Pointer(&data[0]))
}

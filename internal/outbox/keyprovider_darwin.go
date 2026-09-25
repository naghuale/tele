//go:build darwin && cgo

package outbox

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum {
    TELECLI_SECITEM_SUCCESS = 0,
    TELECLI_SECITEM_NOT_FOUND = 1,
    TELECLI_SECITEM_DUPLICATE = 2,
    TELECLI_SECITEM_UNAVAILABLE = 3,
    TELECLI_SECITEM_FAILURE = 4
};

static int telecli_map_load_status(OSStatus status) {
    if (status == errSecSuccess) {
        return TELECLI_SECITEM_SUCCESS;
    }
    if (status == errSecItemNotFound) {
        return TELECLI_SECITEM_NOT_FOUND;
    }
    if (status == errSecInteractionNotAllowed ||
        status == errSecAuthFailed ||
        status == errSecNotAvailable) {
        return TELECLI_SECITEM_UNAVAILABLE;
    }
    return TELECLI_SECITEM_FAILURE;
}

static int telecli_map_add_status(OSStatus status) {
    if (status == errSecSuccess) {
        return TELECLI_SECITEM_SUCCESS;
    }
    if (status == errSecDuplicateItem) {
        return TELECLI_SECITEM_DUPLICATE;
    }
    if (status == errSecInteractionNotAllowed ||
        status == errSecAuthFailed ||
        status == errSecNotAvailable) {
        return TELECLI_SECITEM_UNAVAILABLE;
    }
    return TELECLI_SECITEM_FAILURE;
}

static CFStringRef telecli_cf_string(
    const char *value,
    size_t length
) {
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

static int telecli_secitem_copy(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength,
    void **outData,
    size_t *outLength
) {
    if (outData == NULL || outLength == NULL) {
        return TELECLI_SECITEM_FAILURE;
    }

    *outData = NULL;
    *outLength = 0;

    CFStringRef serviceValue = telecli_cf_string(
        service,
        serviceLength
    );
    CFStringRef accountValue = telecli_cf_string(
        account,
        accountLength
    );

    if (serviceValue == NULL || accountValue == NULL) {
        if (serviceValue != NULL) {
            CFRelease(serviceValue);
        }
        if (accountValue != NULL) {
            CFRelease(accountValue);
        }
        return TELECLI_SECITEM_FAILURE;
    }

    CFMutableDictionaryRef query =
        CFDictionaryCreateMutable(
            kCFAllocatorDefault,
            0,
            &kCFTypeDictionaryKeyCallBacks,
            &kCFTypeDictionaryValueCallBacks
        );

    if (query == NULL) {
        CFRelease(serviceValue);
        CFRelease(accountValue);
        return TELECLI_SECITEM_FAILURE;
    }

    CFDictionarySetValue(
        query,
        kSecClass,
        kSecClassGenericPassword
    );
    CFDictionarySetValue(
        query,
        kSecAttrService,
        serviceValue
    );
    CFDictionarySetValue(
        query,
        kSecAttrAccount,
        accountValue
    );
    CFDictionarySetValue(
        query,
        kSecReturnData,
        kCFBooleanTrue
    );
    CFDictionarySetValue(
        query,
        kSecMatchLimit,
        kSecMatchLimitOne
    );

    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(
        query,
        &result
    );

    int mapped = telecli_map_load_status(status);

    if (status == errSecSuccess) {
        if (result == NULL ||
            CFGetTypeID(result) != CFDataGetTypeID()) {
            mapped = TELECLI_SECITEM_FAILURE;
        } else {
            CFDataRef data = (CFDataRef)result;
            CFIndex length = CFDataGetLength(data);

            if (length < 0) {
                mapped = TELECLI_SECITEM_FAILURE;
            } else if (length > 0) {
                void *copy = malloc((size_t)length);
                if (copy == NULL) {
                    mapped = TELECLI_SECITEM_FAILURE;
                } else {
                    CFDataGetBytes(
                        data,
                        CFRangeMake(0, length),
                        (UInt8 *)copy
                    );
                    *outData = copy;
                    *outLength = (size_t)length;
                }
            }
        }
    }

    if (result != NULL) {
        CFRelease(result);
    }
    CFRelease(query);
    CFRelease(serviceValue);
    CFRelease(accountValue);

    return mapped;
}

static int telecli_secitem_add(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength,
    const void *data,
    size_t dataLength
) {
    if (data == NULL || dataLength == 0) {
        return TELECLI_SECITEM_FAILURE;
    }

    CFStringRef serviceValue = telecli_cf_string(
        service,
        serviceLength
    );
    CFStringRef accountValue = telecli_cf_string(
        account,
        accountLength
    );

    CFDataRef secretValue = CFDataCreate(
        kCFAllocatorDefault,
        (const UInt8 *)data,
        (CFIndex)dataLength
    );

    if (serviceValue == NULL ||
        accountValue == NULL ||
        secretValue == NULL) {
        if (serviceValue != NULL) {
            CFRelease(serviceValue);
        }
        if (accountValue != NULL) {
            CFRelease(accountValue);
        }
        if (secretValue != NULL) {
            CFRelease(secretValue);
        }
        return TELECLI_SECITEM_FAILURE;
    }

    CFMutableDictionaryRef attributes =
        CFDictionaryCreateMutable(
            kCFAllocatorDefault,
            0,
            &kCFTypeDictionaryKeyCallBacks,
            &kCFTypeDictionaryValueCallBacks
        );

    if (attributes == NULL) {
        CFRelease(serviceValue);
        CFRelease(accountValue);
        CFRelease(secretValue);
        return TELECLI_SECITEM_FAILURE;
    }

    CFDictionarySetValue(
        attributes,
        kSecClass,
        kSecClassGenericPassword
    );
    CFDictionarySetValue(
        attributes,
        kSecAttrService,
        serviceValue
    );
    CFDictionarySetValue(
        attributes,
        kSecAttrAccount,
        accountValue
    );
    CFDictionarySetValue(
        attributes,
        kSecValueData,
        secretValue
    );

    OSStatus status = SecItemAdd(
        attributes,
        NULL
    );

    int mapped = telecli_map_add_status(status);

    CFRelease(attributes);
    CFRelease(serviceValue);
    CFRelease(accountValue);
    CFRelease(secretValue);

    return mapped;
}

static int telecli_secitem_delete(
    const char *service,
    size_t serviceLength,
    const char *account,
    size_t accountLength
) {
    CFStringRef serviceValue = telecli_cf_string(
        service,
        serviceLength
    );
    CFStringRef accountValue = telecli_cf_string(
        account,
        accountLength
    );

    if (serviceValue == NULL || accountValue == NULL) {
        if (serviceValue != NULL) {
            CFRelease(serviceValue);
        }
        if (accountValue != NULL) {
            CFRelease(accountValue);
        }
        return TELECLI_SECITEM_FAILURE;
    }

    CFMutableDictionaryRef query =
        CFDictionaryCreateMutable(
            kCFAllocatorDefault,
            0,
            &kCFTypeDictionaryKeyCallBacks,
            &kCFTypeDictionaryValueCallBacks
        );

    if (query == NULL) {
        CFRelease(serviceValue);
        CFRelease(accountValue);
        return TELECLI_SECITEM_FAILURE;
    }

    CFDictionarySetValue(
        query,
        kSecClass,
        kSecClassGenericPassword
    );
    CFDictionarySetValue(
        query,
        kSecAttrService,
        serviceValue
    );
    CFDictionarySetValue(
        query,
        kSecAttrAccount,
        accountValue
    );

    OSStatus status = SecItemDelete(query);

    int mapped;
    if (status == errSecSuccess) {
        mapped = TELECLI_SECITEM_SUCCESS;
    } else if (status == errSecItemNotFound) {
        mapped = TELECLI_SECITEM_NOT_FOUND;
    } else if (status == errSecInteractionNotAllowed ||
               status == errSecAuthFailed ||
               status == errSecNotAvailable) {
        mapped = TELECLI_SECITEM_UNAVAILABLE;
    } else {
        mapped = TELECLI_SECITEM_FAILURE;
    }

    CFRelease(query);
    CFRelease(serviceValue);
    CFRelease(accountValue);

    return mapped;
}
*/
import "C"

import (
	"crypto/rand"
	"unsafe"
)

// nativeSecItemClient calls Security.framework through the C bridge.
type nativeSecItemClient struct{}

func (nativeSecItemClient) CopyPassword(
	service string,
	account string,
) ([]byte, secItemResult) {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)

	var (
		data   unsafe.Pointer
		length C.size_t
	)

	result := C.telecli_secitem_copy(
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(serviceBytes),
			),
		),
		C.size_t(len(serviceBytes)),
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(accountBytes),
			),
		),
		C.size_t(len(accountBytes)),
		&data,
		&length,
	)

	if data == nil {
		return nil, secItemResult(result)
	}
	defer C.free(data)

	value := C.GoBytes(
		data,
		C.int(length),
	)

	return value, secItemResult(result)
}

func (nativeSecItemClient) AddPassword(
	service string,
	account string,
	value []byte,
) secItemResult {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)

	if len(value) == 0 {
		return secItemFailure
	}

	result := C.telecli_secitem_add(
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(serviceBytes),
			),
		),
		C.size_t(len(serviceBytes)),
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(accountBytes),
			),
		),
		C.size_t(len(accountBytes)),
		unsafe.Pointer(
			unsafe.SliceData(value),
		),
		C.size_t(len(value)),
	)

	return secItemResult(result)
}

func (nativeSecItemClient) deletePassword(
	service string,
	account string,
) secItemResult {
	serviceBytes := []byte(service)
	accountBytes := []byte(account)

	result := C.telecli_secitem_delete(
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(serviceBytes),
			),
		),
		C.size_t(len(serviceBytes)),
		(*C.char)(
			unsafe.Pointer(
				unsafe.SliceData(accountBytes),
			),
		),
		C.size_t(len(accountBytes)),
	)

	return secItemResult(result)
}

// NewPlatformKeyProvider returns the macOS Keychain provider.
func NewPlatformKeyProvider() KeyProvider {
	return newDarwinKeyProvider(
		nativeSecItemClient{},
		rand.Reader,
	)
}

var _ secItemClient = nativeSecItemClient{}

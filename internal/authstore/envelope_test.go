package authstore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{
		APIHash: "0123456789abcdef0123456789abcdef",
		Phone:   "+15551234567",
	}
}

// ---- envelope ----

func TestTelegramCredentialEnvelopeRoundTrip(t *testing.T) {
	credentials := validProfile()

	encoded, err := encodeTelegramCredentialEnvelope(credentials)
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := decodeTelegramCredentialEnvelope(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if envelope.Version != telegramCredentialEnvelopeVersion {
		t.Fatalf("version = %d", envelope.Version)
	}
	if envelope.APIHash != credentials.APIHash {
		t.Fatal("the API hash did not survive the round trip")
	}
	if envelope.Phone != credentials.Phone {
		t.Fatal("the phone did not survive the round trip")
	}
}

func TestTelegramCredentialEnvelopeRejectsUnknownField(t *testing.T) {
	raw := `{"version":1,"api_hash":"h","phone":"+1","extra":"x"}`

	_, err := decodeTelegramCredentialEnvelope([]byte(raw))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("error = %v, want ErrInvalidProfile", err)
	}
}

func TestTelegramCredentialEnvelopeRejectsUnknownVersion(t *testing.T) {
	for _, version := range []int{0, 2, 99, -1} {
		raw := `{"version":` + itoa(version) +
			`,"api_hash":"h","phone":"+1"}`

		_, err := decodeTelegramCredentialEnvelope([]byte(raw))
		if !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf(
				"version %d: error = %v, want ErrInvalidProfile",
				version,
				err,
			)
		}
	}
}

func TestTelegramCredentialEnvelopeRejectsTrailingData(t *testing.T) {
	cases := []string{
		`{"version":1,"api_hash":"h","phone":"+1"} {"extra":1}`,
		`{"version":1,"api_hash":"h","phone":"+1"}{}`,
		`{"version":1,"api_hash":"h","phone":"+1"}garbage`,
	}

	for _, raw := range cases {
		if _, err := decodeTelegramCredentialEnvelope(
			[]byte(raw),
		); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf(
				"raw = %q: error = %v, want ErrInvalidProfile",
				raw,
				err,
			)
		}
	}
}

func TestTelegramCredentialEnvelopeRejectsEmptyHash(t *testing.T) {
	raw := `{"version":1,"api_hash":"","phone":"+1"}`

	_, err := decodeTelegramCredentialEnvelope([]byte(raw))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("error = %v, want ErrInvalidProfile", err)
	}
}

func TestTelegramCredentialEnvelopeRejectsEmptyPhone(t *testing.T) {
	raw := `{"version":1,"api_hash":"h","phone":""}`

	_, err := decodeTelegramCredentialEnvelope([]byte(raw))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("error = %v, want ErrInvalidProfile", err)
	}
}

func TestTelegramCredentialEnvelopeRejectsEmptyPayload(t *testing.T) {
	if _, err := decodeTelegramCredentialEnvelope(nil); !errors.Is(
		err,
		ErrInvalidProfile,
	) {
		t.Fatalf("error = %v, want ErrInvalidProfile", err)
	}
}

func TestTelegramCredentialEnvelopeRejectsMalformedJSON(t *testing.T) {
	for _, raw := range []string{
		`{`,
		`not json`,
		`{"version":"one","api_hash":"h","phone":"+1"}`,
	} {
		if _, err := decodeTelegramCredentialEnvelope(
			[]byte(raw),
		); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf(
				"raw = %q: error = %v, want ErrInvalidProfile",
				raw,
				err,
			)
		}
	}
}

// TestTelegramCredentialEnvelopeDoesNotExposeDataInError pins that a
// malformed payload never reaches an error string.
func TestTelegramCredentialEnvelopeDoesNotExposeDataInError(t *testing.T) {
	const secretHash = "leak-canary-hash-0123456789"
	const secretPhone = "+15550009999"

	cases := []string{
		`{"version":1,"api_hash":"` + secretHash +
			`","phone":"` + secretPhone + `","extra":1}`,
		`{"version":9,"api_hash":"` + secretHash +
			`","phone":"` + secretPhone + `"}`,
		`{"version":1,"api_hash":"` + secretHash +
			`","phone":"` + secretPhone + `"}trailing`,
		`{"version":1,"api_hash":"","phone":"` + secretPhone + `"}`,
	}

	for _, raw := range cases {
		_, err := decodeTelegramCredentialEnvelope([]byte(raw))
		if err == nil {
			t.Fatalf("raw = %q: expected an error", raw)
		}
		for _, secret := range []string{secretHash, secretPhone} {
			if strings.Contains(err.Error(), secret) {
				t.Fatal("the error leaked a credential value")
			}
		}
	}
}

func TestTelegramCredentialEnvelopeEncodeRejectsIncompleteProfile(t *testing.T) {
	cases := []Profile{
		{Phone: "+15551234567"},
		{APIHash: "h"},
		{},
	}

	for _, credentials := range cases {
		if _, err := encodeTelegramCredentialEnvelope(
			credentials,
		); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf(
				"credentials = %+v: error = %v, want ErrInvalidProfile",
				credentials,
				err,
			)
		}
	}
}

// TestTelegramCredentialEnvelopeEncodedShapeIsStable guards the wire
// format: an existing stored profile must stay readable.
func TestTelegramCredentialEnvelopeEncodedShapeIsStable(t *testing.T) {
	encoded, err := encodeTelegramCredentialEnvelope(validProfile())
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	want := []string{"api_hash", "phone", "version"}
	if len(decoded) != len(want) {
		t.Fatalf("envelope has %d fields, want %d", len(decoded), len(want))
	}
	for _, field := range want {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("envelope is missing %q", field)
		}
	}
	if decoded["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", decoded["version"])
	}
}

// ---- profile name validation ----

func TestTelegramCredentialProfileAcceptsSafeNames(t *testing.T) {
	for _, name := range []string{
		"default",
		"Default",
		"profile-1",
		"profile_1",
		"profile.1",
		"a",
		// A leading dash is allowed by the documented character set;
		// the name is a Keychain account, not a CLI argument.
		"--profile",
		strings.Repeat("a", 64),
	} {
		if err := validateProfileName(name); err != nil {
			t.Fatalf("name = %q: unexpected error %v", name, err)
		}
	}
}

func TestTelegramCredentialProfileRejectsUnsafeNames(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"single space", " "},
		{"embedded space", "my profile"},
		{"tab", "my\tprofile"},
		{"newline", "my\nprofile"},
		{"null byte", "my\x00profile"},
		{"control character", "my\x01profile"},
		{"forward slash", "team/profile"},
		{"backslash", "team\\profile"},
		{"path traversal", "../../etc/passwd"},
		{"overlong", strings.Repeat("a", 65)},
		{"unicode", "профиль"},
		{"emoji", "profile-\U0001F510"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateProfileName(c.value)
			if !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf(
					"value = %q: error = %v, want ErrInvalidProfile",
					c.value,
					err,
				)
			}
		})
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}

	negative := v < 0
	if negative {
		v = -v
	}

	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}

	if negative {
		return "-" + string(digits)
	}

	return string(digits)
}

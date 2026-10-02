package tui

import "strings"

// What a locale says about the hour it writes.
//
// The table is here and not in a platform file because it is the same table
// everywhere a locale is the answer, and because a test has to be able to ask
// it a question without being on the machine whose locale it is about.
//
// A locale name is `language_TERRITORY` with an optional codeset and an
// optional modifier, and the territory decides where the language does not:
// `en_US` is twelve hours and `en_GB` is twenty-four, and a table that stopped
// at the language would get one of those two wrong. So the territory is read
// first, the language second, and a locale that is neither is twelve hours.
func writesTwentyFourHours(locale string) bool {
	language, territory := splitLocale(locale)

	if territory != "" {
		if twentyFour, known := twentyFourHourTerritories[territory]; known {
			return twentyFour
		}
	}

	if twentyFour, known := twentyFourHourLanguages[language]; known {
		return twentyFour
	}

	return false
}

// twentyFourHourTerritories is what the territory of a locale decides, and
// `known` says whether this table has an opinion about it at all: a
// territory that is not here falls through to the language of the locale,
// which is the answer for a territory nobody has listed.
var twentyFourHourTerritories = map[string]bool{
	// Twenty-four: most of Europe, and the places whose language writes it.
	"GB": true, "IE": true, "DE": true, "FR": true, "ES": true, "IT": true,
	"PT": true, "NL": true, "BE": true, "AT": true, "CH": true, "DK": true,
	"SE": true, "NO": true, "FI": true, "PL": true, "CZ": true, "SK": true,
	"HU": true, "RO": true, "BG": true, "GR": true, "TR": true, "RU": true,
	"UA": true, "KZ": true, "BY": true, "HR": true, "RS": true, "SI": true,
	"LT": true, "LV": true, "EE": true, "IS": true, "LU": true, "MT": true,
	"CY": true, "AL": true, "ME": true, "MK": true, "BA": true, "GE": true,
	"AM": true, "AZ": true, "UZ": true, "MN": true,

	// Twelve: the rest of the world, named because a table that listed only
	// the twenty-four ones would be a table of everything else by omission,
	// and the day it was wrong about would be on a machine whose owner had
	// set nothing at all.
	"US": false, "CA": false, "AU": false, "NZ": false, "PH": false,
	"IN": false, "ZA": false, "EG": false, "SA": false, "AE": false,
	"IL": false, "HK": false, "ID": false, "MY": false, "MX": false,
	"BR": false, "CO": false, "PE": false, "AR": false,
}

// twentyFourHourLanguages is what the language of a locale decides when its
// territory is unknown or absent: `de` and `fr` are twenty-four wherever
// they are, and `en` and `ja` are twelve wherever they are.
var twentyFourHourLanguages = map[string]bool{
	"de": true, "fr": true, "es": true, "it": true, "pt": true, "nl": true,
	"da": true, "sv": true, "nb": true, "nn": true, "fi": true, "pl": true,
	"cs": true, "sk": true, "hu": true, "ro": true, "bg": true, "el": true,
	"tr": true, "ru": true, "uk": true, "sr": true, "hr": true, "sl": true,
	"lt": true, "lv": true, "et": true, "is": true, "sq": true, "bs": true,
	"mk": true, "ka": true, "hy": true, "az": true, "kk": true, "ky": true,
	"uz": true, "mn": true, "vi": true, "id": true, "ms": true, "gl": true,
	"eu": true, "ca": true, "sw": true, "af": true,

	"en": false, "ja": false, "zh": false, "ko": false, "ar": false,
	"he": false, "hi": false, "th": false, "fa": false, "ur": false,
	"bn": false, "ta": false, "te": false, "ml": false, "kn": false,
	"mr": false, "gu": false, "pa": false,
}

// splitLocale splits a locale name into its language and its territory,
// without the codeset and the modifier.
//
// `de_DE.UTF-8`, `de_DE@euro` and `de-DE` all name German in Germany, and a
// table that stopped at the underscore would have an answer for none of them
// except the plain form. The separator is both `_` and `-`, because macOS
// writes the first and a locale copied from elsewhere carries the second.
func splitLocale(locale string) (string, string) {
	name := locale
	if at := strings.IndexAny(name, ".@"); at >= 0 {
		name = name[:at]
	}

	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})

	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return parts[0], parts[len(parts)-1]
	}
}

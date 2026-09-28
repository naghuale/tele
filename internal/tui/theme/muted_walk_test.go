package theme

// The muted tier is a value the preset names, and this file is the proof
// that the value is the right one: it walks the same path the value was
// taken from — the dim step of the palette lifted towards the text ramp —
// and asks that the first step which clears WCAG AA on every surface a
// preview and a timestamp are drawn on is the value the preset wrote.
//
// The walk lives in the test and not in the package for the reason the
// value is a constant at all: a colour worked out at run time comes out
// of the arithmetic, and a golden file has to be the same on a Mac and on
// a Linux runner. A palette whose presets do not name the step cannot be
// checked this way, and the contrast test says so for it.

// mutedLiftSteps is how many steps from dim to the text ramp are tried.
//
// It is a fixed number rather than a search, so that the answer is the
// same in every test and on every machine — which is the whole reason the
// presets name a value rather than having one worked out for them.
const mutedLiftSteps = 16

// mutedSurfaces are the backgrounds a chat preview and a timestamp are
// drawn on: the list and the conversation.
//
// ComposerBackground is left out on purpose. The composer carries a draft
// and a placeholder, both of which the user wrote or is about to write,
// and §4.5 puts the placeholder in the muted tier on the ground that a
// placeholder is not text. The previews and the times are somebody else's
// words, and those are what the bar is for.
func mutedSurfaces(palette Palette, dark bool) []Color {
	if dark {
		return []Color{palette.Mantle, palette.Crust}
	}

	return []Color{palette.Base, palette.Mantle}
}

// readableMuted returns the dim step lifted towards the text ramp until
// it reads on every given surface.
//
// The darkest step that clears the bar wins, so a tier that is already
// readable is the one a palette was authored with. A ramp on which no step
// clears it returns the text ramp: that is the brightest step there is,
// and a muted tier that cannot be read is not a tier.
func readableMuted(dim, ramp Color, surfaces []Color) Color {
	for step := 0; step <= mutedLiftSteps; step++ {
		candidate := mixToward(dim, ramp, float64(step)/float64(mutedLiftSteps))
		if readableOnEvery(candidate, surfaces) {
			return candidate
		}
	}

	return ramp
}

// readableOnEvery reports whether a colour clears the bar on all of them.
func readableOnEvery(candidate Color, surfaces []Color) bool {
	if candidate.Kind() != ColorKindRGB {
		return false
	}

	for _, surface := range surfaces {
		if candidate.ContrastRatio(surface) < MinimumTextContrast {
			return false
		}
	}

	return true
}

// mixToward returns the colour at share of the way from one to the other.
//
// A step that is not an RGB step is returned unchanged: a palette with a
// basic colour in it has no channels to mix, and a zero result would be a
// token the theme contract test cannot see.
func mixToward(from, to Color, share float64) Color {
	if from.Kind() != ColorKindRGB || to.Kind() != ColorKindRGB {
		return from
	}
	if share <= 0 {
		return from
	}
	if share >= 1 {
		return to
	}

	return RGB(blendHex(from.hex, to.hex, share))
}

// blendHex mixes two "#rrggbb" values channel by channel.
//
// The channels are mixed in sRGB rather than in linear light on purpose: a
// ramp step is a design decision about what a colour looks like, and the
// tokens only have to end up on the right side of a contrast bar, which
// both spaces agree about at the ends of the walk.
func blendHex(from, to string, share float64) string {
	out := []byte("#000000")
	for offset := 0; offset < 3; offset++ {
		start := float64(hexDigit(from[1+offset*2])<<4 | hexDigit(from[2+offset*2]))
		end := float64(hexDigit(to[1+offset*2])<<4 | hexDigit(to[2+offset*2]))
		mixed := start + (end-start)*share

		value := int(mixed + 0.5)
		out[1+offset*2] = "0123456789abcdef"[value>>4]
		out[2+offset*2] = "0123456789abcdef"[value&0x0f]
	}

	return string(out)
}

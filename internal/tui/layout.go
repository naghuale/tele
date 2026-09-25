package tui

const (
	minWidth  = 40
	minHeight = 12
)

// visibleRange returns the [start, end) slice window of a list of total
// items such that selected is included. end is exclusive.
//
// If total <= 0 or available <= 0, returns (0, 0).
// If available >= total, returns (0, total).
// visibleRange does not mutate any input.
func visibleRange(total, selected, available int) (int, int) {
	if total <= 0 || available <= 0 {
		return 0, 0
	}
	if available >= total {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}

	half := available / 2
	start := selected - half
	if start < 0 {
		start = 0
	}
	end := start + available
	if end > total {
		end = total
		start = end - available
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// truncateRunes truncates value by rune count, not by terminal cell
// width. This is sufficient for the mock shell in PR-03; full cell-width
// handling of CJK/emoji belongs to a later UX hardening pass.
//
//   - width <= 0 returns "".
//   - If the string fits, it is returned unchanged.
//   - If truncated and width >= 2, the last rune is replaced by "…".
//   - If truncated and width == 1, only the first rune is returned.
func truncateRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

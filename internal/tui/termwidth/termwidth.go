// Package termwidth decides how many terminal columns a piece of text takes,
// and measures the terminal the program is running in when it can.
//
// It exists because the two ways of counting a width disagree, and the
// interface is drawn in columns. A grapheme cluster library counts `✌️` —
// a victory hand with the emoji selector behind it — as two columns,
// because the cluster is one glyph and the glyph is drawn twice as wide as
// a letter. The macOS Terminal draws it in one, and so does iTerm2, and so
// does every terminal that follows wcwidth rather than Unicode's emoji
// rules. A row the program believes is one column narrower than it is gets
// wrapped by the terminal, and everything under the wrap moves: the focus
// bars step sideways, rows repeat, the fragments of the last frame stay on
// the screen, and the time of a message is cut off. It is not a matter of
// taste which count is right, and the only one that can be right is the
// count the terminal is using.
//
// So the package has two rules and one measurement:
//
//   - grapheme counts a cluster the way the Unicode emoji rules say it is
//     drawn, which is what the interface counted before the measurement
//     existed and what terminals that implement the emoji rules (Ghostty,
//     WezTerm, kitty) draw;
//   - codepoint adds up the widths of the code points of a cluster the way
//     wcwidth does: a variation selector, a zero width joiner and a skin
//     tone modifier take no columns, and everything else takes what its
//     East Asian Width says, with a flag at the width the terminal gave it.
//
// Both rules cut at grapheme cluster boundaries. A width is about columns
// and a cut is about letters: half an emoji is not a narrower emoji.
//
// The measurement asks the terminal itself, with the standard cursor
// position request, before the interface draws its first frame. It is the
// only way to learn what a terminal does, and a terminal that does not
// answer is answered for by the codepoint rule, which is the one the macOS
// Terminal and iTerm2 follow.
//
// The package is a leaf. It imports the width libraries and nothing of
// telecli, and the interface reads its answers from here rather than
// measuring anything itself: a view that counted columns for itself would
// be a view deciding what the terminal can show.
package termwidth

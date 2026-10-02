package telegram

import "time"

// This file is what a Unix timestamp TDLib sends becomes.
//
// TDLib writes every moment as a count of seconds since the epoch, with no
// zone in it at all — it is an instant and nothing else. What zone it is read
// in is the reader's business, and the reader here is a person looking at a
// chat list.
//
// So no timestamp crosses this package as UTC. It used to: four of them were
// built with `.UTC()`, and the projections above formatted them where they
// stood, so every time in the interface was written in UTC. A user ten hours
// east of Greenwich saw Telegram's 07:21 as 21:21 — the owner's report of
// 29.09.2026, and the reason the owner could not tell whether telecli had the
// wrong time or the wrong day.
//
// `time.Unix` already returns the moment in the zone of the machine, and this
// helper exists so that the choice is stated in one place rather than implied
// by the absence of a method call. A reader who wants a moment in some other
// zone calls `In` on it, which is a decision somebody made; a reader who
// forgot a `UTC()` is not a decision at all.
func instantOf(seconds int64) time.Time {
	return time.Unix(seconds, 0)
}

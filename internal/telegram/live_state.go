package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ErrLiveStateOrder is returned when a TDLib position carries an order
// that is not a decimal integer.
//
// TDLib carries position.order as a JSON string of an int64, so a
// malformed value is a protocol surprise rather than a routine event.
// The store treats it as an error and leaves its state untouched.
var ErrLiveStateOrder = errors.New("telegram live state: invalid chat position order")

// LiveChat is one chat in the live main-list projection.
//
// It is a copy: the store never hands out a value a caller can use to
// mutate its own state.
type LiveChat struct {
	ID          ChatID
	Title       string
	Order       int64
	UnreadCount int
	LastMessage *Message
}

// liveChatEntry is the store's own mutable record for one chat.
//
// mainOrder and inMain are kept apart because a chat can be positioned in
// the main list with order 0, which is not a listable position.
type liveChatEntry struct {
	ID          ChatID
	Title       string
	Order       int64
	inMain      bool
	UnreadCount int
	lastMessage *Message
}

// clone returns a deep copy, so a snapshot and the store never share
// mutable memory.
func (e *liveChatEntry) clone() LiveChat {
	out := LiveChat{
		ID:          e.ID,
		Title:       e.Title,
		Order:       e.Order,
		UnreadCount: e.UnreadCount,
	}
	if e.lastMessage != nil {
		message := *e.lastMessage
		out.LastMessage = &message
	}
	return out
}

func (e *liveChatEntry) equal(other *liveChatEntry) bool {
	if e.Title != other.Title ||
		e.Order != other.Order ||
		e.inMain != other.inMain ||
		e.UnreadCount != other.UnreadCount {
		return false
	}
	switch {
	case e.lastMessage == nil && other.lastMessage == nil:
		return true
	case e.lastMessage == nil || other.lastMessage == nil:
		return false
	default:
		return *e.lastMessage == *other.lastMessage
	}
}

// listable reports whether the chat belongs in the main chat list.
//
// TDLib documents a chat with order 0, or with no chatListMain position
// at all, as not part of the main list.
func (e *liveChatEntry) listable() bool {
	return e.inMain && e.Order != 0
}

// LiveState is the in-memory projection of the main chat list that the
// session pump applies TDLib updates to.
//
// It follows ADR-0003: the pump is the only writer, consumers are told
// that the state changed rather than what changed, and a consumer reads
// the state itself. Applying an update is in-memory work only, so the
// pump never waits on a reader.
type LiveState struct {
	mu      sync.RWMutex
	chats   map[ChatID]*liveChatEntry
	changed chan struct{}
}

// NewLiveState returns an empty store.
func NewLiveState() *LiveState {
	return &LiveState{
		chats:   make(map[ChatID]*liveChatEntry),
		changed: make(chan struct{}, 1),
	}
}

// Changed returns the coalesced change signal.
//
// Capacity is one and the send is non-blocking, so a full channel already
// means "something changed since you last looked" and a dropped signal
// loses nothing. An update that does not change state does not signal.
func (l *LiveState) Changed() <-chan struct{} {
	if l == nil {
		return nil
	}
	return l.changed
}

// ChatList returns a copy of the main chat list, ordered by position
// order descending and then by chat ID descending.
//
// Chats without a chatListMain position, or whose order is 0, are
// excluded. The returned slice and the messages in it are copies and are
// not affected by later updates.
func (l *LiveState) ChatList() []LiveChat {
	if l == nil {
		return nil
	}

	l.mu.RLock()
	chats := make([]LiveChat, 0, len(l.chats))
	for _, entry := range l.chats {
		if entry.listable() {
			chats = append(chats, entry.clone())
		}
	}
	l.mu.RUnlock()

	sort.Slice(chats, func(i, j int) bool {
		if chats[i].Order != chats[j].Order {
			return chats[i].Order > chats[j].Order
		}
		return chats[i].ID > chats[j].ID
	})
	return chats
}

// signalChanged posts a change signal without blocking. The caller must
// hold the write lock.
func (l *LiveState) signalChanged() {
	select {
	case l.changed <- struct{}{}:
	default:
	}
}

// apply applies one raw TDLib update.
//
// The boolean result reports whether the update changed state. An
// unsupported update type is ignored and reports (false, nil); only a
// malformed payload or an unparsable order is an error, and an error
// leaves the state untouched.
func (l *LiveState) apply(raw RawMessage) (bool, error) {
	if l == nil {
		return false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	before := make(map[ChatID]liveChatEntry, len(l.chats))
	for id, entry := range l.chats {
		before[id] = *entry
	}

	if err := l.applyLocked(raw); err != nil {
		l.restoreLocked(before)
		return false, err
	}

	if l.unchangedLocked(before) {
		return false, nil
	}
	l.signalChanged()
	return true, nil
}

// unchangedLocked reports whether every entry still matches snapshot.
func (l *LiveState) unchangedLocked(snapshot map[ChatID]liveChatEntry) bool {
	if len(l.chats) != len(snapshot) {
		return false
	}
	for id, entry := range l.chats {
		previous, existed := snapshot[id]
		if !existed || !entry.equal(&previous) {
			return false
		}
	}
	return true
}

// restoreLocked rolls the store back to snapshot after a failed apply.
func (l *LiveState) restoreLocked(snapshot map[ChatID]liveChatEntry) {
	for id := range l.chats {
		delete(l.chats, id)
	}
	for id, previous := range snapshot {
		entry := previous
		l.chats[id] = &entry
	}
}

// ---- Update decoding ----

type updateEnvelope struct {
	Type string `json:"@type"`
}

// liveChatRaw mirrors the fields of a TDLib chat that the main list
// needs. positions is decoded separately because only the main-list
// entry matters.
type liveChatRaw struct {
	ID          int64           `json:"id"`
	Title       string          `json:"title"`
	UnreadCount int             `json:"unread_count"`
	LastMessage json.RawMessage `json:"last_message"`
	Positions   json.RawMessage `json:"positions"`
	Order       json.RawMessage `json:"order"`
}

type updateChatTitleRaw struct {
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title"`
}

type updateChatPositionRaw struct {
	ChatID   int64           `json:"chat_id"`
	Position json.RawMessage `json:"position"`
}

type updateChatLastMessageRaw struct {
	ChatID      int64           `json:"chat_id"`
	LastMessage json.RawMessage `json:"last_message"`
	Positions   json.RawMessage `json:"positions"`
}

type updateChatDraftMessageRaw struct {
	ChatID    int64           `json:"chat_id"`
	Positions json.RawMessage `json:"positions"`
}

type updateChatReadInboxRaw struct {
	ChatID      int64 `json:"chat_id"`
	UnreadCount int   `json:"unread_count"`
}

// chatPositionsRaw mirrors TDLib's chatPositions object.
type chatPositionsRaw struct {
	Positions []chatPositionEntryRaw `json:"positions"`
}

type chatPositionEntryRaw struct {
	Position chatPositionRaw `json:"position"`
	ChatID   int64           `json:"chat_id"`
}

type chatPositionRaw struct {
	Source struct {
		Type string `json:"@type"`
	} `json:"source"`
	Order json.RawMessage `json:"order"`
}

// mainChatList is the TDLib chat list source this store projects.
const mainChatList = "chatListMain"

// liveStateUpdateTypes are the update @types the main-list store
// consumes. Every other type is ignored by applyLocked.
var liveStateUpdateTypes = map[string]struct{}{
	"updateNewChat":          {},
	"updateChatTitle":        {},
	"updateChatPosition":     {},
	"updateChatLastMessage":  {},
	"updateChatDraftMessage": {},
	"updateChatReadInbox":    {},
}

// isLiveStateUpdate reports whether raw is an update the store consumes.
//
// The authorization phase uses this to decide what to hold on to. Holding
// every skipped update would grow with the message traffic of a busy
// account during a code-entry wait, while the store ignores message
// updates until a later step. Keeping only chat-list updates bounds the
// held slice by the chat list, and keeps the held data exactly the data
// the store needs.
func isLiveStateUpdate(raw RawMessage) bool {
	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	_, ok := liveStateUpdateTypes[envelope.Type]
	return ok
}

// applyLocked applies one update. The caller holds the write lock.
//
// An unknown update type is a no-op: the wire carries many objects that
// the main chat list does not model, and ignoring them keeps the pump
// free of errors for traffic it was never meant to interpret.
func (l *LiveState) applyLocked(raw RawMessage) error {
	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode live update: %w", err)
	}

	switch envelope.Type {
	case "updateNewChat":
		return l.applyNewChat(raw)
	case "updateChatTitle":
		return l.applyChatTitle(raw)
	case "updateChatPosition":
		return l.applyChatPosition(raw)
	case "updateChatLastMessage":
		return l.applyChatLastMessage(raw)
	case "updateChatDraftMessage":
		return l.applyChatDraftPositions(raw)
	case "updateChatReadInbox":
		return l.applyChatReadInbox(raw)
	default:
		return nil
	}
}

// applyNewChat records a chat, or replaces an existing record.
//
// TDLib sends updateNewChat once per chat, so a later update for a chat
// this store has never seen cannot be interpreted: a title or a position
// for an unknown chat would create a partial record that the list cannot
// use. Only updateNewChat creates entries.
func (l *LiveState) applyNewChat(raw RawMessage) error {
	var update struct {
		Chat liveChatRaw `json:"chat"`
	}
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateNewChat: %w", err)
	}
	if update.Chat.ID == 0 {
		return nil
	}

	entry := l.entryLocked(ChatID(update.Chat.ID))
	entry.Title = update.Chat.Title
	entry.UnreadCount = update.Chat.UnreadCount
	entry.lastMessage = decodeLiveMessage(update.Chat.LastMessage)

	order, inMain, err := mainPositionFromPositions(update.Chat.Positions)
	if err != nil {
		return err
	}
	// A chat object also carries its own order. Positions win when they
	// are present, because they are what decides list membership.
	if !inMain && len(update.Chat.Order) > 0 {
		if parsed, err := parsePositionOrder(update.Chat.Order); err != nil {
			return err
		} else if parsed != 0 {
			entry.inMain = true
			entry.Order = parsed
		}
	} else if inMain {
		entry.inMain = true
		entry.Order = order
	}
	return nil
}

func (l *LiveState) applyChatTitle(raw RawMessage) error {
	var update updateChatTitleRaw
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateChatTitle: %w", err)
	}
	entry := l.existingLocked(update.ChatID)
	if entry == nil {
		return nil
	}
	entry.Title = update.Title
	return nil
}

func (l *LiveState) applyChatPosition(raw RawMessage) error {
	var update updateChatPositionRaw
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateChatPosition: %w", err)
	}
	entry := l.existingLocked(update.ChatID)
	if entry == nil {
		return nil
	}

	order, inMain, err := mainPositionFromPosition(update.Position)
	if err != nil {
		return err
	}
	// A position in another list, such as the archive, removes the chat
	// from the main list.
	entry.inMain = inMain
	entry.Order = order
	return nil
}

func (l *LiveState) applyChatLastMessage(raw RawMessage) error {
	var update updateChatLastMessageRaw
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateChatLastMessage: %w", err)
	}
	entry := l.existingLocked(update.ChatID)
	if entry == nil {
		return nil
	}

	entry.lastMessage = decodeLiveMessage(update.LastMessage)
	return l.applyPositions(entry, update.Positions)
}

// applyChatDraftPositions applies only the positions of a draft update:
// the draft text itself is not part of the main list projection.
func (l *LiveState) applyChatDraftPositions(raw RawMessage) error {
	var update updateChatDraftMessageRaw
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateChatDraftMessage: %w", err)
	}
	entry := l.existingLocked(update.ChatID)
	if entry == nil {
		return nil
	}
	return l.applyPositions(entry, update.Positions)
}

func (l *LiveState) applyChatReadInbox(raw RawMessage) error {
	var update updateChatReadInboxRaw
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode updateChatReadInbox: %w", err)
	}
	entry := l.existingLocked(update.ChatID)
	if entry == nil {
		return nil
	}
	entry.UnreadCount = update.UnreadCount
	return nil
}

// applyPositions updates list membership from a chatPositions object.
func (l *LiveState) applyPositions(entry *liveChatEntry, positions json.RawMessage) error {
	order, inMain, err := mainPositionFromPositions(positions)
	if err != nil {
		return err
	}
	// An empty or absent positions object says nothing about membership,
	// so an existing position is kept.
	if !hasPositions(positions) {
		return nil
	}
	entry.inMain = inMain
	entry.Order = order
	return nil
}

// entryLocked returns the mutable record for id, creating it if needed.
// The caller holds the write lock.
func (l *LiveState) entryLocked(id ChatID) *liveChatEntry {
	if entry, exists := l.chats[id]; exists {
		return entry
	}
	entry := &liveChatEntry{ID: id}
	l.chats[id] = entry
	return entry
}

// existingLocked returns the record for id, or nil when the chat is
// unknown. A missing chat never creates an entry.
func (l *LiveState) existingLocked(chatID int64) *liveChatEntry {
	if chatID == 0 {
		return nil
	}
	return l.chats[ChatID(chatID)]
}

// isAbsentJSON reports whether a raw JSON field carries no value at all.
func isAbsentJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// hasPositions reports whether the payload carries a positions object at
// all.
func hasPositions(raw json.RawMessage) bool {
	return !isAbsentJSON(raw)
}

// mainPositionFromPositions extracts the main-list order from a
// chatPositions object.
func mainPositionFromPositions(raw json.RawMessage) (int64, bool, error) {
	if !hasPositions(raw) {
		return 0, false, nil
	}

	var positions chatPositionsRaw
	if err := json.Unmarshal(raw, &positions); err != nil {
		return 0, false, fmt.Errorf("decode chat positions: %w", err)
	}

	for _, item := range positions.Positions {
		if item.Position.Source.Type != mainChatList {
			continue
		}
		order, err := parsePositionOrder(item.Position.Order)
		if err != nil {
			return 0, false, err
		}
		return order, true, nil
	}
	return 0, false, nil
}

// mainPositionFromPosition extracts list membership from a single
// position object, as carried by updateChatPosition.
func mainPositionFromPosition(raw json.RawMessage) (int64, bool, error) {
	if !hasPositions(raw) {
		return 0, false, nil
	}

	var position chatPositionRaw
	if err := json.Unmarshal(raw, &position); err != nil {
		return 0, false, fmt.Errorf("decode chat position: %w", err)
	}
	if position.Source.Type != mainChatList {
		return 0, false, nil
	}

	order, err := parsePositionOrder(position.Order)
	if err != nil {
		return 0, false, err
	}
	return order, true, nil
}

// parsePositionOrder reads a position order, which TDLib carries as a
// JSON string of an int64.
func parsePositionOrder(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q: %w", ErrLiveStateOrder, text, err)
		}
		return parsed, nil
	}

	// Tolerate a numeric order even though TDLib documents a string.
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		parsed, err := number.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: %q: %w", ErrLiveStateOrder, number.String(), err)
		}
		return parsed, nil
	}

	return 0, fmt.Errorf("%w: %q is neither a string nor a number",
		ErrLiveStateOrder, string(raw))
}

// liveMessageRaw mirrors the fields of a TDLib message used for a
// preview. The preview text is built by parseLastMessage, which the
// snapshot path already uses, so the two paths cannot drift apart.
type liveMessageRaw struct {
	ID         int64           `json:"id"`
	ChatID     int64           `json:"chat_id"`
	IsOutgoing bool            `json:"is_outgoing"`
	Date       int64           `json:"date"`
	Content    json.RawMessage `json:"content"`
}

// decodeLiveMessage turns a raw last_message into a Message, or nil when
// there is none.
func decodeLiveMessage(raw json.RawMessage) *Message {
	if isAbsentJSON(raw) {
		return nil
	}

	var message liveMessageRaw
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil
	}
	if message.ID == 0 {
		return nil
	}

	// parseLastMessage already extracts the preview text for a message
	// payload, including the placeholder for unsupported content.
	_, text := parseLastMessage(raw)

	return &Message{
		ID:        MessageID(message.ID),
		ChatID:    ChatID(message.ChatID),
		Outgoing:  message.IsOutgoing,
		Timestamp: time.Unix(message.Date, 0).UTC(),
		Text:      text,
	}
}

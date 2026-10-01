package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrLiveStateOrder is returned when a TDLib chat position carries an
// order that is not a decimal integer.
//
// TDLib carries chatPosition.order as a JSON string of an int64, so a
// malformed value is a protocol surprise rather than a routine event.
// The store treats it as an error and leaves its state untouched.
var ErrLiveStateOrder = errors.New("telegram live state: invalid chat position order")

// The field and type names below follow the pinned TDLib schema,
// td/generate/scheme/td_api.tl at commit ea97bcdd (TDLib 1.8.67):
//
//	chatPosition list:ChatList order:int64 is_pinned:Bool source:ChatSource
//	    = ChatPosition;                                              // :3545
//	chat ... last_message:message positions:vector<chatPosition> ...;  // :3628
//	updateNewChat chat:chat = Update;                              // :10483
//	updateChatLastMessage chat_id:int53 last_message:message
//	    positions:vector<chatPosition> = Update;                    // :10507
//	updateChatPosition chat_id:int53 position:chatPosition
//	    = Update;                                                  // :10512
//	updateChatDraftMessage chat_id:int53 draft_message:draftMessage
//	    positions:vector<chatPosition> = Update;                    // :10539
//
// Two consequences are easy to get wrong and are load-bearing here:
//
//   - positions is a bare JSON array of chatPosition, not an object
//     wrapping one. There is no chatPositions type in this schema.
//   - the chat list is the position's `list` field. `source` is a
//     ChatSource (an MTProto proxy or PSA) and says nothing about chat
//     lists.
//
// The `chat` object has no order field: order lives only on a position.

// mainChatList is the TDLib chat list this store projects.
const mainChatList = "chatListMain"

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
// inMain and Order are kept apart because a chat can hold a
// chatListMain position with order 0, which TDLib documents as "needs
// to be removed from the list".
type liveChatEntry struct {
	ID          ChatID
	Title       string
	Order       int64
	inMain      bool
	UnreadCount int
	lastMessage *Message

	// chatKind, peerUserID and onlineMemberCount are what the presence of
	// the other side is read from. They are on the chat record because a
	// chat's type arrives with the chat and never changes afterwards, and
	// because a presence read that had to look the type up somewhere else
	// would be two lookups where one is enough.
	chatKind          chatKind
	peerUserID        int64
	onlineMemberCount int64
}

// listable reports whether the chat belongs in the main chat list.
func (e *liveChatEntry) listable() bool {
	return e.inMain && e.Order != 0
}

// LiveState is the in-memory projection of the main chat list and of the
// message events of every chat, that the session pump applies TDLib
// updates to.
//
// It follows ADR-0003: the pump is the only writer, consumers are told
// that the state changed rather than what changed, and a consumer reads
// the state itself. Applying an update is in-memory work only, so the
// pump never waits on a reader.
type LiveState struct {
	mu       sync.RWMutex
	chats    map[ChatID]*liveChatEntry
	messages map[ChatID]*chatMessages
	changed  chan struct{}

	// connection is what TDLib last said about the link to the servers.
	// It lives under the same lock as the chat list because it is applied
	// from the same pump, and a status read is a single read of one word.
	connection ConnectionState

	// users holds the presence of the users the store has been told about,
	// and nothing else about them: no names, no phone numbers, no
	// usernames. See live_state_presence.go.
	users map[int64]userRecord

	// access is what the store knows about writing in a chat, and
	// bySupergroup is the index that places an update about a supergroup on
	// the chat it belongs to. See live_state_access.go.
	access       map[ChatID]chatAccessFacts
	bySupergroup map[int64]ChatID
}

// NewLiveState returns an empty store.
func NewLiveState() *LiveState {
	return &LiveState{
		chats:        make(map[ChatID]*liveChatEntry),
		messages:     make(map[ChatID]*chatMessages),
		users:        make(map[int64]userRecord),
		access:       make(map[ChatID]chatAccessFacts),
		bySupergroup: make(map[int64]ChatID),
		changed:      make(chan struct{}, 1),
	}
}

// ApplyUpdate applies one raw TDLib update to the store and reports
// whether anything changed.
//
// It is the store's only entry point for a raw update, and the session
// pump goes through it so that there is exactly one place where an
// update is routed. It is exported because the send-result reconciler
// has to be proved against recorded TDLib payloads and a store that can
// only be fed from inside this package could not be: a fake window that
// a test fills directly proves the reconciler, not the chain.
//
// An update the store does not model is not an error. TDLib sends
// updates this program has no use for, and one of them must not stop the
// pump or fail a caller that only wanted the rest of the update applied.
func (l *LiveState) ApplyUpdate(raw RawMessage) (bool, error) {
	return l.apply(raw)
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
// order descending and then by chat ID descending, which is the order
// TDLib documents for chatPosition.
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
// malformed payload or an unparsable order is an error.
//
// Decoding completes before any state is touched, so an error cannot
// leave a half-applied update behind and no rollback is needed.
//
// The update types are routed by one read of the envelope. The two
// decoders keep their own handling of it on purpose: the chat-list path
// must not change under a message-event step, and one small unmarshal is
// cheaper than a decoder both paths would have to agree on.
func (l *LiveState) apply(raw RawMessage) (bool, error) {
	if l == nil {
		return false, nil
	}

	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false, fmt.Errorf("decode live update: %w", err)
	}

	// The connection state and the presence updates are the ones that are
	// not about a chat list row. They are routed before the chat-list
	// decoders, which have no place for them.
	switch envelope.Type {
	case updateConnectionStateType:
		return l.applyConnectionState(raw)

	case updateUserType:
		return l.applyUser(raw)

	case updateUserStatusType:
		return l.applyUserStatus(raw)

	case updateChatOnlineMemberCountType:
		return l.applyOnlineMemberCount(raw)

	case updateChatPermissionsType:
		return l.applyChatPermissions(raw)

	case updateSupergroupType:
		return l.applySupergroup(raw)
	}

	if _, isMessageUpdate := messageUpdateTypes[envelope.Type]; isMessageUpdate {
		update, applies, err := decodeMessageUpdate(raw)
		if err != nil {
			return false, err
		}
		if !applies {
			return false, nil
		}

		l.mu.Lock()
		defer l.mu.Unlock()

		return l.recordMessageUpdate(update), nil
	}

	patch, applies, err := decodeLivePatch(raw)
	if err != nil {
		return false, err
	}
	if !applies || patch.chatID == 0 {
		return false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.commit(patch), nil
}

// commit applies a decoded patch to a single entry.
//
// Only the affected entry is compared, so applying an update costs the
// same whether the store holds ten chats or ten thousand.
func (l *LiveState) commit(p livePatch) bool {
	before, exists := l.chats[p.chatID]
	if !exists {
		// Only updateNewChat may create a record: a title or a position
		// for an unknown chat would leave a partial entry that the list
		// cannot use, since TDLib never repeats updateNewChat.
		if !p.create {
			return false
		}
		l.chats[p.chatID] = p.entry()
		l.signalChanged()
		return true
	}

	after := p.applyTo(before)
	if after.equal(before) {
		return false
	}
	l.chats[p.chatID] = after
	l.signalChanged()
	return true
}

// livePatch is a decoded update that has not been applied yet.
//
// Keeping the decoded intent separate from the store is what allows
// commit to compare one entry instead of snapshotting the whole map.
type livePatch struct {
	chatID ChatID
	create bool

	setTitle    bool
	title       string
	setUnread   bool
	unreadCount int
	setMessage  bool
	message     *Message

	setMain bool
	inMain  bool
	order   int64

	// setChatType carries the type of a chat and the user on the other
	// side of it, which is what its presence is read from.
	setChatType bool
	chatKind    chatKind
	peerUserID  int64
}

// entry returns the patch as a fresh store record.
func (p livePatch) entry() *liveChatEntry {
	after := &liveChatEntry{ID: p.chatID}
	return p.applyTo(after)
}

// applyTo returns a copy of base with the patch's fields applied.
func (p livePatch) applyTo(base *liveChatEntry) *liveChatEntry {
	after := *base
	if p.setTitle {
		after.Title = p.title
	}
	if p.setUnread {
		after.UnreadCount = p.unreadCount
	}
	if p.setMessage {
		after.lastMessage = p.message
	}
	if p.setMain {
		after.inMain = p.inMain
		after.Order = p.order
	}
	if p.setChatType {
		after.chatKind = p.chatKind
		after.peerUserID = p.peerUserID
	}
	return &after
}

// equal compares two records by value.
func (e *liveChatEntry) equal(other *liveChatEntry) bool {
	if e.Title != other.Title ||
		e.Order != other.Order ||
		e.inMain != other.inMain ||
		e.UnreadCount != other.UnreadCount ||
		e.chatKind != other.chatKind ||
		e.peerUserID != other.peerUserID ||
		e.onlineMemberCount != other.onlineMemberCount {
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

// ---- Update decoding ----

type updateEnvelope struct {
	Type string `json:"@type"`
}

// livePatchJSON mirrors the fields of a TDLib chat that the main list
// needs.
type livePatchJSON struct {
	ID          int64           `json:"id"`
	Title       string          `json:"title"`
	UnreadCount int             `json:"unread_count"`
	LastMessage json.RawMessage `json:"last_message"`
	Positions   json.RawMessage `json:"positions"`
	Type        chatTypeJSON    `json:"type"`
}

type updateChatTitleJSON struct {
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title"`
}

type updateChatPositionJSON struct {
	ChatID   int64           `json:"chat_id"`
	Position json.RawMessage `json:"position"`
}

type updateChatLastMessageJSON struct {
	ChatID      int64           `json:"chat_id"`
	LastMessage json.RawMessage `json:"last_message"`
	Positions   json.RawMessage `json:"positions"`
}

type updateChatDraftMessageJSON struct {
	ChatID    int64           `json:"chat_id"`
	Positions json.RawMessage `json:"positions"`
}

type updateChatReadInboxJSON struct {
	ChatID      int64 `json:"chat_id"`
	UnreadCount int   `json:"unread_count"`
}

// chatPositionRaw mirrors chatPosition (td_api.tl:3545).
type chatPositionRaw struct {
	List struct {
		Type string `json:"@type"`
	} `json:"list"`
	Order    json.RawMessage `json:"order"`
	IsPinned bool            `json:"is_pinned"`
}

// isMain reports whether the position is in the main chat list.
func (p chatPositionRaw) isMain() bool {
	return p.List.Type == mainChatList
}

// liveStateUpdateTypes are the update @types the main-list store
// consumes. Every other type is ignored.
//
// The two updates about the rights of a chat are consumed by the store and
// are not in this list, and that is not an oversight: a right is recorded
// for a chat the store has been asked about, and nothing is asked before the
// login is over. Held here they would be applied to a store that holds no
// rights for the chat they are about, and dropped — while the read that
// follows the login asks TDLib, whose answer is newer than the update was.
var liveStateUpdateTypes = map[string]struct{}{
	"updateNewChat":           {},
	"updateChatTitle":         {},
	"updateChatPosition":      {},
	"updateChatLastMessage":   {},
	"updateChatDraftMessage":  {},
	"updateChatReadInbox":     {},
	updateConnectionStateType: {},
}

// The updates the store applies to something other than a chat-list row.
const (
	updateConnectionStateType       = "updateConnectionState"
	updateUserType                  = "updateUser"
	updateUserStatusType            = "updateUserStatus"
	updateChatOnlineMemberCountType = "updateChatOnlineMemberCount"
)

// isLiveStateUpdate reports whether raw is an update the store consumes.
//
// The authorization phase uses this to decide what to hold on to. Holding
// every skipped update would grow with the message traffic of a busy
// account during a code-entry wait, while the store ignores message
// updates until a later step. Keeping only chat-list updates and the
// connection state bounds the held slice by the chat list plus one word,
// and keeps the held data exactly the data the store needs: the chat list
// is rebuilt from its updates, and a connection state TDLib announced
// during the login wait is not announced again afterwards.
func isLiveStateUpdate(raw RawMessage) bool {
	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	_, ok := liveStateUpdateTypes[envelope.Type]
	return ok
}

// decodeLivePatch decodes one update into the intent it expresses.
//
// The second result is false for an update type the store does not
// consume: the wire carries many objects the main chat list does not
// model, and ignoring them keeps the pump free of errors for traffic it
// was never meant to interpret. Nothing is mutated here, so every error
// is raised before the store is involved.
func decodeLivePatch(raw RawMessage) (livePatch, bool, error) {
	var envelope updateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return livePatch{}, false, fmt.Errorf("decode live update: %w", err)
	}

	switch envelope.Type {
	case "updateNewChat":
		return decodeNewChatPatch(raw)
	case "updateChatTitle":
		return decodeChatTitlePatch(raw)
	case "updateChatPosition":
		return decodeChatPositionPatch(raw)
	case "updateChatLastMessage":
		return decodeChatLastMessagePatch(raw)
	case "updateChatDraftMessage":
		return decodeChatDraftPatch(raw)
	case "updateChatReadInbox":
		return decodeChatReadInboxPatch(raw)
	default:
		return livePatch{}, false, nil
	}
}

func decodeNewChatPatch(raw RawMessage) (livePatch, bool, error) {
	var update struct {
		Chat livePatchJSON `json:"chat"`
	}
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateNewChat: %w", err)
	}
	patch := livePatch{
		chatID:      ChatID(update.Chat.ID),
		create:      true,
		setTitle:    true,
		title:       update.Chat.Title,
		setUnread:   true,
		unreadCount: update.Chat.UnreadCount,
		setMessage:  true,
		message:     decodeLiveMessage(update.Chat.LastMessage),
	}
	if kind, userID := chatTypePatch(update.Chat.Type); kind != chatKindUnknown {
		patch.setChatType = true
		patch.chatKind = kind
		patch.peerUserID = userID
	}
	if err := patch.applyPositions(update.Chat.Positions); err != nil {
		return livePatch{}, false, err
	}
	return patch, true, nil
}

func decodeChatTitlePatch(raw RawMessage) (livePatch, bool, error) {
	var update updateChatTitleJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateChatTitle: %w", err)
	}
	return livePatch{
		chatID:   ChatID(update.ChatID),
		setTitle: true,
		title:    update.Title,
	}, true, nil
}

func decodeChatReadInboxPatch(raw RawMessage) (livePatch, bool, error) {
	var update updateChatReadInboxJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateChatReadInbox: %w", err)
	}
	return livePatch{
		chatID:      ChatID(update.ChatID),
		setUnread:   true,
		unreadCount: update.UnreadCount,
	}, true, nil
}

func decodeChatPositionPatch(raw RawMessage) (livePatch, bool, error) {
	var update updateChatPositionJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateChatPosition: %w", err)
	}
	if isAbsentJSON(update.Position) {
		return livePatch{chatID: ChatID(update.ChatID)}, true, nil
	}

	var position chatPositionRaw
	if err := json.Unmarshal(update.Position, &position); err != nil {
		return livePatch{}, false, fmt.Errorf("decode chat position: %w", err)
	}

	// updateChatPosition carries one position in one specific list.
	// TDLib documents "if new order is 0, then the chat needs to be
	// removed from the list", meaning the list named in this position. A
	// chat can sit in the main list and in folders at the same time, so a
	// position for any other list says nothing about the main list and is
	// ignored. Leaving the main list is reported by a separate
	// chatListMain position with order 0.
	if !position.isMain() {
		return livePatch{chatID: ChatID(update.ChatID)}, true, nil
	}

	order, err := parsePositionOrder(position.Order)
	if err != nil {
		return livePatch{}, false, err
	}
	return livePatch{
		chatID:  ChatID(update.ChatID),
		setMain: true,
		inMain:  true,
		order:   order,
	}, true, nil
}

func decodeChatLastMessagePatch(raw RawMessage) (livePatch, bool, error) {
	var update updateChatLastMessageJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateChatLastMessage: %w", err)
	}
	patch := livePatch{
		chatID:     ChatID(update.ChatID),
		setMessage: true,
		message:    decodeLiveMessage(update.LastMessage),
	}
	// The update carries the chat's whole position vector, so a missing
	// chatListMain entry means the chat is not in the main list.
	if err := patch.applyPositions(update.Positions); err != nil {
		return livePatch{}, false, err
	}
	return patch, true, nil
}

// decodeChatDraftPatch applies only the positions of a draft update: the
// draft text is not part of the main-list projection.
func decodeChatDraftPatch(raw RawMessage) (livePatch, bool, error) {
	var update updateChatDraftMessageJSON
	if err := json.Unmarshal(raw, &update); err != nil {
		return livePatch{}, false, fmt.Errorf("decode updateChatDraftMessage: %w", err)
	}
	patch := livePatch{chatID: ChatID(update.ChatID)}
	if err := patch.applyPositions(update.Positions); err != nil {
		return livePatch{}, false, err
	}
	return patch, true, nil
}

// applyPositions reads main-list membership from a positions vector.
func (p *livePatch) applyPositions(raw json.RawMessage) error {
	if isAbsentJSON(raw) {
		// No positions at all: the update says nothing about membership,
		// so an existing position is kept.
		return nil
	}

	var positions []chatPositionRaw
	if err := json.Unmarshal(raw, &positions); err != nil {
		return fmt.Errorf("decode chat positions: %w", err)
	}

	for _, position := range positions {
		if !position.isMain() {
			continue
		}
		order, err := parsePositionOrder(position.Order)
		if err != nil {
			return err
		}
		p.setMain = true
		p.inMain = true
		p.order = order
		return nil
	}

	// The vector is present but holds no main-list position.
	p.setMain = true
	p.inMain = false
	return nil
}

// isAbsentJSON reports whether a raw JSON field carries no value at all.
func isAbsentJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// parsePositionOrder reads chatPosition.order, which TDLib carries as a
// JSON string of an int64. The one field that had to be read both ways
// before tdInt existed; it reads it like every other number now.
func parsePositionOrder(raw json.RawMessage) (int64, error) {
	if isAbsentJSON(raw) {
		return 0, nil
	}

	var order tdInt
	if err := json.Unmarshal(raw, &order); err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrLiveStateOrder, string(raw), err)
	}

	return int64(order), nil
}

// liveMessageRaw mirrors the fields of a TDLib message used for a
// preview. The preview text is built by parseLastMessage, which the
// snapshot path already uses, so the two paths cannot drift apart.
//
// The numbers are tdInt, like every other number TDLib writes: see
// tdint.go.
type liveMessageRaw struct {
	ID         tdInt           `json:"id"`
	ChatID     tdInt           `json:"chat_id"`
	IsOutgoing bool            `json:"is_outgoing"`
	Date       tdInt           `json:"date"`
	Content    json.RawMessage `json:"content"`
}

// decodeLiveMessage turns a raw last_message into a Message, or nil when
// there is none.
func decodeLiveMessage(raw json.RawMessage) *Message {
	message, err := parseLiveMessage(raw)
	if err != nil {
		return nil
	}
	return &message
}

// parseLiveMessage is decodeLiveMessage without the silent nil.
//
// The chat list treats an unreadable last_message as "no message": a
// preview that fails to parse must not cost the user the whole update
// that carried it. A message event cannot afford that, because the event
// would be filed under no chat at all and the consumer would never learn
// that a message exists. So the same decode is done strictly here, and
// the caller reports the failure.
//
// The preview text comes from parseLastMessage, which the snapshot path
// already uses, so a message cannot look different depending on which
// path saw it.
func parseLiveMessage(raw json.RawMessage) (Message, error) {
	if isAbsentJSON(raw) {
		return Message{}, errors.New("telegram message is absent")
	}

	var message liveMessageRaw
	if err := json.Unmarshal(raw, &message); err != nil {
		return Message{}, fmt.Errorf("decode message: %w", err)
	}
	if message.ID == 0 {
		return Message{}, errors.New("telegram message has no id")
	}
	if message.ChatID == 0 {
		return Message{}, fmt.Errorf(
			"telegram message %d has no chat id",
			message.ID,
		)
	}

	// parseLastMessage already extracts the preview text for a message
	// payload, including the word for a file and the caption under it.
	_, text, _ := parseLastMessage(raw)

	return Message{
		ID:        MessageID(message.ID),
		ChatID:    ChatID(message.ChatID),
		Outgoing:  message.IsOutgoing,
		Timestamp: time.Unix(int64(message.Date), 0).UTC(),
		Text:      text,
	}, nil
}

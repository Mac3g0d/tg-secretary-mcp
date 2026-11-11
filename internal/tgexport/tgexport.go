// Package tgexport loads chat history exported by Telegram Desktop
// ("Export chat history" → JSON, result.json) into the archive.
//
// Both layouts are understood: a single-chat export, whose top-level object is
// the chat, and a full account export, which nests chats under chats.list.
// Media files are not needed and not read: only the kind of attachment is
// recorded, the same way the bot does for live messages.
package tgexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const (
	// _batchSize bounds memory use and the length of each write transaction,
	// so the bot can keep saving live messages during a large import.
	_batchSize = 1000

	// _supergroupOffset converts the bare ID of a supergroup or channel, as
	// exports write it, into the "-100…" form the Bot API uses.
	_supergroupOffset = 1_000_000_000_000

	// _deletedAccount is what Telegram shows for an author without a name.
	_deletedAccount = "Deleted Account"

	_exportDateLayout = "2006-01-02T15:04:05"
)

// ErrNoChats is returned when the input holds no chat history at all, which
// usually means it is not a Telegram export.
var ErrNoChats = errors.New("no chats found: not a Telegram Desktop JSON export")

// Importer is the part of the archive an import writes to.
type Importer interface {
	ImportMessages(ctx context.Context, chat store.Chat, msgs []store.Message) (int64, error)
}

var _ Importer = (*store.Store)(nil)

// ChatResult summarises the import of one chat.
type ChatResult struct {
	ChatID int64
	Title  string
	Type   string
	// Imported counts messages added to the archive.
	Imported int64
	// AlreadyStored counts messages skipped because the archive had a
	// message with the same ID in this chat.
	AlreadyStored int64
	// Skipped counts entries without text or attachment, such as service
	// messages about joins and calls.
	Skipped int64
}

// Result summarises an import.
type Result struct {
	Chats []ChatResult
}

// Import reads a Telegram Desktop JSON export from r and stores its messages.
// The input is processed as a stream, so large exports do not have to fit in
// memory. Running the same import twice adds nothing the second time.
func Import(ctx context.Context, dst Importer, r io.Reader) (Result, error) {
	imp := &importer{
		ctx: ctx,
		dst: dst,
		dec: json.NewDecoder(r),
	}
	if err := imp.readExport(); err != nil {
		return imp.result, fmt.Errorf("read export: %w", err)
	}
	if len(imp.result.Chats) == 0 {
		return imp.result, ErrNoChats
	}
	return imp.result, nil
}

type importer struct {
	ctx     context.Context
	dst     Importer
	dec     *json.Decoder
	ownerID int64 // account owner, known only in full exports
	result  Result
}

// readExport reads the top-level object, which is either one chat or a full
// export with personal_information and chats.list.
func (imp *importer) readExport() error {
	var chat chatHeader
	seenMessages := false
	err := imp.object(func(key string) error {
		switch key {
		case "personal_information":
			var info struct {
				UserID int64 `json:"user_id"`
			}
			if err := imp.dec.Decode(&info); err != nil {
				return err
			}
			imp.ownerID = info.UserID
			return nil
		case "chats":
			return imp.object(func(key string) error {
				if key != "list" {
					return imp.skip()
				}
				return imp.array(imp.readChat)
			})
		case "messages":
			seenMessages = true
			return imp.readMessages(chat)
		default:
			return chat.field(key, imp)
		}
	})
	if err != nil {
		return err
	}
	if seenMessages && chat.ID == 0 {
		return errors.New("the chat has no id")
	}
	return nil
}

// readChat reads one element of chats.list.
func (imp *importer) readChat() error {
	var chat chatHeader
	return imp.object(func(key string) error {
		if key == "messages" {
			return imp.readMessages(chat)
		}
		return chat.field(key, imp)
	})
}

// readMessages reads the messages array of chat in batches. The export
// writes name, type and id before messages, which is what makes streaming
// possible.
func (imp *importer) readMessages(header chatHeader) error {
	if header.ID == 0 {
		return errors.New(`"messages" comes before the chat "id": unsupported export layout`)
	}
	chat := header.chat()
	res := ChatResult{
		ChatID: chat.ID,
		Title:  chat.Title,
		Type:   chat.Type,
	}

	batch := make([]store.Message, 0, _batchSize)
	flush := func() error {
		if err := imp.ctx.Err(); err != nil {
			return err
		}
		n, err := imp.dst.ImportMessages(imp.ctx, chat, batch)
		if err != nil {
			return err
		}
		res.Imported += n
		res.AlreadyStored += int64(len(batch)) - n
		batch = batch[:0]
		return nil
	}

	err := imp.array(func() error {
		var raw exportMessage
		if err := imp.dec.Decode(&raw); err != nil {
			return err
		}
		m, ok := raw.message(header, imp.ownerID)
		if !ok {
			res.Skipped++
			return nil
		}
		batch = append(batch, m)
		if len(batch) == _batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chat %d: %w", chat.ID, err)
	}
	// Flushing an empty batch still registers a chat that has no messages.
	if err := flush(); err != nil {
		return fmt.Errorf("chat %d: %w", chat.ID, err)
	}
	imp.result.Chats = append(imp.result.Chats, res)
	return nil
}

// object reads a JSON object, calling field for every key. field must
// consume the value.
func (imp *importer) object(field func(key string) error) error {
	if err := imp.delim('{'); err != nil {
		return err
	}
	for imp.dec.More() {
		tok, err := imp.dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("unexpected token %v, want an object key", tok)
		}
		if err := field(key); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	_, err := imp.dec.Token() // closing brace
	return err
}

// array reads a JSON array, calling elem for every element. elem must
// consume the element.
func (imp *importer) array(elem func() error) error {
	if err := imp.delim('['); err != nil {
		return err
	}
	for imp.dec.More() {
		if err := elem(); err != nil {
			return err
		}
	}
	_, err := imp.dec.Token() // closing bracket
	return err
}

func (imp *importer) delim(want json.Delim) error {
	tok, err := imp.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("unexpected token %v, want %q", tok, want)
	}
	return nil
}

// skip discards the next value.
func (imp *importer) skip() error {
	var discard json.RawMessage
	return imp.dec.Decode(&discard)
}

// chatHeader holds the chat fields that precede the messages array.
type chatHeader struct {
	Name string
	Type string
	ID   int64
}

// field stores the value of a chat-level key, skipping the ones not needed.
func (h *chatHeader) field(key string, imp *importer) error {
	switch key {
	case "name":
		var name *string // null for deleted accounts
		if err := imp.dec.Decode(&name); err != nil {
			return err
		}
		if name != nil {
			h.Name = *name
		}
		return nil
	case "type":
		return imp.dec.Decode(&h.Type)
	case "id":
		return imp.dec.Decode(&h.ID)
	default:
		return imp.skip()
	}
}

// chat maps the export's chat to the archive's, using the same IDs and types
// as the Bot API so imported history lands in the chat the bot writes to.
func (h chatHeader) chat() store.Chat {
	c := store.Chat{
		ID:    h.ID,
		Type:  "private",
		Title: h.Name,
	}
	switch h.Type {
	case "private_group":
		c.ID, c.Type = -h.ID, "group"
	case "private_supergroup", "public_supergroup":
		c.ID, c.Type = -(_supergroupOffset + h.ID), "supergroup"
	case "private_channel", "public_channel":
		c.ID, c.Type = -(_supergroupOffset + h.ID), "channel"
	case "saved_messages":
		c.Title = "Saved Messages"
	}
	if c.Title == "" {
		c.Title = _deletedAccount
	}
	return c
}

// exportMessage is one entry of an export's messages array.
type exportMessage struct {
	ID            int64           `json:"id"`
	Type          string          `json:"type"`
	Date          string          `json:"date"`
	DateUnix      string          `json:"date_unixtime"`
	EditedUnix    string          `json:"edited_unixtime"`
	From          *string         `json:"from"`
	FromID        string          `json:"from_id"`
	Text          json.RawMessage `json:"text"`
	ReplyToID     int64           `json:"reply_to_message_id"`
	ForwardedFrom json.RawMessage `json:"forwarded_from"`
	Photo         json.RawMessage `json:"photo"`
	File          json.RawMessage `json:"file"`
	MediaType     string          `json:"media_type"`
	Poll          json.RawMessage `json:"poll"`
	Location      json.RawMessage `json:"location_information"`
	Contact       json.RawMessage `json:"contact_information"`
}

// message converts the entry. It reports false for entries that carry
// neither text nor an attachment.
func (e exportMessage) message(chat chatHeader, ownerID int64) (store.Message, bool) {
	if e.Type != "message" || e.ID == 0 {
		return store.Message{}, false
	}
	m := store.Message{
		ID:        e.ID,
		Date:      e.date(),
		Text:      flattenText(e.Text),
		MediaType: e.mediaType(),
		ReplyToID: e.ReplyToID,
	}
	if m.Text == "" && m.MediaType == "" {
		return store.Message{}, false
	}
	if sec, err := strconv.ParseInt(e.EditedUnix, 10, 64); err == nil && sec > 0 {
		m.EditedAt = time.Unix(sec, 0)
	}

	m.SenderID = senderID(e.FromID)
	if e.From != nil {
		m.SenderName = *e.From
	}
	if m.SenderName == "" {
		m.SenderName = _deletedAccount
	}

	// The export does not say when the original of a forward was sent, so
	// the author's name is what marks a message as forwarded.
	if len(e.ForwardedFrom) > 0 {
		var name *string
		_ = json.Unmarshal(e.ForwardedFrom, &name)
		m.ForwardFrom = _deletedAccount
		if name != nil && *name != "" {
			m.ForwardFrom = *name
		}
	}

	switch {
	case ownerID != 0:
		m.Outgoing = m.SenderID == ownerID
	case chat.Type == "saved_messages":
		m.Outgoing = true
	case chat.Type == "personal_chat" || chat.Type == "bot_chat":
		// In a one-to-one chat the chat ID is the other side's user ID, so
		// everything else was written by the exporting account.
		m.Outgoing = m.SenderID != 0 && m.SenderID != chat.ID
	}
	return m, true
}

func (e exportMessage) date() time.Time {
	if sec, err := strconv.ParseInt(e.DateUnix, 10, 64); err == nil {
		return time.Unix(sec, 0)
	}
	// Older exports only have the local wall-clock time.
	if t, err := time.ParseInLocation(_exportDateLayout, e.Date, time.Local); err == nil {
		return t
	}
	return time.Unix(0, 0)
}

// mediaType names the attachment the way the bot does for live messages.
func (e exportMessage) mediaType() string {
	switch e.MediaType {
	case "voice_message":
		return "voice"
	case "video_message":
		return "video_note"
	case "video_file":
		return "video"
	case "audio_file":
		return "audio"
	case "animation", "sticker":
		return e.MediaType
	}
	switch {
	case len(e.Photo) > 0:
		return "photo"
	case len(e.File) > 0:
		return "document"
	case len(e.Poll) > 0:
		return "poll"
	case len(e.Location) > 0:
		return "location"
	case len(e.Contact) > 0:
		return "contact"
	}
	return e.MediaType
}

// flattenText returns the plain text of a message. Exports write it either
// as a string or, when the message has formatting or links, as an array that
// mixes strings and {"type": …, "text": …} objects.
func flattenText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		var s string
		if err := json.Unmarshal(part, &s); err == nil {
			b.WriteString(s)
			continue
		}
		var entity struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(part, &entity); err == nil {
			b.WriteString(entity.Text)
		}
	}
	return b.String()
}

// senderID converts an export's from_id ("user123", "channel456", "chat789")
// into the numeric ID the Bot API would report, or 0 if it has another form.
func senderID(fromID string) int64 {
	for _, p := range []struct {
		prefix string
		toID   func(int64) int64
	}{
		{"user", func(id int64) int64 { return id }},
		{"channel", func(id int64) int64 { return -(_supergroupOffset + id) }},
		{"chat", func(id int64) int64 { return -id }},
	} {
		rest, ok := strings.CutPrefix(fromID, p.prefix)
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return 0
		}
		return p.toID(id)
	}
	return 0
}

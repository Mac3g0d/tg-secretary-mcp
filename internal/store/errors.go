package store

import "errors"

// Sentinel errors returned (wrapped) by Store methods. Use errors.Is to test
// for them; the error text is written for humans and LLM agents.
var (
	// ErrChatRequired is returned when a chat reference is empty.
	ErrChatRequired = errors.New("chat is required")
	// ErrChatNotFound is returned when no stored chat matches a reference.
	ErrChatNotFound = errors.New("chat not found")
	// ErrAmbiguousChat is returned when a reference matches several chats.
	ErrAmbiguousChat = errors.New("ambiguous chat")
	// ErrMessageNotFound is returned when a message is not stored.
	ErrMessageNotFound = errors.New("message not found")
)

// described pairs a sentinel with a human-readable message, so errors.Is
// works while Error keeps the exact text shown to users.
type described struct {
	msg string
	err error
}

func (e *described) Error() string { return e.msg }

func (e *described) Unwrap() error { return e.err }

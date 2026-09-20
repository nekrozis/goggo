// Package log defines the message model used for console reporting: a message's
// type, verbosity level, timestamp, optional prefix and ANSI-colored rendering.
//
// It only models and formats a single message; the level filtering and the
// printing loop live in the front end.
package log

import "time"

// MsgType classifies a message and selects its ANSI color.
type MsgType uint32

// MsgTypeInfo and the following constants are the message types; the type also
// selects the ANSI color.
const (
	MsgTypeInfo    MsgType = 1 << 0
	MsgTypeWarning MsgType = 1 << 1
	MsgTypeError   MsgType = 1 << 2
	MsgTypeSuccess MsgType = 1 << 3
)

// MsgLevel is the verbosity gate a message is printed under.
type MsgLevel int

// MsgLevelAlways and the following constants are the verbosity gates. A message
// at MsgLevelAlways prints regardless of the configured verbosity.
const (
	MsgLevelAlways  MsgLevel = -1
	MsgLevelDefault MsgLevel = 0
	MsgLevelVerbose MsgLevel = 1
	MsgLevelDebug   MsgLevel = 2
)

// timeLayout is the timestamp layout: local time, 24-hour, e.g.
// "2026-Sep-09 23:46:13".
const timeLayout = "2006-Jan-02 15:04:05"

// ANSI escape sequences used by Format.
const (
	ansiReset     = "\x1b[0m"
	ansiDefaultFg = "\x1b[39m"
	ansiYellow    = "\x1b[33m"
	ansiRed       = "\x1b[31m"
	ansiGreen     = "\x1b[32m"
)

// Message is a single reportable event. The fields are plain and exported; the
// value is used read-only once queued.
type Message struct {
	Time   time.Time
	Text   string
	Prefix string
	Level  MsgLevel
	Type   MsgType
}

// NewMessage builds a Message with the Info type and the default level. Callers
// that need a specific type, level or prefix can construct the struct literal
// directly.
func NewMessage(text string) Message {
	return Message{
		Text:  text,
		Type:  MsgTypeInfo,
		Time:  time.Now(),
		Level: MsgLevelDefault,
	}
}

// ansiColor returns the foreground color for typ, or the default foreground
// color for an unknown type.
func ansiColor(typ MsgType) string {
	switch typ {
	case MsgTypeInfo:
		return ansiDefaultFg
	case MsgTypeWarning:
		return ansiYellow
	case MsgTypeError:
		return ansiRed
	case MsgTypeSuccess:
		return ansiGreen
	default:
		return ansiDefaultFg
	}
}

// Format renders the message as:
//
//	[timestamp][ " " prefix] message
//
// with the prefix included only when non-empty and prefix is true. When color
// is true the whole line is wrapped in the type color and a reset sequence.
func (m Message) Format(color, prefix bool) string {
	line := m.Text
	if m.Prefix != "" && prefix {
		line = m.Prefix + " " + line
	}
	line = m.Time.Format(timeLayout) + " " + line
	if color {
		line = ansiColor(m.Type) + line + ansiReset
	}
	return line
}

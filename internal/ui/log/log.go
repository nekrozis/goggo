// Package log defines the message model used for console reporting.
//
// It ports include/message.h of LGOGDownloader (WTFPL; pinned reference under
// /reference). The level filtering and the console printing loop that consume
// these messages live in the C++ downloader queue (downloader.cpp) and will be
// ported with the core work (internal/core). This package only models a single
// message and its formatted rendering.
package log

import "time"

// MsgType classifies a message and selects its ANSI color.
type MsgType uint32

// Message type bit flags (message.h:12-15).
const (
	MsgTypeInfo    MsgType = 1 << 0
	MsgTypeWarning MsgType = 1 << 1
	MsgTypeError   MsgType = 1 << 2
	MsgTypeSuccess MsgType = 1 << 3
)

// MsgLevel is the verbosity gate a message is printed under.
type MsgLevel int

// Message levels (message.h:17-20). Always prints regardless of the
// configured verbosity.
const (
	MsgLevelAlways  MsgLevel = -1
	MsgLevelDefault MsgLevel = 0
	MsgLevelVerbose MsgLevel = 1
	MsgLevelDebug   MsgLevel = 2
)

// timeLayout mirrors boost::posix_time::to_simple_string used for the C++
// timestamp (e.g. "2026-Sep-09 23:46:13", local time, 24h).
const timeLayout = "2006-Jan-02 15:04:05"

// ANSI escape sequences matching getFormattedString (message.h:93-103).
const (
	ansiReset     = "\x1b[0m"
	ansiDefaultFg = "\x1b[39m"
	ansiYellow    = "\x1b[33m"
	ansiRed       = "\x1b[31m"
	ansiGreen     = "\x1b[32m"
)

// Message is a single reportable event. Getter/setter pairs of the C++ class
// are omitted in favour of plain exported fields; the value is used
// read-only once queued.
//
// Fields are ordered by allocation size (largest first, smallest last) to
// minimise struct padding: Time time.Time = 24B, strings = 16B, MsgLevel
// (int) = 8B, MsgType = 4B.
type Message struct {
	Time   time.Time
	Text   string
	Prefix string
	Level  MsgLevel
	Type   MsgType
}

// NewMessage builds a Message with the Info type and the default level
// (message.h:26-33). Callers that need a specific type/level/prefix can
// construct the struct literal directly.
func NewMessage(text string) Message {
	return Message{
		Text:  text,
		Type:  MsgTypeInfo,
		Time:  time.Now(),
		Level: MsgLevelDefault,
	}
}

// ansiColor returns the foreground color for typ, defaulting to the default
// foreground color for unknown types, matching getFormattedString.
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
// with the prefix included only when non-empty and bPrefix is true. When
// bColor is true the whole line is wrapped in the type color and a reset
// sequence (message.h:90-115).
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

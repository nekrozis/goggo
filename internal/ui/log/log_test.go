package log

import (
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, time.September, 9, 23, 46, 13, 0, time.Local)
}

func TestNewMessageDefaults(t *testing.T) {
	m := NewMessage("hello")
	if m.Text != "hello" {
		t.Errorf("Text = %q", m.Text)
	}
	if m.Type != MsgTypeInfo {
		t.Errorf("Type = %d, want MsgTypeInfo", m.Type)
	}
	if m.Level != MsgLevelDefault {
		t.Errorf("Level = %d, want MsgLevelDefault", m.Level)
	}
	if m.Time.IsZero() {
		t.Error("Time must be set by NewMessage")
	}
}

func TestFormatWithoutColorAndPrefix(t *testing.T) {
	m := Message{Text: "done", Type: MsgTypeInfo, Time: fixedTime()}
	got := m.Format(false, true)
	want := "2026-Sep-09 23:46:13 done"
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}

func TestFormatIncludesPrefixOnlyWhenSet(t *testing.T) {
	base := Message{Text: "msg", Type: MsgTypeInfo, Time: fixedTime()}

	withPrefix := base
	withPrefix.Prefix = "tag"
	got := withPrefix.Format(false, true)
	want := "2026-Sep-09 23:46:13 tag msg"
	if got != want {
		t.Errorf("with prefix Format = %q, want %q", got, want)
	}

	// bPrefix=false must suppress an otherwise non-empty prefix.
	if got := withPrefix.Format(false, false); got != "2026-Sep-09 23:46:13 msg" {
		t.Errorf("prefix suppressed Format = %q", got)
	}

	// Empty prefix must never introduce an extra space.
	if got := base.Format(false, true); got != "2026-Sep-09 23:46:13 msg" {
		t.Errorf("empty prefix Format = %q", got)
	}
}

// Contract (format): the SGR spellings and their exact placement around the
// timestamp and the text are the bytes a terminal consumes, so the four
// type-to-colour wrappings are pinned byte-exactly.
func TestFormatColorWrappingPerType(t *testing.T) {
	cases := []struct {
		name string
		typ  MsgType
		want string
	}{
		{"info", MsgTypeInfo, "\x1b[39m2026-Sep-09 23:46:13 m\x1b[0m"},
		{"warning", MsgTypeWarning, "\x1b[33m2026-Sep-09 23:46:13 m\x1b[0m"},
		{"error", MsgTypeError, "\x1b[31m2026-Sep-09 23:46:13 m\x1b[0m"},
		{"success", MsgTypeSuccess, "\x1b[32m2026-Sep-09 23:46:13 m\x1b[0m"},
		{"unknown", MsgType(1 << 5), "\x1b[39m2026-Sep-09 23:46:13 m\x1b[0m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Message{Text: "m", Type: c.typ, Time: fixedTime()}
			if got := m.Format(true, true); got != c.want {
				t.Errorf("Format = %q, want %q", got, c.want)
			}
		})
	}
}

// Contract (format): the timestamp layout is itself the interface — boost's
// to_simple_string spelling, English short month and 24h clock — so the prefix
// is pinned byte-exactly.
func TestFormatTimeLayoutMatchesBoostSimple(t *testing.T) {
	// boost to_simple_string prints "2026-Sep-09 23:46:13"; the layout must
	// render English short month names and 24h time.
	m := Message{Text: "x", Type: MsgTypeInfo, Time: fixedTime()}
	got := m.Format(false, true)
	if !strings.HasPrefix(got, "2026-Sep-09 23:46:13 x") {
		t.Errorf("Format = %q, want boost-style timestamp prefix", got)
	}
	// Sanity: month abbreviation must be the English one, not a number.
	if strings.Contains(got, "09 09") || strings.Contains(got, "9月") {
		t.Errorf("Format = %q contains non-English month", got)
	}
}

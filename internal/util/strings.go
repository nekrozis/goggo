package util

import "strings"

// strippedAllowed reports whether a byte survives getStrippedString.
//
// C++ keeps a char when (isspace(c) && isprint(c)) || isalnum(c) or it is in
// {'-','_','.','(',')','[',']','{','}'}. On an unsigned char the only
// whitespace that is also printable is the space (0x20), and isalnum covers
// ASCII letters and digits only. The rule is therefore pinned to these exact
// bytes; unicode helpers must NOT be used as a substitute.
func strippedAllowed(c byte) bool {
	switch {
	case c == ' ':
		return true
	case c >= '0' && c <= '9':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	}
	switch c {
	case '-', '_', '.', '(', ')', '[', ']', '{', '}':
		return true
	}
	return false
}

// StrippedString mirrors Util::getStrippedString (util.cpp:643-662) as a
// byte-level filter: it keeps ASCII [A-Za-z0-9], the space and -_.[]{}() and
// drops every other byte (so UTF-8 multi-byte sequences are removed
// byte-by-byte).
func StrippedString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strippedAllowed(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

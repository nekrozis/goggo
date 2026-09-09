package cookiefile

import (
	"io"
	"strconv"
	"strings"
	"time"
)

// headerLine is the conventional first line of a curl-style cookies.txt file.
const headerLine = "# Netscape HTTP Cookie File"

// httpOnlyPrefix marks HttpOnly cookies in curl's cookies.txt extension: the
// domain field carries this prefix before the actual domain.
const httpOnlyPrefix = "#HttpOnly_"

// fieldCount is the number of TAB-separated columns per Netscape row:
// domain, includeSubdomains, path, secure, expiry, name, value.
const fieldCount = 7

// PersistentCookie is one cookie as carried by a cookies.txt file.
//
// Domain is kept verbatim (leading dot and case preserved) in both
// directions: this codec does not normalise domain semantics — that is the
// bridge's job (see doc.go). HostOnly maps one-to-one onto the file's
// includeSubdomains column (TRUE => HostOnly=false, FALSE => HostOnly=true).
//
// Fields are ordered to minimise padding: time.Time (24B), the string block
// (16B each), then the bool flags (1B each).
type PersistentCookie struct {
	Expires  time.Time
	Domain   string
	Path     string
	Name     string
	Value    string
	Secure   bool
	HttpOnly bool
	HostOnly bool
}

// unrepresentable reports whether the row cannot be expressed in the
// TAB-delimited Netscape format: a TAB, CR or LF inside any of the four
// free-form columns would break the column layout (there is no escaping
// mechanism in the format).
func (c PersistentCookie) unrepresentable() bool {
	return strings.ContainsAny(c.Domain, "\t\r\n") ||
		strings.ContainsAny(c.Path, "\t\r\n") ||
		strings.ContainsAny(c.Name, "\t\r\n") ||
		strings.ContainsAny(c.Value, "\t\r\n")
}

// Parse decodes a cookies.txt document into its rows.
//
// Tolerance mirrors curl's cookie engine: comment lines and blank lines are
// skipped, and malformed rows (wrong column count, a non-TRUE/FALSE flag, a
// non-integer expiry, a negative expiry, or extra columns caused by a tab
// inside the value) are skipped instead of failing the whole parse. A row
// whose domain column starts with the "#HttpOnly_" prefix yields
// HttpOnly=true with the prefix stripped; any other "#"-prefixed row is
// treated as a comment. Both LF and CRLF line endings are accepted. The
// function never fails and never panics on arbitrary input.
func Parse(data []byte) []PersistentCookie {
	var out []PersistentCookie
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != fieldCount {
			continue
		}
		domain := cols[0]
		httpOnly := false
		if strings.HasPrefix(domain, httpOnlyPrefix) {
			httpOnly = true
			domain = domain[len(httpOnlyPrefix):]
		} else if strings.HasPrefix(domain, "#") {
			continue
		}
		if domain == "" {
			continue
		}

		// Second column is includeSubdomains: TRUE means a domain cookie
		// (HostOnly=false), FALSE means a host-only cookie (HostOnly=true).
		include, ok := boolColumn(cols[1], true)
		if !ok {
			continue
		}
		secure, ok := boolColumn(cols[3], false)
		if !ok {
			continue
		}

		expiry, err := strconv.ParseInt(cols[4], 10, 64)
		if err != nil || expiry < 0 {
			continue
		}
		var expires time.Time
		if expiry > 0 {
			expires = time.Unix(expiry, 0)
		}

		out = append(out, PersistentCookie{
			Expires:  expires,
			Domain:   domain,
			Path:     cols[2],
			Name:     cols[5],
			Value:    cols[6],
			Secure:   secure,
			HttpOnly: httpOnly,
			HostOnly: !include,
		})
	}
	return out
}

// boolColumn parses a TRUE/FALSE column and reports whether it reads TRUE.
// An empty column is treated as emptyIs (curl always writes one of the two
// words, so this only guards hand-edited files); ok is false for any other
// value, which makes the caller skip the row as malformed.
func boolColumn(s string, emptyIs bool) (bool, bool) {
	switch s {
	case "TRUE":
		return true, true
	case "FALSE":
		return false, true
	case "":
		return emptyIs, true
	default:
		return false, false
	}
}

// Write encodes cookies to w in cookies.txt form: a leading comment header
// then one row per cookie. Rows whose Domain/Path/Name/Value contain a TAB,
// CR or LF cannot be expressed and are SKIPPED (they never fail the write);
// the number of successfully written rows is returned so the caller can
// report the skip count (len(cookies)-n). w errors are returned unchanged.
func Write(w io.Writer, cookies []PersistentCookie) (int, error) {
	if _, err := io.WriteString(w, headerLine+"\n"); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cookies {
		if c.unrepresentable() {
			continue
		}
		if _, err := io.WriteString(w, encodeRow(c)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// encodeRow renders one cookie as a single line (no trailing newline-free
// output: the newline is included).
func encodeRow(c PersistentCookie) string {
	domain := c.Domain
	if c.HttpOnly {
		domain = httpOnlyPrefix + domain
	}
	include := "FALSE"
	if !c.HostOnly {
		include = "TRUE"
	}
	secure := "FALSE"
	if c.Secure {
		secure = "TRUE"
	}
	expiry := "0"
	if !c.Expires.IsZero() {
		expiry = strconv.FormatInt(c.Expires.Unix(), 10)
	}
	return domain + "\t" + include + "\t" + c.Path + "\t" + secure + "\t" + expiry +
		"\t" + c.Name + "\t" + c.Value + "\n"
}

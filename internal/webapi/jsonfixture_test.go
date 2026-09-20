package webapi

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/nekrozis/goggo/internal/jsonread"
)

// mustText reads a string member for an assertion. A fixture that cannot be read
// is a broken test rather than a failed case, so it reports Fatalf.
func mustText(t *testing.T, v jsontext.Value) string {
	t.Helper()
	s, err := jsonread.Text(v)
	if err != nil {
		t.Fatalf("jsonread.Text(%s): %v", string(v), err)
	}
	return s
}

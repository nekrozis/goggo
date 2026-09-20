package zipx

import (
	"strconv"
	"testing"
)

// mojoLine1 builds the typical offset line of a MojoSetup script: literal
// text `offset=`head -n N "$0"` (the regexp matches up to "$0").
func mojoLine1(n int) string {
	return "offset=`head -n " + strconv.Itoa(n) + " \"$0\""
}

// TestMojoSetupScriptSizeMatch locks the core contract: the offset line
// declares N and MojoSetupScriptSize returns the total length of the first N
// lines of data, each line plus a trailing newline.
func TestMojoSetupScriptSizeMatch(t *testing.T) {
	line1 := mojoLine1(2)
	line2 := "second script line"
	data := line1 + "\n" + line2 + "\nBINARY-PAYLOAD"

	want := int64(len(line1) + 1 + len(line2) + 1)
	if got := MojoSetupScriptSize([]byte(data)); got != want {
		t.Errorf("MojoSetupScriptSize = %d, want %d", got, want)
	}
}

func TestMojoSetupScriptSizeNoMatch(t *testing.T) {
	data := "#!/bin/sh\necho nothing useful\n"
	if got := MojoSetupScriptSize([]byte(data)); got != 0 {
		t.Errorf("MojoSetupScriptSize = %d, want 0", got)
	}
}

// TestMojoSetupScriptSizeCaseInsensitive: the offset pattern is matched
// case-insensitively.
func TestMojoSetupScriptSizeCaseInsensitive(t *testing.T) {
	line1 := "OFFSET=`head -n 1 \"$0\""
	line2 := "only line"
	data := line1 + "\n" + line2 + "\nrest"
	want := int64(len(line1) + 1) // N = 1: only the offset line is counted
	if got := MojoSetupScriptSize([]byte(data)); got != want {
		t.Errorf("MojoSetupScriptSize = %d, want %d", got, want)
	}
}

// TestMojoSetupScriptSizeCountPastEOF locks the behaviour when N exceeds the
// available lines: every extra counted line contributes only the "\n"
// terminator.
func TestMojoSetupScriptSizeCountPastEOF(t *testing.T) {
	line1 := mojoLine1(5)
	line2 := "abc"
	data := line1 + "\n" + line2 + "\n"
	// 5 counted lines: two with content, three extra newline-only.
	want := int64(len(line1) + 1 + len(line2) + 1 + 3)
	if got := MojoSetupScriptSize([]byte(data)); got != want {
		t.Errorf("MojoSetupScriptSize = %d, want %d", got, want)
	}
}

// TestMojoSetupScriptSizeCountZero: `head -n 0` yields an empty script.
func TestMojoSetupScriptSizeCountZero(t *testing.T) {
	data := "offset=`head -n 0 \"$0\"\nignored"
	if got := MojoSetupScriptSize([]byte(data)); got != 0 {
		t.Errorf("MojoSetupScriptSize = %d, want 0", got)
	}
}

// TestMojoSetupScriptSizeOverflow: a count beyond int32 maps to 0, the same
// overflow convention the option-value parser uses.
func TestMojoSetupScriptSizeOverflow(t *testing.T) {
	data := "offset=`head -n 99999999999999999999 \"$0\"\nwhatever"
	if got := MojoSetupScriptSize([]byte(data)); got != 0 {
		t.Errorf("MojoSetupScriptSize = %d, want 0", got)
	}
}

// TestMojoSetupScriptSizeFirstMatchWins: regex_search returns the leftmost
// match; a later offset line must not override the first.
func TestMojoSetupScriptSizeFirstMatchWins(t *testing.T) {
	line1 := mojoLine1(2)
	data := line1 + "\nsecond\n" + "offset=`head -n 9 \"$0\"\nnever-used"
	want := int64(len(line1) + 1 + len("second") + 1)
	if got := MojoSetupScriptSize([]byte(data)); got != want {
		t.Errorf("MojoSetupScriptSize = %d, want %d (first match wins)", got, want)
	}
}

func TestMojoSetupInstallerSizeMatch(t *testing.T) {
	data := "filesizes=\"12345\""
	size, ok := MojoSetupInstallerSize([]byte(data))
	if !ok || size != 12345 {
		t.Errorf("MojoSetupInstallerSize = (%d, %v), want (12345, true)", size, ok)
	}
}

func TestMojoSetupInstallerSizeCaseInsensitive(t *testing.T) {
	data := "FILESIZES=\"99\""
	size, ok := MojoSetupInstallerSize([]byte(data))
	if !ok || size != 99 {
		t.Errorf("MojoSetupInstallerSize = (%d, %v), want (99, true)", size, ok)
	}
}

func TestMojoSetupInstallerSizeNoMatch(t *testing.T) {
	size, ok := MojoSetupInstallerSize([]byte("no filesizes marker here"))
	if ok || size != 0 {
		t.Errorf("MojoSetupInstallerSize = (%d, %v), want (0, false)", size, ok)
	}
}

// TestMojoSetupInstallerSizeFirstMatchWins locks the leftmost-match rule for the
// filesizes marker as well.
func TestMojoSetupInstallerSizeFirstMatchWins(t *testing.T) {
	data := "filesizes=\"111\" trailing filesizes=\"222\""
	size, ok := MojoSetupInstallerSize([]byte(data))
	if !ok || size != 111 {
		t.Errorf("MojoSetupInstallerSize = (%d, %v), want (111, true)", size, ok)
	}
}

// TestMojoSetupInstallerSizeOverflow: a value beyond int64 reports ok=false.
func TestMojoSetupInstallerSizeOverflow(t *testing.T) {
	data := "filesizes=\"99999999999999999999\""
	size, ok := MojoSetupInstallerSize([]byte(data))
	if ok || size != 0 {
		t.Errorf("MojoSetupInstallerSize = (%d, %v), want (0, false)", size, ok)
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/core"
)

// TestRenderOrphansListsTheObjects locks the listing: the header states the root
// once, every orphan gets one line relative to it (a long list stays readable and
// the root is not repeated), and the count closes it.
func TestRenderOrphansListsTheObjects(t *testing.T) {
	res := core.OrphansResult{
		InstallPath: "/games/hoMM3",
		Files: []string{
			"/games/hoMM3/saves/autosave.gam",
			"/games/hoMM3/mods/hd/patch.dll",
		},
	}
	var out bytes.Buffer
	renderOrphans(&out, res)

	want := strings.Join([]string{
		"Checking → /games/hoMM3",
		"  saves/autosave.gam",
		"  mods/hd/patch.dll",
		"2 orphaned files",
		"",
	}, "\n")
	if out.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", out.String(), want)
	}
}

// TestRenderOrphansEmpty locks the empty answer: nothing found is a result, not
// a silent success.
func TestRenderOrphansEmpty(t *testing.T) {
	var out bytes.Buffer
	renderOrphans(&out, core.OrphansResult{InstallPath: "/games/hoMM3"})

	want := "Checking → /games/hoMM3\nNo orphaned files\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// TestRenderOrphansWithoutARoot locks the degenerate case a stopped plan leaves:
// no root to name, so no header — the plan's own notices explain why.
func TestRenderOrphansWithoutARoot(t *testing.T) {
	var out bytes.Buffer
	renderOrphans(&out, core.OrphansResult{})

	if want := "No orphaned files\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// TestDeleteQuestion locks the authorization wording, count included: the default
// is No, and the reader is told how many files are about to go.
func TestDeleteQuestion(t *testing.T) {
	if got, want := deleteQuestion(1), "Delete this file? [y/N] "; got != want {
		t.Errorf("one file = %q, want %q", got, want)
	}
	if got, want := deleteQuestion(3), "Delete these 3 files? [y/N] "; got != want {
		t.Errorf("three files = %q, want %q", got, want)
	}
}

// TestConfirm locks what counts as a yes: an explicit
// y/yes in any case, and nothing else — an empty line, a stray keystroke and an
// answer that cannot be read all mean "do not delete". The prompt goes to the
// error stream, like every other prompt.
func TestConfirm(t *testing.T) {
	cases := []struct {
		stdin string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"  YES  \n", true},
		{"n\n", false},
		{"\n", false},
		{"yeah\n", false},
		{"", false}, // EOF: no answer is not an approval
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		ui := newConsole(strings.NewReader(c.stdin), &out, &errOut)

		if got := ui.confirm("Delete these 2 files? [y/N] "); got != c.want {
			t.Errorf("confirm(%q) = %v, want %v", c.stdin, got, c.want)
		}
		if !strings.Contains(errOut.String(), "Delete these 2 files? [y/N] ") {
			t.Errorf("confirm(%q) prompt = %q, want it on stderr", c.stdin, errOut.String())
		}
		if out.Len() != 0 {
			t.Errorf("confirm(%q) wrote %q to stdout, want nothing", c.stdin, out.String())
		}
	}
}

// TestOrphansRemoveHelpStatesBothRolesOfYes ties the documentation to the
// behaviour the tests above lock: --yes both skips the question and
// is what makes a run without a terminal possible. The help said so before the
// code did, so a topic that loses either half is a contract change.
func TestOrphansRemoveHelpStatesBothRolesOfYes(t *testing.T) {
	code, out, _ := run(t, "", "orphans", "remove", "-h")
	if code != 0 {
		t.Fatalf("orphans remove -h exit = %d, want 0", code)
	}
	for _, want := range []string{
		"Do not ask for confirmation before deleting",
		"Required when stdin is not a terminal",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the topic is missing %q: %q", want, out)
		}
	}
}

// TestOrphansRemoveWithoutATerminalNeedsYes locks the refusal: a removal that
// cannot ask for authorization and was not given one fails as a usage error, and
// it does so before any session work — the runner's input here is not a terminal,
// so nothing is asked for and nothing is opened.
func TestOrphansRemoveWithoutATerminalNeedsYes(t *testing.T) {
	code, out, errOut := run(t, "", "orphans", "remove", "123")
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage failure)", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "--yes") {
		t.Errorf("stderr = %q, want it to name --yes", errOut)
	}
}

// orphansRemovalFixture puts two files under a temp root and returns the result a
// walk would have produced. The empty downloader is enough: applying a removal
// reads no downloader state, it removes the paths it was handed.
func orphansRemovalFixture(t *testing.T) (core.OrphansResult, *core.Downloader, []string) {
	t.Helper()
	root := t.TempDir()
	var files []string
	for _, name := range []string{"saves/autosave.gam", "mods/patch.dll"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	return core.OrphansResult{InstallPath: root, Files: files}, &core.Downloader{}, files
}

// TestRemoveOrphansWithYesDoesNotAsk locks the meaning of --yes (D16): it IS
// the authorization, so a removal that was given it goes straight to the
// deletion — no question is printed, and the files are gone. Asking anyway would
// make the flag useless exactly where it matters, on a terminal.
func TestRemoveOrphansWithYesDoesNotAsk(t *testing.T) {
	res, d, files := orphansRemovalFixture(t)
	// A console whose reader has nothing to give: if the code asked, the answer
	// could only be "no", and the assertions below would fail on both counts.
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader(""), &out, &errOut)

	got := ui.removeOrphans(context.Background(), d, res, true, &out, &errOut)

	if got != outcomeOK {
		t.Errorf("outcome = %v, want success", got)
	}
	if strings.Contains(errOut.String(), "Delete these") {
		t.Errorf("stderr = %q, want no confirmation question with --yes", errOut.String())
	}
	if strings.Contains(errOut.String(), "Nothing was deleted") {
		t.Errorf("stderr = %q, want the removal to have happened", errOut.String())
	}
	want := "Deleting 2 orphaned files\n  saves/autosave.gam\n  mods/patch.dll\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	for _, path := range files {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived a removal authorized by --yes", path)
		}
	}
}

// TestRemoveOrphansAsksWithoutYes locks the other half: with no --yes the question
// decides, and only a clear yes deletes. The list of files that must survive a
// "no" is the same list that would have gone — nothing else is touched either way.
func TestRemoveOrphansAsksWithoutYes(t *testing.T) {
	cases := []struct {
		name       string
		stdin      string
		wantDelete bool
	}{
		{"y", "y\n", true},
		{"yes", "yes\n", true},
		{"n", "n\n", false},
		{"empty", "\n", false},
		{"no answer", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, d, files := orphansRemovalFixture(t)
			var out, errOut bytes.Buffer
			ui := newConsole(strings.NewReader(c.stdin), &out, &errOut)

			got := ui.removeOrphans(context.Background(), d, res, false, &out, &errOut)

			if got != outcomeOK {
				t.Errorf("outcome = %v, want success: a declined removal is not a failure", got)
			}
			if !strings.Contains(errOut.String(), "Delete these 2 files? [y/N] ") {
				t.Errorf("stderr = %q, want the confirmation question", errOut.String())
			}
			for _, path := range files {
				_, err := os.Stat(path)
				survived := err == nil
				if survived == c.wantDelete {
					t.Errorf("%s deleted = %v, want %v", path, c.wantDelete, !c.wantDelete)
				}
			}
			if c.wantDelete {
				if !strings.Contains(out.String(), "Deleting 2 orphaned files") {
					t.Errorf("stdout = %q, want the deletion header", out.String())
				}
				return
			}
			if !strings.Contains(errOut.String(), "Nothing was deleted.") {
				t.Errorf("stderr = %q, want the refusal reported", errOut.String())
			}
			if strings.Contains(out.String(), "Deleting") {
				t.Errorf("stdout = %q, want no deletion header", out.String())
			}
		})
	}
}

// TestRemoveOrphansWithoutFilesNeedsNoAuthorization locks the empty case: a walk
// that found nothing has nothing to authorize, so no question is asked even
// without --yes — and the run is a success.
func TestRemoveOrphansWithoutFilesNeedsNoAuthorization(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader(""), &out, &errOut)

	if got := ui.removeOrphans(context.Background(), &core.Downloader{},
		core.OrphansResult{InstallPath: t.TempDir()}, false, &out, &errOut); got != outcomeOK {
		t.Errorf("outcome = %v, want success", got)
	}
	if errOut.Len() != 0 || out.Len() != 0 {
		t.Errorf("wrote %q / %q, want nothing: there was nothing to decide", out.String(), errOut.String())
	}
}

package cli

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"github.com/nekrozis/goggo/internal/auth"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
)

// TestRegexIsAcceptedWhereAReferenceIsRead locks the option's surface against
// the tree: every command that takes a positional reference accepts --regex, and
// no other command does. The arity table decides which is which: a positional
// "game" is a product reference, a positional "file" is a path and no argument
// is neither. A command that reads a reference but forgets the option fails here
// instead of at a user's terminal.
func TestRegexIsAcceptedWhereAReferenceIsRead(t *testing.T) {
	var walk func(path []string, nodes []commandNode)
	references, others := 0, 0
	walk = func(path []string, nodes []commandNode) {
		for _, n := range nodes {
			here := append(append([]string{}, path...), n.name)
			if len(n.children) != 0 && n.id == cmdNone {
				walk(here, n.children)
				continue
			}
			if n.id != cmdNone {
				argName, count := commandArity(n.id)
				readsReference := count != 0 && argName == "game"
				args := append([]string{}, here...)
				if count != 0 {
					args = append(args, "some_game")
				}
				args = append(args, "--regex")
				_, err := parseArgs(args, baseConfig())
				if readsReference {
					references++
					if err != nil {
						t.Errorf("%s --regex: %v, want a command that reads a reference to accept it",
							strings.Join(here, " "), err)
					}
				} else {
					others++
					if err == nil {
						t.Errorf("%s --regex: accepted, want a usage error", strings.Join(here, " "))
					}
				}
			}
			if len(n.children) != 0 {
				walk(here, n.children)
			}
		}
	}
	walk(nil, commandTree)
	if references < 2 || others < 2 {
		t.Fatalf("walked %d reference commands and %d others: the walk is wrong, not the option", references, others)
	}
}

// TestReferenceModeIsWhatTheFlagSays locks the two readings the flag selects: the
// product's own name by default, an expression under --regex.
func TestReferenceModeIsWhatTheFlagSays(t *testing.T) {
	if got := productRefMode(mustParse(t, "install", "some_game")); got != core.ProductRefExact {
		t.Errorf("install without --regex: mode = %v, want the exact read", got)
	}
	if got := productRefMode(mustParse(t, "install", "some_game", "--regex")); got != core.ProductRefRegex {
		t.Errorf("install --regex: mode = %v, want the expression read", got)
	}
}

// newReferenceFixture answers a session and an account with two products whose
// slugs share the word "Game", so the two readings of one reference differ
// observably: the exact read finds no product called "Game", the expression
// matches both and has to ask. It writes the credential file itself, because a
// reference is only read after a session exists.
func newReferenceFixture(t *testing.T) core.Dependencies {
	t.Helper()
	isolateRoots(t)
	home, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	dir := filepath.Join(home, config.ProgramName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	token := fmt.Sprintf(`{"access_token":"at","refresh_token":"rt","expires_at":%d,"user_id":"u1"}`,
		time.Now().Add(time.Hour).Unix())
	// Seeded through the seam, not by writing a file: the store's path and format
	// are the seam's business, and a test that hard-coded either would have to be
	// edited every time they change.
	seed, err := auth.Open(auth.StorePath(config.Config{ConfigDirectory: dir}))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	var fields map[string]any
	if err := jsonv2.Unmarshal([]byte(token), &fields); err != nil {
		t.Fatal(err)
	}
	seed.StoreLoginResponse(fields)
	if err := seed.Save(); err != nil {
		t.Fatalf("seed the session: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/builds"):
			fmt.Fprint(w, `{"items":[{"build_id":"b-one","version_name":"1.0.0",`+
				`"date_published":"2024-01-01","generation":2}]}`)
		case r.URL.Path == "/www/account":
			fmt.Fprint(w, "account")
		case r.URL.Path == "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["555","556"]}`)
		case r.URL.Path == "/www/account/getFilteredProducts":
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[`+
				`{"id":"555","slug":"Some Game A"},{"id":"556","slug":"Some Game B"}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	return core.Dependencies{HTTPTransport: &hostRedirectTransport{target: target}}
}

// TestReferenceReadReachesTheSelector locks the hop from the flag to the
// selector on every command that reads a reference: one argument resolves as a
// product's name without --regex and as an expression with it, which shows up as
// the different failure each reading reports. A dispatch that dropped the mode
// would answer both readings the same way.
func TestReferenceReadReachesTheSelector(t *testing.T) {
	// Each entry is a command that reads a reference, with the arguments its
	// arity needs. orphans remove is driven with --yes because the destructive
	// authorization is asked for before the walk, not after it.
	cases := [][]string{
		{"install", "Game"},
		{"verify", "Game"},
		{"orphans", "check", "Game"},
		{"orphans", "remove", "Game", "--yes"},
		{"backup", "list", "Game"},
		{"galaxy", "builds", "Game"},
		{"galaxy", "manifest", "Game"},
		{"galaxy", "cdns", "Game"},
		{"backup", "download", "Game"},
		{"backup", "download", "Game", "1"},
	}
	for _, base := range cases {
		t.Run(strings.Join(base, " "), func(t *testing.T) {
			deps := newReferenceFixture(t)

			_, _, errOut := runReference(t, deps, base...)
			if want := `no product named "Game"`; !strings.Contains(errOut, want) {
				t.Errorf("without --regex: stderr = %q, want it to report %q", errOut, want)
			}

			withRegex := append(append([]string{}, base...), "--regex")
			_, _, errOut = runReference(t, deps, withRegex...)
			if want := "Unable to read selection"; !strings.Contains(errOut, want) {
				t.Errorf("--regex: stderr = %q, want %q: the expression matched both products and had to ask",
					errOut, want)
			}
		})
	}
}

// TestExactReferenceResolvesThroughTheAccount locks the other half of the
// default read end to end: the product's own name is what reaches the work, so
// the command runs instead of failing to resolve.
func TestExactReferenceResolvesThroughTheAccount(t *testing.T) {
	deps := newReferenceFixture(t)

	code, _, errOut := runReference(t, deps, "backup", "list", "Some Game A")
	if code != 0 {
		t.Errorf("exit = %d, want 0 for a name the account holds:\n%s", code, errOut)
	}
}

// TestZeroMatchExitCodes locks the decided split between the two families: a
// reference that resolves to nothing is an operational failure for the commands
// that go on to do work, and the notice IS the answer for the commands that only
// display one. "There is nothing to show" and "the command could not do what it
// was asked" are different categories, so the exit codes differ on purpose.
func TestZeroMatchExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{args: []string{"install", "Game"}, want: 1},
		{args: []string{"verify", "Game"}, want: 1},
		{args: []string{"orphans", "check", "Game"}, want: 1},
		{args: []string{"orphans", "remove", "Game", "--yes"}, want: 1},
		{args: []string{"backup", "list", "Game"}, want: 1},
		{args: []string{"backup", "download", "Game"}, want: 1},
		{args: []string{"backup", "download", "Game", "1"}, want: 1},
		{args: []string{"galaxy", "builds", "Game"}, want: 0},
		{args: []string{"galaxy", "manifest", "Game"}, want: 0},
		{args: []string{"galaxy", "cdns", "Game"}, want: 0},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			deps := newReferenceFixture(t)
			code, _, errOut := runReference(t, deps, c.args...)
			if code != c.want {
				t.Errorf("exit = %d, want %d:\n%s", code, c.want, errOut)
			}
		})
	}
}

// TestSelectionFailureExitCodes locks the other half of the galaxy contract: a
// reference that matches several products and cannot be chosen is a FAILED
// command, so it exits 1. It is deliberately paired with TestZeroMatchExitCodes,
// which asserts exit 0 for a reference that matches nothing — the two together
// are what keep "nothing to show" and "could not choose" from collapsing back
// into one answer.
func TestSelectionFailureExitCodes(t *testing.T) {
	// The fixture holds two products whose slugs share the word "Game", so an
	// expression matching both cannot be resolved without a terminal.
	for _, args := range [][]string{
		{"galaxy", "builds", "Game", "--regex"},
		{"galaxy", "manifest", "Game", "--regex"},
		{"galaxy", "cdns", "Game", "--regex"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			deps := newReferenceFixture(t)
			code, _, errOut := runReference(t, deps, args...)
			if code != 1 {
				t.Errorf("exit = %d, want 1 when several products match and none can be chosen:\n%s", code, errOut)
			}
			// The wording is a secondary anchor — the exit code is the contract —
			// and it pins WHICH failure produced the code.
			if want := "Unable to read selection"; !strings.Contains(errOut, want) {
				t.Errorf("stderr = %q, want it to report %q", errOut, want)
			}
		})
	}
}

// TestShowExactNameResolvesAndExitsZero is this round's behaviour guard: only the
// selection FAILURE moved to exit 1, so an unambiguous name must still resolve,
// list and succeed.
func TestShowExactNameResolvesAndExitsZero(t *testing.T) {
	deps := newReferenceFixture(t)

	code, out, errOut := runReference(t, deps, "galaxy", "builds", "Some Game A")
	if code != 0 {
		t.Errorf("exit = %d, want 0 for a name the account holds:\n%s", code, errOut)
	}
	if !strings.Contains(out, "b-one") {
		t.Errorf("stdout = %q, want the build listing of the resolved product", out)
	}
}

// runReference drives one command line through the real dispatcher.
func runReference(t *testing.T, deps core.Dependencies, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := runWithDeps(args, strings.NewReader(""), &out, &errOut, deps)
	return code, out.String(), errOut.String()
}

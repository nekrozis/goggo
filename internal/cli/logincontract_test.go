package cli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/core"
)

// walkCommandTree visits every runnable node of the command tree. Three tests
// below walk the same tree; one visitor keeps the leaf/namespace rule (a node
// can be both, "download") in a single place.
func walkCommandTree(visit func(commandNode)) {
	var walk func([]commandNode)
	walk = func(nodes []commandNode) {
		for _, n := range nodes {
			if n.id != cmdNone {
				visit(n)
			}
			if len(n.children) != 0 {
				walk(n.children)
			}
		}
	}
	walk(commandTree)
}

// TestCommandTreeDeclaresASessionClass is the completeness guard of the login
// contract: every runnable leaf declares what it needs from
// the session, each declaration is the one the contract names, and a new command
// that forgets the field fails here instead of at run time — where the class
// would still be unset and sessionRequest would panic.
//
// This is also the home of the full commandID -> sessionClass mapping; the
// parser tests only sample it.
func TestCommandTreeDeclaresASessionClass(t *testing.T) {
	want := map[commandID]sessionClass{
		cmdAuthLogin:       sessionExplicitLogin,
		cmdAuthClear:       sessionNone,
		cmdAuthStatus:      sessionNone,
		cmdListGames:       sessionRequired,
		cmdListTags:        sessionRequired,
		cmdListWishlist:    sessionRequired,
		cmdGame:            sessionNone,
		cmdGalaxyBuilds:    sessionRequired,
		cmdGalaxyManifest:  sessionRequired,
		cmdGalaxyCDNs:      sessionRequired,
		cmdInstall:         sessionImplicitLogin,
		cmdInstallOptions:  sessionRequired,
		cmdVerify:          sessionRequired,
		cmdBackupList:      sessionRequired,
		cmdBackupDownload:  sessionImplicitLogin,
		cmdOrphansCheck:    sessionRequired,
		cmdOrphansRemove:   sessionImplicitLogin,
		cmdManifestInspect: sessionNone,
		cmdManifestVerify:  sessionNone,
		cmdManifestCreate:  sessionNone,
	}

	seen := map[commandID]bool{}
	walkCommandTree(func(n commandNode) {
		if n.session == sessionUnset {
			t.Errorf("%s does not declare a session class", n.name)
			return
		}
		seen[n.id] = true
		if got := want[n.id]; got != n.session {
			t.Errorf("%s session = %s, want %s", n.name, n.session, got)
		}
	})

	for id := range want {
		if !seen[id] {
			t.Errorf("%s is not in the command tree", id.path())
		}
	}
}

// TestParseCarriesTheDeclaredSessionClass locks the copy from the tree node into
// the invocation: the dispatcher acts on the declaration, so the value the
// parser hands over is part of the contract. One command per class is enough —
// the mapping itself is TestCommandTreeDeclaresASessionClass'.
func TestParseCarriesTheDeclaredSessionClass(t *testing.T) {
	cases := []struct {
		args []string
		want sessionClass
	}{
		{[]string{"auth", "login"}, sessionExplicitLogin},
		{[]string{"auth", "clear"}, sessionNone},
		{[]string{"list", "games"}, sessionRequired},
		{[]string{"install", "123"}, sessionImplicitLogin},
	}
	for _, c := range cases {
		if inv := parseOpts(t, c.args...); inv.session != c.want {
			t.Errorf("parseOpts(%v).session = %s, want %s", c.args, inv.session, c.want)
		}
	}
}

// TestSessionRequestMatrix locks the mapping the dispatcher uses, class by class
// and terminal by terminal. It is the table form of the
// contract: an implicit login is the only one conditioned on the terminal, and
// the two permissions never collapse into one another.
func TestSessionRequestMatrix(t *testing.T) {
	cases := []struct {
		class       sessionClass
		interactive bool
		want        core.SessionRequest
	}{
		{sessionNone, true, core.SessionRequest{}},
		{sessionNone, false, core.SessionRequest{}},
		{sessionRequired, true, core.SessionRequest{Required: true}},
		{sessionRequired, false, core.SessionRequest{Required: true}},
		{sessionImplicitLogin, true, core.SessionRequest{Required: true, AllowLogin: true}},
		{sessionImplicitLogin, false, core.SessionRequest{Required: true}},
		{sessionExplicitLogin, true, core.SessionRequest{AllowLogin: true}},
		{sessionExplicitLogin, false, core.SessionRequest{AllowLogin: true}},
	}
	for _, c := range cases {
		if got := sessionRequest(c.class, c.interactive); got != c.want {
			t.Errorf("sessionRequest(%s, interactive=%v) = %+v, want %+v",
				c.class, c.interactive, got, c.want)
		}
	}
}

// TestSessionRequestPanicsOnAnUndeclaredClass locks the defensive half of that
// mapping: an undeclared class is a broken tree, and silently
// turning it into "no session needed" would let a command that needs the account
// run unauthenticated.
func TestSessionRequestPanicsOnAnUndeclaredClass(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("sessionRequest(sessionUnset, ...) did not panic")
		}
	}()
	sessionRequest(sessionUnset, true)
}

// TestOnlyWritingCommandsMayLogIn states the contract as a property of the whole
// tree rather than as individual mappings: outside the
// downloading commands (install, backup download), the destructive
// orphans remove and an explicit login, no command may ever be handed a request
// that lets it log in.
func TestOnlyWritingCommandsMayLogIn(t *testing.T) {
	may := map[commandID]bool{
		cmdInstall:        true,
		cmdBackupDownload: true,
		cmdOrphansRemove:  true,
		cmdAuthLogin:      true,
	}
	walkCommandTree(func(n commandNode) {
		if may[n.id] {
			return
		}
		for _, interactive := range []bool{true, false} {
			if req := sessionRequest(n.session, interactive); req.AllowLogin {
				t.Errorf("%s may log in (interactive=%v): it does not write and is not a login",
					n.name, interactive)
			}
		}
	})
}

// --- the status report's two-session model ---

// TestAuthStatusReportsSessionsSeparately locks the frozen table: the website
// observation and the API credential observation are separate lines, the
// first line never changes, no exit code changes, and the command never
// probes the API.
func TestAuthStatusReportsSessionsSeparately(t *testing.T) {
	t.Run("expired and refresh refused: not logged in, degraded", func(t *testing.T) {
		sentinelRoots(t, sentinelExpiry(true))
		f := newSentinelFixture(t)
		f.setTokenStatus(http.StatusBadGateway)
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, sentinelDeps(t, f))
		if code != 1 {
			t.Errorf("exit = %d, want 1 (unchanged by this round)", code)
		}
		out := stdout.String()
		if !strings.HasPrefix(out, "Login status: Not logged in\n") {
			t.Errorf("first line changed:\n%s", out)
		}
		if !strings.Contains(out, "API session: degraded (refresh failed: ") {
			t.Errorf("output lacks the degraded explanation:\n%s", out)
		}
		if f.hits("/token") == 0 {
			t.Error("the refresh was never attempted: the degraded line must explain a real failure")
		}
	})

	t.Run("live credential: logged in, API unknown", func(t *testing.T) {
		sentinelRoots(t, sentinelExpiry(false))
		f := newSentinelFixture(t)
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, sentinelDeps(t, f))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
		}
		want := "Login status: Logged in\nAPI session: unknown\n"
		if stdout.String() != want {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
	})

	t.Run("empty store: not logged in, no second line", func(t *testing.T) {
		isolateRoots(t)
		// No credentials file at all: nothing was renewed, so nothing failed —
		// the degraded line must not leak onto this path.
		f := newSentinelFixture(t)
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, sentinelDeps(t, f))
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if got := stdout.String(); got != "Login status: Not logged in\n" {
			t.Errorf("stdout = %q, want the bare first line", got)
		}
	})
}

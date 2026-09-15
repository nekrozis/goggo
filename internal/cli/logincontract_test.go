package cli

import (
	"testing"

	"github.com/nekrozis/goggo/internal/core"
)

// TestCommandTreeDeclaresASessionClass is the completeness guard of the login
// contract (review CLI1 §7, S4): every runnable leaf declares what it needs from
// the session, each declaration is the one the contract names, and a new command
// that forgets the field fails here instead of at run time — where the class
// would still be unset and sessionRequest would panic.
func TestCommandTreeDeclaresASessionClass(t *testing.T) {
	want := map[commandID]sessionClass{
		cmdAuthLogin:     sessionExplicitLogin,
		cmdAuthLogout:    sessionNone,
		cmdAuthStatus:    sessionNone,
		cmdListGames:     sessionRequired,
		cmdListTags:      sessionRequired,
		cmdListWishlist:  sessionRequired,
		cmdListDetails:   sessionRequired,
		cmdListJSON:      sessionRequired,
		cmdShowBuilds:    sessionRequired,
		cmdShowManifest:  sessionRequired,
		cmdShowCDNs:      sessionRequired,
		cmdInstall:       sessionImplicitLogin,
		cmdVerify:        sessionRequired,
		cmdDownload:      sessionImplicitLogin,
		cmdDownloadFile:  sessionImplicitLogin,
		cmdOrphansCheck:  sessionRequired,
		cmdOrphansRemove: sessionImplicitLogin,
	}

	seen := map[commandID]bool{}
	var walk func([]commandNode)
	walk = func(nodes []commandNode) {
		for _, n := range nodes {
			// A node can be leaf and namespace at once ("download", GD4
			// ruling 9): the leaf check and the recursion are independent.
			if n.id != cmdNone {
				if n.session == sessionUnset {
					t.Errorf("%s does not declare a session class", n.name)
					continue
				}
				seen[n.id] = true
				if got := want[n.id]; got != n.session {
					t.Errorf("%s session = %s, want %s", n.name, n.session, got)
				}
			}
			if len(n.children) != 0 {
				walk(n.children)
			}
		}
	}
	walk(commandTree)

	for id := range want {
		if !seen[id] {
			t.Errorf("%s is not in the command tree", id.path())
		}
	}
}

// TestParseCarriesTheDeclaredSessionClass locks the copy from the tree node into
// the invocation: the dispatcher acts on the declaration, so the value the
// parser hands over is part of the contract (review S4).
func TestParseCarriesTheDeclaredSessionClass(t *testing.T) {
	cases := []struct {
		args []string
		want sessionClass
	}{
		{[]string{"auth", "login"}, sessionExplicitLogin},
		{[]string{"auth", "logout"}, sessionNone},
		{[]string{"auth", "status"}, sessionNone},
		{[]string{"list", "games"}, sessionRequired},
		{[]string{"list", "wishlist"}, sessionRequired},
		{[]string{"show", "manifest", "123"}, sessionRequired},
		{[]string{"install", "123"}, sessionImplicitLogin},
		{[]string{"verify", "123"}, sessionRequired},
		{[]string{"orphans", "check", "123"}, sessionRequired},
		{[]string{"orphans", "remove", "123", "--yes"}, sessionImplicitLogin},
	}
	for _, c := range cases {
		if inv := parseOpts(t, c.args...); inv.session != c.want {
			t.Errorf("parseOpts(%v).session = %s, want %s", c.args, inv.session, c.want)
		}
	}
}

// TestSessionRequestMatrix locks the mapping the dispatcher uses, class by class
// and terminal by terminal (review CLI1 §7, S4). It is the table form of the
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
// mapping (review S4): an undeclared class is a broken tree, and silently
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
// tree rather than as individual mappings (review CLI1 §7): outside the
// downloading commands (install, download, download file), the destructive
// orphans remove and an explicit login, no command may ever be handed a request
// that lets it log in.
func TestOnlyWritingCommandsMayLogIn(t *testing.T) {
	may := map[commandID]bool{
		cmdInstall:       true,
		cmdDownload:      true,
		cmdDownloadFile:  true,
		cmdOrphansRemove: true,
		cmdAuthLogin:     true,
	}
	var walk func([]commandNode)
	walk = func(nodes []commandNode) {
		for _, n := range nodes {
			if n.id != cmdNone && !may[n.id] {
				for _, interactive := range []bool{true, false} {
					if req := sessionRequest(n.session, interactive); req.AllowLogin {
						t.Errorf("%s may log in (interactive=%v): it does not write and is not a login",
							n.name, interactive)
					}
				}
			}
			if len(n.children) != 0 {
				walk(n.children)
			}
		}
	}
	walk(commandTree)
}

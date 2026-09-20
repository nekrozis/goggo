package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// The console is the front end internal/core talks to; this assertion turns the
// structural contract into a compile-time one.
var _ core.Console = (*console)(nil)

// TestPromptPasswordWithoutTerminal covers the injected-reader branch: with no
// terminal to hide behind the line is read normally, which is what keeps the
// front end testable. The hidden path (term.ReadPassword) needs a real console,
// so it is not covered here.
func TestPromptPasswordWithoutTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader("secret\n"), &out, &errOut)
	got, err := ui.PromptPassword()
	if err != nil {
		t.Fatalf("PromptPassword: %v", err)
	}
	if got != "secret" {
		t.Errorf("password = %q, want %q", got, "secret")
	}
	if !strings.Contains(errOut.String(), "Password: ") {
		t.Errorf("prompt output = %q, want it on stderr", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// TestPromptEmailWritesToStderr locks the prompt wording and its stream: every
// prompt goes to stderr so stdout stays program output.
func TestPromptEmailWritesToStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader("user@example.com\n"), &out, &errOut)
	got, err := ui.PromptEmail()
	if err != nil {
		t.Fatalf("PromptEmail: %v", err)
	}
	if got != "user@example.com" {
		t.Errorf("email = %q, want the typed address", got)
	}
	if !strings.Contains(errOut.String(), "Email: ") {
		t.Errorf("prompt output = %q, want it on stderr", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// browserBlockRE matches the ordered four-line browser block: the URL line, the
// blank line after it, the paste instruction and the `URL: ` prompt. A partial
// move would fail here.
func browserBlockRE(t *testing.T, browserURL string) *regexp.Regexp {
	t.Helper()
	return regexp.MustCompile(`^Login using browser at the following url\n` +
		regexp.QuoteMeta(browserURL) + `\n\n` +
		`Copy & paste the full url from your browser here after login is complete\n` +
		`URL: `)
}

// TestResolveChallengeWording locks the wording the console uses for the two
// interactive login outcomes, and the fact that the answer is handed to webapi.
//
// The challenges are built by hand, so webapi rejects them with
// ErrChallengeClientMismatch before any network work — that rejection is the
// proof that the console passed the answer on. The prompt itself is printed
// first, which is what this test is about.
func TestResolveChallengeWording(t *testing.T) {
	hx, err := httpx.New(httpx.Config{})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := auth.Open("")
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	web, err := webapi.New(hx, store)
	if err != nil {
		t.Fatalf("webapi.New: %v", err)
	}

	const browserURL = "https://auth.gog.com/auth?client_id=x&redirect_uri=y"
	cases := []struct {
		name     string
		ch       *webapi.LoginChallenge
		stdin    string
		wantOut  string
		wantBody string
	}{
		{
			name: "second-step code", stdin: "1234\n",
			ch:      &webapi.LoginChallenge{Kind: webapi.ChallengeTwoFactor, CodeLength: 4},
			wantOut: "Security code: ",
		},
		{
			// The 6-character authenticator code is asked for differently;
			// webapi exposes only the length, so this case is what pins the
			// wording branch.
			name: "authenticator code", stdin: "123456\n",
			ch:      &webapi.LoginChallenge{Kind: webapi.ChallengeTwoFactor, CodeLength: 6},
			wantOut: "Authenticator security code: ",
		},
		{
			name: "browser callback", stdin: "https://auth.gog.com/callback?code=abc\n",
			ch:       &webapi.LoginChallenge{Kind: webapi.ChallengeBrowser, BrowserURL: browserURL},
			wantBody: browserURL,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			ui := newConsole(strings.NewReader(c.stdin), &out, &errOut)

			err := ui.ResolveChallenge(context.Background(), web, c.ch)
			if !strings.Contains(errOut.String(), c.wantOut) {
				t.Errorf("stderr = %q, want it to carry %q", errOut.String(), c.wantOut)
			}
			if c.wantBody != "" && !browserBlockRE(t, c.wantBody).MatchString(errOut.String()) {
				t.Errorf("browser block is not the ordered block: %q", errOut.String())
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty: prompts belong on stderr", out.String())
			}
			if err == nil {
				t.Fatal("a hand-built challenge must be refused by webapi, which proves the answer was handed on")
			}
		})
	}
}

// TestSelectProductWithoutTerminal locks the listing the user is shown when the
// selection cannot be answered: the candidates are printed and only THEN is the
// terminal checked, so a non-interactive run still sees them. The candidates go
// to stdout and the prompt would go to stderr.
//
// Coverage boundary: the interactive loop needs a real terminal, so the index it
// returns is covered in internal/core through the Console double.
func TestSelectProductWithoutTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader("0\n"), &out, &errOut)

	index, err := ui.SelectProduct([]string{"Some Game", "Another Game"})
	if err == nil {
		t.Fatalf("SelectProduct returned index %d, want an error without a terminal", index)
	}
	wantOut := "Select product:\n0: Some Game\n1: Another Game\n"
	if out.String() != wantOut {
		t.Errorf("stdout = %q, want the candidate list %q", out.String(), wantOut)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty: the prompt was never reached", errOut.String())
	}
}

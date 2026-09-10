package cli

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/term"

	"github.com/nekrozis/goggo/internal/webapi"
)

// totpCodeLength is the authenticator (TOTP) one-time code length; the other
// flavour is the 4-character second-step code (website.cpp:515-525). webapi
// deliberately hides which flavour it is (the distinction is a protocol detail)
// and exposes only CodeLength, so the wording is chosen from the length.
const totpCodeLength = 6

// Every prompt in this file writes to stderr, mirroring the C++ source, which
// uses std::cerr for all interactive input (downloader.cpp:267,270,
// website.cpp:519,524,614-617). stdout therefore carries program output only,
// which is what makes `goggo --list games > file` usable.

// PromptEmail asks for the account e-mail on the injected streams.
func (c *console) PromptEmail() (string, error) {
	fmt.Fprint(c.errOut, "Email: ")
	email, err := c.readLine()
	if err != nil {
		return "", fmt.Errorf("read email: %w", err)
	}
	return email, nil
}

// PromptPassword asks for the account password.
//
// When the input is a real terminal the typed characters are hidden, using
// golang.org/x/term (the only platform-portable way to do so). With an injected
// reader — tests, pipes, redirection — there is no terminal to hide behind, so
// the line is read normally and stays visible; that fallback is what keeps the
// front end testable.
//
// Abort limitation (documented, not hardened): term.ReadPassword restores the
// terminal mode that was saved when it started. A Ctrl+C delivered in the
// middle of the read can leave the console with echo switched off, because the
// saved mode is the one that was already in effect. The restore path is
// unchanged from the standard library's behaviour; see the audit note.
func (c *console) PromptPassword() (string, error) {
	if fd, ok := c.terminalFd(); ok {
		fmt.Fprint(c.errOut, "Password: ")
		secret, err := term.ReadPassword(fd)
		// The hidden read consumes the newline, so the terminal needs one back.
		fmt.Fprintln(c.errOut)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(string(secret), "\r\n"), nil
	}
	fmt.Fprint(c.errOut, "Password: ")
	password, err := c.readLine()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return password, nil
}

// ResolveChallenge finishes an interactive login: it prints what the user has
// to do, reads the answer and hands it back to webapi. The challenge itself is
// consumed exactly once by webapi, so a failure here is reported rather than
// retried with the same challenge.
//
// The wording is the C++ one: "Security code: " for the second-step code and
// "Authenticator security code: " for the TOTP code (website.cpp:519,524), and
// the four-line browser block (website.cpp:614-617).
func (c *console) ResolveChallenge(ctx context.Context, web *webapi.Client, ch *webapi.LoginChallenge) error {
	switch ch.Kind {
	case webapi.ChallengeTwoFactor:
		prompt := "Security code: "
		if ch.CodeLength == totpCodeLength {
			prompt = "Authenticator security code: "
		}
		fmt.Fprint(c.errOut, prompt)
		code, err := c.readLine()
		if err != nil {
			return fmt.Errorf("read security code: %w", err)
		}
		if err := web.ContinueLogin(ctx, ch, strings.TrimSpace(code)); err != nil {
			return err
		}
	case webapi.ChallengeBrowser:
		fmt.Fprintln(c.errOut, "Login using browser at the following url")
		fmt.Fprintln(c.errOut, ch.BrowserURL)
		fmt.Fprintln(c.errOut)
		fmt.Fprintln(c.errOut, "Copy & paste the full url from your browser here after login is complete")
		fmt.Fprint(c.errOut, "URL: ")
		callback, err := c.readLine()
		if err != nil {
			return fmt.Errorf("read callback URL: %w", err)
		}
		if err := web.ContinueLogin(ctx, ch, strings.TrimSpace(callback)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported login challenge %v", ch.Kind)
	}
	return nil
}

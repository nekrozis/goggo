package cli

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/term"

	"github.com/nekrozis/goggo/internal/webapi"
)

// promptEmail asks for the account e-mail on the injected streams.
func (c *console) promptEmail() (string, error) {
	fmt.Fprint(c.out, "Email: ")
	email, err := c.readLine()
	if err != nil {
		return "", fmt.Errorf("read email: %w", err)
	}
	return email, nil
}

// promptPassword asks for the account password.
//
// When the input is a real terminal the typed characters are hidden, using
// golang.org/x/term (the only platform-portable way to do so). With an injected
// reader — tests, pipes, redirection — there is no terminal to hide behind, so
// the line is read normally and stays visible; that fallback is what keeps the
// front end testable.
func (c *console) promptPassword() (string, error) {
	if f, ok := c.rawIn.(interface{ Fd() uintptr }); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(c.out, "Password: ")
		secret, err := term.ReadPassword(int(f.Fd()))
		// The hidden read consumes the newline, so the terminal needs one back.
		fmt.Fprintln(c.out)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(string(secret), "\r\n"), nil
	}
	fmt.Fprint(c.out, "Password: ")
	password, err := c.readLine()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return password, nil
}

// resolveChallenge finishes an interactive login: it prints what the user has
// to do, reads the answer and hands it back to webapi. The challenge itself is
// consumed exactly once by webapi, so a failure here is reported rather than
// retried with the same challenge.
func (c *console) resolveChallenge(ctx context.Context, web *webapi.Client, ch *webapi.LoginChallenge) error {
	switch ch.Kind {
	case webapi.ChallengeTwoFactor:
		fmt.Fprintf(c.out, "Enter the %d-character security code: ", ch.CodeLength)
		code, err := c.readLine()
		if err != nil {
			return fmt.Errorf("read security code: %w", err)
		}
		if err := web.ContinueLogin(ctx, ch, strings.TrimSpace(code)); err != nil {
			return err
		}
	case webapi.ChallengeBrowser:
		fmt.Fprintf(c.out, "Open this URL in a browser and finish the login:\n%s\n", ch.BrowserURL)
		fmt.Fprint(c.out, "Paste the callback URL: ")
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

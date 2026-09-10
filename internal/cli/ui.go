package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/webapi"
)

// promptCredentials asks for the account credentials on the injected streams.
// The C++ source prompts on std::cin when --login-email/--login-password are
// not both set (downloader.cpp:249-276).
func (c *console) promptCredentials() (email, password string, err error) {
	fmt.Fprint(c.out, "Email: ")
	email, err = c.readLine()
	if err != nil {
		return "", "", fmt.Errorf("read email: %w", err)
	}
	fmt.Fprint(c.out, "Password: ")
	password, err = c.readLine()
	if err != nil {
		return "", "", fmt.Errorf("read password: %w", err)
	}
	return email, password, nil
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

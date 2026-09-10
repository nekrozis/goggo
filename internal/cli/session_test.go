package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
)

// TestTransportOwnedByCallerPersistsCookies locks the S12-R ownership rule: the
// transport is created (and therefore owned) by the caller, and the jar that
// carried a session cookie is the one SaveCookies writes and LoadCookies
// restores.
//
// Coverage boundary (recorded in the audit): the cookie is set by a direct
// request rather than "during a webapi call", because webapi's endpoints have
// no injection point. The property being tested — one transport, one jar, one
// persistence path — is what the ownership change guarantees; the end-to-end
// form is verified by GATE-A.
func TestTransportOwnedByCallerPersistsCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc", Path: "/"})
		default:
			if _, err := r.Cookie("SID"); err == nil {
				fmt.Fprint(w, "with-cookie")
				return
			}
			fmt.Fprint(w, "no-cookie")
		}
	}))
	defer srv.Close()

	cookieFile := filepath.Join(t.TempDir(), "cookies.txt")

	// First transport: receives the cookie, then persists the jar.
	first, err := httpx.New(httpx.Config{CookieFile: cookieFile})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	if _, err := first.Get(context.Background(), srv.URL+"/set"); err != nil {
		t.Fatalf("Get(/set): %v", err)
	}
	if _, err := first.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}

	// Second transport: fresh, empty jar restored from the same file.
	second, err := httpx.New(httpx.Config{CookieFile: cookieFile})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	if err := second.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	resp, err := second.Get(context.Background(), srv.URL+"/check")
	if err != nil {
		t.Fatalf("Get(/check): %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "with-cookie" {
		t.Errorf("body = %q, want the cookie to be sent by the restored jar", body)
	}
}

// TestCredentialsPromptOnDemand locks the credentialed prompting rule: only the
// values that were not supplied are asked for (intentional difference from the
// C++ behaviour of prompting for both unless both flags are set).
func TestCredentialsPromptOnDemand(t *testing.T) {
	cases := []struct {
		name     string
		email    string
		password string
		stdin    string
		want     [2]string
		wantOut  []string
		denyOut  []string
	}{
		{
			name: "email flag only", email: "a@b", stdin: "pw\n",
			want:    [2]string{"a@b", "pw"},
			wantOut: []string{"Password: "},
			denyOut: []string{"Email: "},
		},
		{
			name: "both flags", email: "a@b", password: "pw",
			want:    [2]string{"a@b", "pw"},
			denyOut: []string{"Email: ", "Password: "},
		},
		{
			name: "no flags", stdin: "a@b\npw\n",
			want:    [2]string{"a@b", "pw"},
			wantOut: []string{"Email: ", "Password: "},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			ui := newConsole(strings.NewReader(c.stdin), &out, io.Discard)
			cfg := config.NewConfig("/cfg", "/cache")
			cfg.Email, cfg.Password = c.email, c.password

			email, password, err := credentials(cfg, ui)
			if err != nil {
				t.Fatalf("credentials: %v", err)
			}
			if email != c.want[0] || password != c.want[1] {
				t.Errorf("credentials = %q/%q, want %q/%q", email, password, c.want[0], c.want[1])
			}
			for _, want := range c.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("prompt output %q must contain %q", out.String(), want)
				}
			}
			for _, deny := range c.denyOut {
				if strings.Contains(out.String(), deny) {
					t.Errorf("prompt output %q must not contain %q", out.String(), deny)
				}
			}
		})
	}
}

// TestPromptPasswordWithoutTerminal covers the injected-reader branch: with no
// terminal to hide behind the line is read normally, which is what keeps the
// front end testable. The hidden path (term.ReadPassword) needs a real console
// and is verified by the GATE-A run on Windows.
func TestPromptPasswordWithoutTerminal(t *testing.T) {
	var out bytes.Buffer
	ui := newConsole(strings.NewReader("secret\n"), &out, io.Discard)
	got, err := ui.promptPassword()
	if err != nil {
		t.Fatalf("promptPassword: %v", err)
	}
	if got != "secret" {
		t.Errorf("password = %q, want %q", got, "secret")
	}
	if !strings.Contains(out.String(), "Password: ") {
		t.Errorf("prompt output = %q", out.String())
	}
}

// TestEnsureDirectories locks the startup directory creation that the first
// real login proved was missing (token persistence failed on a non-existent
// %AppData%\goggo).
func TestEnsureDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.NewConfig(filepath.Join(root, "conf"), filepath.Join(root, "cache"))
	if err := ensureDirectories(cfg); err != nil {
		t.Fatalf("ensureDirectories: %v", err)
	}
	for _, dir := range []string{cfg.ConfigDirectory, cfg.CacheDirectory, cfg.XMLDirectory} {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			t.Errorf("directory %q not created: %v", dir, err)
		}
	}
	// Idempotent: running it again must succeed.
	if err := ensureDirectories(cfg); err != nil {
		t.Errorf("second ensureDirectories: %v", err)
	}

	// A configuration root whose parent is a regular file cannot be created.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := config.NewConfig(blocker, filepath.Join(root, "cache2"))
	err := ensureDirectories(bad)
	if err == nil {
		t.Fatal("ensureDirectories must fail when a parent path is a file")
	}
	// The wrapped error names the directory it tried to create; compare on the
	// caller's root so the assertion stays platform-independent (the joined path
	// uses forward slashes while filepath.Join would not).
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error %q should carry the directory path", err)
	}
}

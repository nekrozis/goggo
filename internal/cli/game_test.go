package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	jsonv2 "encoding/json/v2"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/model"
)

const fixtureGameJSON = `{
	"id": 1207658991,
	"title": "Worms United",
	"slug": "worms_united",
	"release_date": "2012-01-10T12:57:00+08:00",
	"content_system_compatibility": {
		"windows": true,
		"osx": false,
		"linux": false
	},
	"images": {
		"icon": "//images-1.gog-statics.com/icon.png",
		"logo": "//images-3.gog-statics.com/logo_glx_logo.jpg"
	},
	"description": {
		"lead": "A turn-based strategy classic with worms."
	},
	"expanded_dlcs": [
		{"id": "2001", "slug": "reinforcements", "title": "Worms Reinforcements"}
	]
}`

func newGameFixture(t *testing.T, authenticated bool) core.Dependencies {
	t.Helper()
	isolateRoots(t)

	if authenticated {
		cfg, err := newConfig()
		if err != nil {
			t.Fatalf("newConfig: %v", err)
		}
		if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		seed, err := auth.Open(auth.StorePath(cfg))
		if err != nil {
			t.Fatalf("auth.Open: %v", err)
		}
		const token = `{"access_token":"at","refresh_token":"rt","expires_in":3600,"user_id":"u1"}`
		var fields map[string]any
		if err := jsonv2.Unmarshal([]byte(token), &fields); err != nil {
			t.Fatal(err)
		}
		seed.StoreLoginResponse(fields)
		if err := seed.Save(); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/products/1207658991"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, fixtureGameJSON)
		case strings.Contains(r.URL.Path, "/products/999999999"):
			http.Error(w, `{"message":"Product not found"}`, http.StatusNotFound)
		case r.URL.Path == "/www/account":
			fmt.Fprint(w, "account")
		case r.URL.Path == "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["1207658991"]}`)
		case r.URL.Path == "/www/account/getFilteredProducts":
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[{"id":"1207658991","slug":"worms_united"}]}`)
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

func TestGameNumericUnauthenticated(t *testing.T) {
	deps := newGameFixture(t, false)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "1207658991"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("goggo game 1207658991 exited %d, stderr: %s", code, errOut.String())
	}
	res := out.String()
	for _, want := range []string{
		"Title:        Worms United",
		"Product ID:   1207658991",
		"Slug:         worms_united",
		"Platforms:    Windows",
		"Icon:         https://images-1.gog-statics.com/icon.png",
		"Logo:         https://images-3.gog-statics.com/logo.jpg",
		"Release date: 2012-01-10",
		"DLCs:         Worms Reinforcements",
		"Description:  A turn-based strategy classic with worms.",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("stdout missing %q, got:\n%s", want, res)
		}
	}
}

func TestGameSlugUnauthenticated(t *testing.T) {
	deps := newGameFixture(t, false)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "worms_united"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 1 {
		t.Fatalf("goggo game worms_united unauthenticated exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "login required") {
		t.Errorf("stderr = %q, want it to report login required", errOut.String())
	}
}

func TestGameSlugAuthenticated(t *testing.T) {
	deps := newGameFixture(t, true)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "worms_united"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("goggo game worms_united exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Worms United") {
		t.Errorf("stdout = %q, want Title: Worms United", out.String())
	}
}

func TestGameSlugAuthenticatedUnknown(t *testing.T) {
	deps := newGameFixture(t, true)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "does_not_exist"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 1 {
		t.Fatalf("goggo game does_not_exist exit = %d, want 1", code)
	}
	if strings.Contains(errOut.String(), "login required") {
		t.Errorf("stderr = %q, should NOT report login required when authenticated", errOut.String())
	}
	if !strings.Contains(errOut.String(), "does_not_exist") {
		t.Errorf("stderr = %q, want it to report game not found for does_not_exist", errOut.String())
	}
}

func TestGameJSONOutput(t *testing.T) {
	deps := newGameFixture(t, false)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "1207658991", "--json"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("goggo game 1207658991 --json exit = %d, stderr: %s", code, errOut.String())
	}

	var info model.ProductInfo
	if err := jsonv2.Unmarshal([]byte(out.String()), &info); err != nil {
		t.Fatalf("unmarshal game --json output: %v\nOutput: %s", err, out.String())
	}

	if info.ID != "1207658991" || info.Title != "Worms United" || info.Slug != "worms_united" {
		t.Errorf("parsed ProductInfo mismatch: %+v", info)
	}
	if len(info.Platforms) != 1 || info.Platforms[0] != "Windows" {
		t.Errorf("platforms = %v, want [Windows]", info.Platforms)
	}
	if len(info.DLCs) != 1 || info.DLCs[0].Title != "Worms Reinforcements" {
		t.Errorf("dlcs = %+v", info.DLCs)
	}
}

func TestGameUnknownProduct(t *testing.T) {
	deps := newGameFixture(t, false)
	var out, errOut strings.Builder
	code := runWithDeps([]string{"game", "999999999"}, strings.NewReader(""), &out, &errOut, deps)
	if code != 1 {
		t.Errorf("exit = %d, want 1 for 404 product", code)
	}
}

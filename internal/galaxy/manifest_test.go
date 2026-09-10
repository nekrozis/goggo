package galaxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// requestRecordingServer answers every request with body and records the
// request URI the handler saw.
func requestRecordingServer(t *testing.T, body string) (*httptest.Server, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RequestURI()
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// TestManifestV1UsesGivenURLVerbatim locks galaxyapi.cpp:204-207: the URL comes
// from the builds document (items[].link) and is fetched unchanged, ".json"
// suffix included. It is the only generation-1 overload that upstream calls.
func TestManifestV1UsesGivenURLVerbatim(t *testing.T) {
	const body = `{"depot":{"items":[]}}`
	srv, got := requestRecordingServer(t, body)
	want := "/content-system/v1/manifests/1207659150/windows/54321/repository.json"

	cl := newTestClient(t, srv, nil)
	doc, err := cl.ManifestV1(context.Background(), srv.URL+want)
	if err != nil {
		t.Fatalf("ManifestV1: %v", err)
	}
	if *got != want {
		t.Errorf("request URI = %q, want %q", *got, want)
	}
	if _, ok := doc["depot"]; !ok {
		t.Errorf("decoded document = %v, want the depot member", doc)
	}
}

// TestManifestV2URL locks the URL construction of galaxyapi.cpp:209-221,
// including the two things that differ from generation 1: no ".json" suffix and
// no query string.
func TestManifestV2URL(t *testing.T) {
	const sha1 = "abcdef0123456789abcdef0123456789abcdef01"
	cases := []struct {
		name         string
		hash         string
		isDependency bool
		wantURI      string
	}{
		{
			name:    "bare hash is expanded",
			hash:    sha1,
			wantURI: "/content-system/v2/meta/ab/cd/" + sha1,
		},
		{
			name:    "hash with a slash is used as given",
			hash:    "ab/cd/" + sha1,
			wantURI: "/content-system/v2/meta/ab/cd/" + sha1,
		},
		{
			name:    "empty hash is not expanded",
			hash:    "",
			wantURI: "/content-system/v2/meta/",
		},
		{
			name:         "dependency repository",
			hash:         sha1,
			isDependency: true,
			wantURI:      "/content-system/v2/dependencies/meta/ab/cd/" + sha1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, got := requestRecordingServer(t, `{"depot":{}}`)

			cl := newTestClient(t, srv, nil)
			if _, err := cl.ManifestV2(context.Background(), c.hash, c.isDependency); err != nil {
				t.Fatalf("ManifestV2: %v", err)
			}
			if *got != c.wantURI {
				t.Errorf("request URI = %q, want %q", *got, c.wantURI)
			}
			if strings.Contains(*got, ".json") {
				t.Errorf("request URI = %q must not carry a .json suffix", *got)
			}
			if strings.Contains(*got, "?") {
				t.Errorf("request URI = %q must not carry a query string", *got)
			}
		})
	}
}

// TestHashToGalaxyPath locks the path layout of galaxyapi.cpp:244-251 and the
// boundary Go has to define for it: the C++ version slices unconditionally, so a
// hash shorter than four characters is undefined there. It comes back unchanged
// instead of panicking, and no error channel is added for it.
func TestHashToGalaxyPath(t *testing.T) {
	const sha1 = "abcdef0123456789abcdef0123456789abcdef01"
	cases := []struct {
		name string
		hash string
		want string
	}{
		{"sha1 hash", sha1, "ab/cd/" + sha1},
		{"already a path", "ab/cd/" + sha1, "ab/cd/" + sha1},
		{"empty", "", ""},
		{"one character", "a", "a"},
		{"two characters", "ab", "ab"},
		{"three characters", "abc", "abc"},
		{"exactly four", "abcd", "ab/cd/abcd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hashToGalaxyPath(c.hash); got != c.want {
				t.Errorf("hashToGalaxyPath(%q) = %q, want %q", c.hash, got, c.want)
			}
		})
	}
}

// TestManifestBearerSanity: manifests go out on the same authenticated
// primitive as builds. The three-state bearer rule itself is covered by S13;
// this only checks the manifest path is not accidentally unauthenticated.
func TestManifestBearerSanity(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"depot":{}}`)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, map[string]any{"access_token": "tok", "expires_in": 3600})
	if _, err := cl.ManifestV2(context.Background(), "abcd", false); err != nil {
		t.Fatalf("ManifestV2: %v", err)
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want the bearer token", auth)
	}
}

// TestManifestNonObjectIsErrNotJSON: the shape contract is the one S13
// established — a manifest that is not a JSON object is ErrNotJSON.
func TestManifestNonObjectIsErrNotJSON(t *testing.T) {
	srv, _ := requestRecordingServer(t, `[{"depot":{}}]`)

	cl := newTestClient(t, srv, nil)
	if _, err := cl.ManifestV2(context.Background(), "abcd", false); !errors.Is(err, ErrNotJSON) {
		t.Errorf("ManifestV2 = %v, want ErrNotJSON", err)
	}
}

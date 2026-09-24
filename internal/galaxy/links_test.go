package galaxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/httpx"
)

// depsCall is one request the dependencies flow made.
type depsCall struct {
	uri  string
	auth string
}

// depsServer serves the two-step dependencies flow: /dependencies/repository is
// answered by repository, /manifest.json by manifest, everything else is a 404.
// It records every request in order.
func depsServer(t *testing.T, repository, manifest http.HandlerFunc) (*httptest.Server, *[]depsCall) {
	t.Helper()
	var mu sync.Mutex
	var seen []depsCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, depsCall{uri: r.URL.RequestURI(), auth: r.Header.Get("Authorization")})
		mu.Unlock()
		switch r.URL.Path {
		case "/dependencies/repository":
			repository(w, r)
		case "/manifest.json":
			manifest(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// jsonBody answers with a fixed JSON document.
func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }
}

// TestSecureLinkURL locks the request of
// no-encoding rule: a url.Values-based query would have produced "path=%2F",
// so asserting the literal "/" is what proves the path goes out as given.
func TestSecureLinkURL(t *testing.T) {
	var gotURI, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		gotPath = r.URL.Query().Get("path")
		fmt.Fprint(w, `{"urls":[]}`)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	doc, err := cl.SecureLink(context.Background(), "1207659150", "/")
	if err != nil {
		t.Fatalf("SecureLink: %v", err)
	}
	const want = "/products/1207659150/secure_link?generation=2&path=/&_version=2"
	if gotURI != want {
		t.Errorf("request URI = %q, want %q", gotURI, want)
	}
	if gotPath != "/" {
		t.Errorf("path parameter = %q, want %q", gotPath, "/")
	}
	if _, ok := doc["urls"]; !ok {
		t.Errorf("decoded document = %v, want the urls member", doc)
	}
}

// TestDependencyLinkURL locks the request; the path is an expanded galaxy path
// whose separators are not encoded either.
func TestDependencyLinkURL(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		fmt.Fprint(w, `{"urls":[]}`)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	if _, err := cl.DependencyLink(context.Background(), "ab/cd/abcdef"); err != nil {
		t.Fatalf("DependencyLink: %v", err)
	}
	const want = "/open_link?generation=2&_version=2&path=/dependencies/store/ab/cd/abcdef"
	if gotURI != want {
		t.Errorf("request URI = %q, want %q", gotURI, want)
	}
}

// TestSecureLinkNonObjectIsErrNotJSON: the links follow the package's object-only
// shape contract.
func TestSecureLinkNonObjectIsErrNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(jsonBody(`[{"endpoint_name":"x"}]`)))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	if _, err := cl.SecureLink(context.Background(), "1", "/"); !errors.Is(err, ErrNotJSON) {
		t.Errorf("SecureLink = %v, want ErrNotJSON", err)
	}
}

// TestDependenciesJSONTwoSteps locks the two-step flow: the repository document
// names the manifest URL, the SECOND document is returned, and both requests
// carry the same bearer authentication.
func TestDependenciesJSONTwoSteps(t *testing.T) {
	repository := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"repository_manifest":"http://%s/manifest.json"}`, r.Host)
	}
	srv, seen := depsServer(t, repository, jsonBody(`{"depots":[{"dependencyId":"dep1"}]}`))

	cl := newTestClient(t, srv, map[string]any{"access_token": "tok", "expires_in": 3600})
	doc, err := cl.DependenciesJSON(context.Background())
	if err != nil {
		t.Fatalf("DependenciesJSON: %v", err)
	}
	if _, ok := doc["depots"]; !ok {
		t.Errorf("returned document = %v, want the manifest's depots member", doc)
	}
	calls := *seen
	if len(calls) != 2 {
		t.Fatalf("requests = %d (%v), want 2", len(calls), calls)
	}
	if calls[0].uri != "/dependencies/repository?generation=2" {
		t.Errorf("first request = %q", calls[0].uri)
	}
	if calls[1].uri != "/manifest.json" {
		t.Errorf("second request = %q, want the URL from repository_manifest", calls[1].uri)
	}
	for i, c := range calls {
		if c.auth != "Bearer tok" {
			t.Errorf("request %d Authorization = %q, want the bearer token", i, c.auth)
		}
	}
}

// TestDependenciesJSONEmptyRepository locks the "no repository" boundary: an
// empty or non-object repository document is not a failure, and no manifest
// request follows.
func TestDependenciesJSONEmptyRepository(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"empty body", ``},
		{"non-object", `[{"x":1}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, seen := depsServer(t, jsonBody(c.body), jsonBody(`{"depots":[]}`))

			cl := newTestClient(t, srv, nil)
			doc, err := cl.DependenciesJSON(context.Background())
			if err != nil {
				t.Fatalf("DependenciesJSON: %v", err)
			}
			if len(doc) != 0 {
				t.Errorf("document = %v, want empty", doc)
			}
			if got := len(*seen); got != 1 {
				t.Errorf("requests = %d (%v), want only the repository request", got, *seen)
			}
		})
	}
}

// TestDependenciesJSONWithoutManifestMember: a valid document without the member
// is an empty result, not an error.
func TestDependenciesJSONWithoutManifestMember(t *testing.T) {
	srv, seen := depsServer(t, jsonBody(`{"other":1}`), jsonBody(`{"depots":[]}`))

	cl := newTestClient(t, srv, nil)
	doc, err := cl.DependenciesJSON(context.Background())
	if err != nil {
		t.Fatalf("DependenciesJSON: %v", err)
	}
	if len(doc) != 0 {
		t.Errorf("document = %v, want empty", doc)
	}
	if got := len(*seen); got != 1 {
		t.Errorf("requests = %d, want no manifest request", got)
	}
}

// TestDependenciesJSONEmptyManifestURL: an empty URL is not requested at all, so
// "there is no manifest" stays distinguishable from "the request failed".
func TestDependenciesJSONEmptyManifestURL(t *testing.T) {
	srv, seen := depsServer(t, jsonBody(`{"repository_manifest":""}`), jsonBody(`{"depots":[]}`))

	cl := newTestClient(t, srv, nil)
	doc, err := cl.DependenciesJSON(context.Background())
	if err != nil {
		t.Fatalf("DependenciesJSON: %v", err)
	}
	if len(doc) != 0 {
		t.Errorf("document = %v, want empty", doc)
	}
	if got := len(*seen); got != 1 {
		t.Errorf("requests = %d, want no manifest request", got)
	}
}

// TestDependenciesJSONMalformedManifestMember: a member that is not a scalar is
// a real problem and is reported.
func TestDependenciesJSONMalformedManifestMember(t *testing.T) {
	srv, seen := depsServer(t, jsonBody(`{"repository_manifest":{"url":"x"}}`), jsonBody(`{"depots":[]}`))

	cl := newTestClient(t, srv, nil)
	if _, err := cl.DependenciesJSON(context.Background()); err == nil {
		t.Fatal("a non-scalar repository_manifest must be reported")
	}
	if got := len(*seen); got != 1 {
		t.Errorf("requests = %d, want no manifest request", got)
	}
}

// TestDependenciesJSONHTTPError locks the other side of that boundary: an HTTP
// failure on either step is a real fetch failure and is returned, unlike a
// "no repository" answer, which is not.
func TestDependenciesJSONHTTPError(t *testing.T) {
	failing := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	repository := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"repository_manifest":"http://%s/manifest.json"}`, r.Host)
	}

	t.Run("repository step", func(t *testing.T) {
		srv, seen := depsServer(t, failing, jsonBody(`{"depots":[]}`))

		cl := newTestClient(t, srv, nil)
		_, err := cl.DependenciesJSON(context.Background())
		if _, ok := errors.AsType[*httpx.StatusError](err); !ok {
			t.Fatalf("err = %v, want a status error", err)
		}
		if got := len(*seen); got != 1 {
			t.Errorf("requests = %d, want 1", got)
		}
	})

	t.Run("manifest step", func(t *testing.T) {
		srv, seen := depsServer(t, repository, failing)

		cl := newTestClient(t, srv, nil)
		_, err := cl.DependenciesJSON(context.Background())
		if _, ok := errors.AsType[*httpx.StatusError](err); !ok {
			t.Fatalf("err = %v, want a status error", err)
		}
		if got := len(*seen); got != 2 {
			t.Errorf("requests = %d, want the repository request plus the failing manifest request", got)
		}
	})
}

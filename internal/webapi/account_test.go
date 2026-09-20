package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nekrozis/goggo/internal/httpx"
)

// accountServer serves one JSON body for every request, capturing the last
// request URI.
func accountServer(t *testing.T, body string, status int) (*httptest.Server, *string) {
	t.Helper()
	var lastURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastURI = r.URL.RequestURI()
		if status != http.StatusOK {
			http.Error(w, "boom", status)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastURI
}

func TestGameDetailsJSONRequestAndDecode(t *testing.T) {
	srv, lastURI := accountServer(t, `{"title":"Alpha","dlcs":[{"manualUrl":"x"}]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.GameDetailsJSON(context.Background(), "1207659156")
	if err != nil {
		t.Fatalf("GameDetailsJSON: %v", err)
	}
	if *lastURI != "/www/account/gameDetails/1207659156.json" {
		t.Errorf("request URI = %q", *lastURI)
	}
	if mustText(t, got["title"]) != "Alpha" {
		t.Errorf("details = %v", got)
	}
}

func TestGameDetailsJSONNonObject(t *testing.T) {
	srv, _ := accountServer(t, `[1,2]`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	if _, err := cl.GameDetailsJSON(context.Background(), "1"); !errors.Is(err, ErrNotJSON) {
		t.Errorf("err = %v, want ErrNotJSON", err)
	}
}

func TestGameDetailsJSONHTTPError(t *testing.T) {
	srv, _ := accountServer(t, "boom", http.StatusNotFound)
	cl, _ := newTestClient(t, srv, 0)

	_, err := cl.GameDetailsJSON(context.Background(), "1")
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("err = %v, want *httpx.StatusError 404", err)
	}
}

func TestOwnedGameIDs(t *testing.T) {
	srv, lastURI := accountServer(t, `{"owned":["a","b",1207659156]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.OwnedGameIDs(context.Background())
	if err != nil {
		t.Fatalf("OwnedGameIDs: %v", err)
	}
	if *lastURI != "/www/user/data/games" {
		t.Errorf("request URI = %q", *lastURI)
	}
	want := []string{"a", "b", "1207659156"} // a number stringifies
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestOwnedGameIDsDegenerateShapes: a missing or non-array owned field yields an
// empty result without an error.
func TestOwnedGameIDsDegenerateShapes(t *testing.T) {
	for _, body := range []string{`{}`, `{"owned":null}`, `{"owned":"a"}`, `{"owned":5}`} {
		t.Run(body, func(t *testing.T) {
			srv, _ := accountServer(t, body, http.StatusOK)
			cl, _ := newTestClient(t, srv, 0)
			got, err := cl.OwnedGameIDs(context.Background())
			if err != nil {
				t.Fatalf("OwnedGameIDs: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("ids = %v, want none", got)
			}
		})
	}
}

func TestOwnedGameIDsRejectsNonScalarElement(t *testing.T) {
	srv, _ := accountServer(t, `{"owned":[{"id":1}]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	if _, err := cl.OwnedGameIDs(context.Background()); err == nil {
		t.Error("object element: want error")
	}
}

// TestTagsArrayShape is the array-of-objects response shape.
func TestTagsArrayShape(t *testing.T) {
	body := `{"tags":[{"id":"gog","name":"GOG.com"},{"id":"wishlist","name":"Wishlist"}]}`
	srv, lastURI := accountServer(t, body, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if *lastURI != "/www/account/getFilteredProducts?mediaType=1&sortBy=title&system=&page=1" {
		t.Errorf("request URI = %q", *lastURI)
	}
	if len(got) != 2 || got["gog"] != "GOG.com" || got["wishlist"] != "Wishlist" {
		t.Errorf("tags = %v", got)
	}
}

// TestTagsObjectShape covers the object-of-objects shape: member values are
// ranged over, so both shapes must work.
func TestTagsObjectShape(t *testing.T) {
	body := `{"tags":{"a":{"id":"gog","name":"GOG.com"}}}`
	srv, _ := accountServer(t, body, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(got) != 1 || got["gog"] != "GOG.com" {
		t.Errorf("tags = %v", got)
	}
}

// TestTagsDuplicateIDLastWins locks array iteration order: the later entry
// overwrites the earlier one.
func TestTagsDuplicateIDLastWins(t *testing.T) {
	body := `{"tags":[{"id":"dup","name":"first"},{"id":"dup","name":"second"}]}`
	srv, _ := accountServer(t, body, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if got["dup"] != "second" {
		t.Errorf("tags = %v, want the last entry to win", got)
	}
}

// TestTagsMissingAndDegenerate: a missing/null tags field is an empty table,
// while a non-container value is an error.
func TestTagsMissingAndDegenerate(t *testing.T) {
	for _, body := range []string{`{}`, `{"tags":null}`, `{"tags":[]}`, `{"tags":{}}`} {
		t.Run(body, func(t *testing.T) {
			srv, _ := accountServer(t, body, http.StatusOK)
			cl, _ := newTestClient(t, srv, 0)
			got, err := cl.Tags(context.Background())
			if err != nil {
				t.Fatalf("Tags: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("tags = %v, want empty", got)
			}
		})
	}

	srv, _ := accountServer(t, `{"tags":"nope"}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)
	if _, err := cl.Tags(context.Background()); err == nil {
		t.Error("scalar tags: want error")
	}
}

func TestTagsRejectsNonObjectEntry(t *testing.T) {
	srv, _ := accountServer(t, `{"tags":["gog"]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	if _, err := cl.Tags(context.Background()); err == nil {
		t.Error("scalar tag entry: want error")
	}
}

// TestTagsMissingIDAndName: absent members read as "".
func TestTagsMissingIDAndName(t *testing.T) {
	srv, _ := accountServer(t, `{"tags":[{"id":"only-id"}]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if v, ok := got["only-id"]; !ok || v != "" {
		t.Errorf("tags = %v, want only-id -> \"\"", got)
	}
}

// TestTagsNotJSON: an HTML login page is ErrNotJSON so the CLI can print the
// "--login" hint.
func TestTagsNotJSON(t *testing.T) {
	srv, _ := accountServer(t, "<html>login</html>", http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	_, err := cl.Tags(context.Background())
	if !errors.Is(err, ErrNotJSON) {
		t.Fatalf("err = %v, want ErrNotJSON", err)
	}
}

func TestTagsHTTPError(t *testing.T) {
	srv, _ := accountServer(t, "boom", http.StatusForbidden)
	cl, _ := newTestClient(t, srv, 0)

	_, err := cl.Tags(context.Background())
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Fatalf("err = %v, want *httpx.StatusError 403", err)
	}
}

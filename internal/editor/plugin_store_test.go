package editor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestParseRemotePluginList(t *testing.T) {
	body, _ := json.Marshal([]map[string]string{
		{"name": "emmet.lua", "type": "file"},
		{"name": "snippets.lua", "type": "file"},
		{"name": "README.md", "type": "file"},
		{"name": "subdir", "type": "dir"},
		{"name": "notes.LUA", "type": "file"}, // case-insensitive suffix
	})
	items, err := parseRemotePluginList(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.File)
	}
	want := []string{"emmet.lua", "snippets.lua", "notes.LUA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, it := range items {
		if !it.Remote {
			t.Errorf("%s should be marked remote", it.File)
		}
	}
}

func TestParseRemotePluginListErrors(t *testing.T) {
	if _, err := parseRemotePluginList([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestStoreEmbeddedItemsSeeded(t *testing.T) {
	items := storeEmbeddedItems()
	if len(items) == 0 {
		t.Fatal("no embedded items")
	}
	for _, it := range items {
		if it.Remote {
			t.Errorf("%s should not be marked remote", it.File)
		}
	}
}

// storeRedirectTransport rewrites every request to one local test server, so
// the store fetches never leave the machine.
type storeRedirectTransport struct{ target *url.URL }

func (s storeRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := req.Clone(req.Context())
	next.URL.Scheme, next.URL.Host = s.target.Scheme, s.target.Host
	return http.DefaultTransport.RoundTrip(next)
}

// TestStoreFetchesThroughSeam drives both fetches through the local seam:
// the listing parses into items with .lua suffixes, and sources download.
func TestStoreFetchesThroughSeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/contents/"):
			fmt.Fprint(w, `[{"type":"file","name":"jumpwords.lua"},{"type":"dir","name":"sub"},{"type":"file","name":"README.md"}]`)
		case strings.HasSuffix(r.URL.Path, "jumpwords.lua"):
			fmt.Fprint(w, `-- remote plugin source`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	storeHTTPTransport = storeRedirectTransport{target: target}
	defer func() { storeHTTPTransport = nil }() // never leak the seam

	items, err := fetchRemotePluginList("owner/repo", "plugins", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].File != "jumpwords.lua" || items[0].Name != "jumpwords" || !items[0].Remote {
		t.Fatalf("items = %+v", items)
	}

	src, err := fetchRemoteSource("owner/repo", "plugins", "main", "jumpwords.lua")
	if err != nil {
		t.Fatal(err)
	}
	if src != "-- remote plugin source" {
		t.Fatalf("src = %q", src)
	}
}

// TestStoreFetchErrorSurfaces: a non-200 answer becomes a readable error.
func TestStoreFetchErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	storeHTTPTransport = storeRedirectTransport{target: target}
	defer func() { storeHTTPTransport = nil }()

	if _, err := fetchRemotePluginList("owner/repo", "plugins", "main"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want the status code", err)
	}
}

package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// resultsPage is a trimmed copy of a DuckDuckGo HTML results page: enough
// structure to exercise the parser, including the redirect wrapper and a
// <div> snippet variant.
const resultsPage = `<!DOCTYPE html><html><body>
<div class="result results_links results_links_deep web-result">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fstrings&amp;rut=xyz"><b>strings</b> package - Go docs</a>
  <a class="result__snippet" href="#">The <b>strings</b> package implements simple functions to manipulate UTF-8 encoded strings.</a>
</div>
<div class="result results_links results_links_deep web-result">
  <a rel="nofollow" class="result__a" href="https://example.org/utf8">Handling UTF-8 &amp; Unicode</a>
  <div class="result__snippet">A short note about rune iteration and byte slicing.</div>
</div>
<div class="result results_links results_links_deep web-result">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F">Protocol-free URL</a>
</div>
</body></html>`

func TestParseExtractsTitleURLAndSnippet(t *testing.T) {
	got := Parse(resultsPage)
	if len(got) != 3 {
		t.Fatalf("parsed %d results, want 3: %+v", len(got), got)
	}
	// The redirect wrapper must be unwrapped: the model (and the user) wants the
	// destination, not duckduckgo.com/l/.
	if got[0].URL != "https://go.dev/doc/strings" {
		t.Fatalf("url = %q, want the unwrapped destination", got[0].URL)
	}
	if got[0].Title != "strings package - Go docs" {
		t.Fatalf("title = %q", got[0].Title)
	}
	if !strings.Contains(got[0].Snippet, "manipulate UTF-8") {
		t.Fatalf("snippet = %q", got[0].Snippet)
	}
	if got[1].URL != "https://example.org/utf8" {
		t.Fatalf("plain url = %q", got[1].URL)
	}
	if !strings.Contains(got[1].Snippet, "rune iteration") {
		t.Fatalf("div snippet = %q", got[1].Snippet)
	}
}

func TestParseIsForgiving(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no results":       "<html><body><div>nothing here</div></body></html>",
		"markup only":      `<a class="result__a" href="https://x.dev"></a>`,
		"missing class":    `<a href="https://x.dev">x</a>`,
		"unclosed snippet": `<a class="result__a" href="https://x.dev">X</a><a class="result__snippet">cut`,
	}
	for name, page := range cases {
		// Must never panic and never invent results.
		for _, r := range Parse(page) {
			if r.Title == "" || r.URL == "" {
				t.Errorf("%s: incomplete result %+v", name, r)
			}
		}
	}
}

func TestParseCapsResults(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxResults*2; i++ {
		b.WriteString(`<a rel="nofollow" class="result__a" href="https://x.dev/">hit</a>`)
	}
	if got := Parse(b.String()); len(got) > MaxResults {
		t.Fatalf("parsed %d results, want at most %d", len(got), MaxResults)
	}
}

func TestFormatIsCompact(t *testing.T) {
	out := Format("go strings", []Result{{Title: "docs", URL: "https://go.dev", Snippet: "about strings"}})
	for _, want := range []string{"[web_search go strings] 1 result(s)", "1. docs", "https://go.dev", "about strings"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSearchUsesEndpointAndQuery(t *testing.T) {
	var gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(resultsPage))
	}))
	defer srv.Close()

	c := NewWithEndpoint(srv.URL + "/html/")
	results, err := c.Search(context.Background(), "go strings & runes")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "go strings & runes" {
		t.Fatalf("query sent as %q", gotQuery)
	}
	if gotUA == "" {
		t.Error("a browser-ish User-Agent is required, DuckDuckGo strips pages without it")
	}
	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
}

func TestSearchRejectsEmptyAndCapsQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query().Get("q")) > MaxQueryLen {
			t.Errorf("query longer than the cap reached the server")
		}
		_, _ = w.Write([]byte(resultsPage))
	}))
	defer srv.Close()

	c := NewWithEndpoint(srv.URL)
	if _, err := c.Search(context.Background(), "   "); err == nil {
		t.Fatal("an empty query must be an error, not a request")
	}
	if _, err := c.Search(context.Background(), strings.Repeat("x", MaxQueryLen*2)); err != nil {
		t.Fatalf("a long query must be truncated, not rejected: %v", err)
	}
}

func TestSearchReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	if _, err := NewWithEndpoint(srv.URL).Search(context.Background(), "q"); err == nil {
		t.Fatal("a non-200 must surface as an error the model can read")
	}
}

func TestSearchRespectsContextCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := NewWithEndpoint(srv.URL).Search(ctx, "q"); err == nil {
		t.Fatal("a cancelled context must abort the query")
	}
}

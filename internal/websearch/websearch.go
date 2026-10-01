// Package websearch is the only component in dmed allowed to reach outside the
// workspace. It queries DuckDuckGo's HTML endpoint (no API key, no tracking
// token) and returns plain results for the model.
//
// It exists as its own package, with its own client and its own limits, so the
// blast radius of "the model asked the internet" stays visible: the caller has
// to opt in, every query is counted, and nothing else in the tree can make a
// network request on the model's behalf.
package websearch

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// netDialer is the dialer used by the shared transport; keeping it named makes
// the timeout visible where the transport is built.
var netDialer = net.Dialer{Timeout: connectTimeout}

// Limits. The body cap matters as much as the timeout: the search page is a
// full HTML document and there is no reason to pull more than a screenful of it
// into the model's context.
const (
	// MaxResults is how many results one query returns.
	MaxResults = 8
	// MaxBody caps the downloaded page.
	MaxBody = 1 << 20 // 1 MiB
	// MaxSnippetLen caps one snippet in runes.
	MaxSnippetLen = 300
	// MaxQueryLen caps the query itself.
	MaxQueryLen = 256
)

// Timeouts are split like the AI client: the header wait is bounded so a dead
// host cannot pin a tool call, the body has no overall deadline.
const (
	connectTimeout = 10 * time.Second
	headerTimeout  = 20 * time.Second
)

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// duckduckgoBase is the HTML endpoint. It takes the query in the query string
// and needs no token, which is why it is the only one used.
const duckduckgoBase = "https://html.duckduckgo.com/html/"

// Client queries DuckDuckGo. The zero value is usable and keeps a shared
// connection pool.
type Client struct {
	http *http.Client
	// endpoint overrides duckduckgoBase; tests point it at a local server so no
	// unit test ever touches the public service.
	endpoint string
}

// New returns a client with bounded timeouts.
func New() *Client {
	return &Client{http: &http.Client{
		Timeout: 30 * time.Second, // whole-exchange cap: the page is small
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           netDialer.DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			ResponseHeaderTimeout: headerTimeout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}}
}

// NewWithEndpoint returns a client pointed at another base URL. Only tests use
// it; production always talks to DuckDuckGo.
func NewWithEndpoint(base string) *Client {
	c := New()
	c.endpoint = base
	return c
}

func (c *Client) base() string {
	if c.endpoint != "" {
		return c.endpoint
	}
	return duckduckgoBase
}

// Search runs one query and returns up to MaxResults hits. An empty result set
// is not an error: "no matches" is an answer the model can act on.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}
	if r := []rune(q); len(r) > MaxQueryLen {
		q = string(r[:MaxQueryLen])
	}
	// The HTML endpoint takes the query in the query string and needs no token.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	// Without a browser-ish User-Agent DuckDuckGo serves a stripped page.
	req.Header.Set("User-Agent", "dmed/1.0 (+https://github.com/dedomorozoff/dmed)")
	req.Header.Set("Accept", "text/html")

	client := c.http
	if client == nil {
		client = New().http
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, err
	}
	return Parse(string(body)), nil
}

// The result page is machine-generated and stable enough to parse, but fragile
// by nature: every element below is matched loosely and a missing piece only
// costs a field, never a result.
var (
	// <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=<encoded>">Title</a>
	linkRe = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	// <a class="result__snippet" ...>Snippet…</a>
	snippetRe = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	// <div class="result__snippet" ...>Snippet…</div>
	divSnippetRe = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</div>`)
	tagRe        = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe         = regexp.MustCompile(`\s+`)
	// uddg carries the real target URL for a redirect link.
	uddgRe = regexp.MustCompile(`[?&]uddg=([^&]+)`)
)

// Parse extracts the results from a DuckDuckGo HTML page. It is exported and
// pure so the parsing can be tested against a fixture without a network.
func Parse(page string) []Result {
	links := linkRe.FindAllStringSubmatch(page, MaxResults*3)
	if len(links) == 0 {
		return nil
	}
	snippets := make([]string, 0, len(links))
	for _, m := range snippetRe.FindAllStringSubmatch(page, MaxResults*3) {
		snippets = append(snippets, clean(m[1]))
	}
	for _, m := range divSnippetRe.FindAllStringSubmatch(page, MaxResults*3) {
		snippets = append(snippets, clean(m[1]))
	}

	out := make([]Result, 0, MaxResults)
	for _, m := range links {
		href := unwrapRedirect(html.UnescapeString(m[1]))
		title := clean(m[2])
		if href == "" || title == "" {
			continue
		}
		r := Result{Title: title, URL: href}
		if len(out) < len(snippets) {
			r.Snippet = capSnippet(snippets[len(out)])
		}
		out = append(out, r)
		if len(out) == MaxResults {
			break
		}
	}
	if out == nil {
		return nil
	}
	return out
}

// unwrapRedirect turns DuckDuckGo's //duckduckgo.com/l/?uddg=… link into the
// destination the model actually wants to see.
func unwrapRedirect(href string) string {
	if m := uddgRe.FindStringSubmatch(href); m != nil {
		if dec, err := url.QueryUnescape(m[1]); err == nil {
			return dec
		}
		return m[1]
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

// clean strips markup, entities and whitespace runs.
func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func capSnippet(s string) string {
	if r := []rune(s); len(r) > MaxSnippetLen {
		return strings.TrimSpace(string(r[:MaxSnippetLen])) + "…"
	}
	return s
}

// Format renders results as the compact text the model reads — one block per
// hit, no table, so it survives a narrow context window.
func Format(query string, results []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[web_search %s] %d result(s)", query, len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "\n   %s", r.Snippet)
		}
	}
	return b.String()
}

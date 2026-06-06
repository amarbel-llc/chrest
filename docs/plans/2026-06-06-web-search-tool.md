# web-search MCP Tool Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Add a `web-search` MCP tool that queries DuckDuckGo's HTML endpoint, returns a structured result list, and pre-fetches the top 3 result pages through the existing web-fetch dispatch pipeline.

**Architecture:** New zero-internal-dep package `go/internal/0/websearch` owns the `Backend` interface, the DDG-HTML backend (net/http GET + SERP parsing + `uddg=` decoding), and result rendering (markdown/json). The MCP handler lives in `go/cmd/chrest/main.go` beside web-fetch, shares `fetchCache` for prefetches, and adds a query-keyed `searchCache`. Design: `docs/plans/2026-06-06-web-search-tool-design.md`.

**Tech Stack:** Go, `golang.org/x/net/html` + `github.com/andybalholm/cascadia` (both already direct deps — no go.mod change, no `build-gomod2nix` needed), dewey `pkgs/protocol` for MCP shapes, bats for integration.

**Rollback:** Purely additive. Hard kill = remove the `registry.Register` block for web-search (one commit).

---

## Context for a zero-context implementer

- **Worktree only:** work exclusively in `/home/sasha/eng/repos/chrest/.worktrees/calm-plum`. Never touch the root repo checkout.
- **Reference implementation:** the `web-fetch` tool in `go/cmd/chrest/main.go:232-408` (registration + handler), `fetchCacheEntry` + `fetchViaFirefox`/`fetchViaDispatch` at `main.go:463-781`. Mirror its style: anonymous param struct, `protocol.ErrorResultV1` for user-facing errors (returns `isError`, never a Go error), content blocks via `protocol.TextContentV1` / `protocol.ResourceLinkContent` / `protocol.EmbeddedTextResourceContent`.
- **NATO tiers:** `go/internal/<level>/<leaf>`; level is computed from dependency height by dagnabit. `websearch` imports no internal packages → level `0` → `go/internal/0/websearch/`. After creating it, `just validate-dagnabit-reposition` must pass; if it fails, run `just codemod-dagnabit-reposition apply` and inspect what moved.
- **Untracked files are invisible to nix builds:** `git add` every new file before any `nix build`-based recipe (`just build`, `just test-mcp`). Plain `go test`/`go build` from the devshell don't care.
- **Test commands:** unit tests via `just test-go <flags>` (runs `cd go && go test <flags> ./...`) or directly `cd go && go test ./internal/0/websearch/ -v -run <Name>`. Cheap compile check: `cd go && go build ./...`. Do NOT run the full `just` aggregate — the spinclass merge hook runs it.
- **One deliberate deviation from the design doc:** the design says prefetch resource_links use URI `web-fetch://<url>`. Fragmentless web-fetch URIs are not readable by `read-resource` (`splitWebFetchURI` at `main.go:450-461` requires a `#fragment`). Emit `web-fetch://<url>#markdown` instead. Task 8 records this in the design doc.

---

### Task 1: websearch package — types + SERP fixtures

**Files:**
- Create: `go/internal/0/websearch/websearch.go`
- Create: `go/internal/0/websearch/testdata/serp_golang_syncmap.html`
- Create: `go/internal/0/websearch/testdata/serp_empty.html`

**Step 1: Create the package with the core types**

`go/internal/0/websearch/websearch.go`:

```go
// Package websearch implements web-search backends for the web-search
// MCP tool. The DDGHTML backend queries DuckDuckGo's no-JS HTML
// endpoint via plain net/http; the Backend interface is the seam for
// future backends (SearXNG, full-JS engine scraping). See
// docs/plans/2026-06-06-web-search-tool-design.md.
package websearch

import "context"

// Result is a single parsed search result.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Backend performs a search and returns parsed results. maxResults <= 0
// means "everything the backend's result page yields".
type Backend interface {
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}
```

**Step 2: Capture real DDG SERP fixtures**

From the devshell (network available):

```bash
cd go/internal/0/websearch
mkdir -p testdata
ua='Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0'
curl -sS -A "$ua" 'https://html.duckduckgo.com/html/?q=golang+sync.Map' > testdata/serp_golang_syncmap.html
curl -sS -A "$ua" 'https://html.duckduckgo.com/html/?q=xqzv+blorpt+nonexistentquery+zzqy' > testdata/serp_empty.html
```

Then **inspect both fixtures** (`grep -o 'class="[^"]*result[^"]*"' testdata/serp_golang_syncmap.html | sort -u`) and verify:

- `serp_golang_syncmap.html` contains result markup. Expected (verify, don't trust): container `div.result.results_links`, title link `a.result__a` (href either direct or `//duckduckgo.com/l/?uddg=<urlencoded>&rut=...`), snippet `a.result__snippet`, ad results carry `result--ad`.
- `serp_empty.html` contains a no-results indicator (expected: an element with class `no-results`; verify the actual marker).

If the live markup differs from the selectors above, **adjust the selectors in Task 2's parser to match the fixture** — the fixture is the source of truth. If DDG returns a block/captcha page instead of a SERP, stop and report to the user rather than committing a junk fixture.

**Step 3: Compile check**

Run: `cd go && go build ./internal/0/websearch/`
Expected: success, no output.

**Step 4: Commit**

```bash
git add go/internal/0/websearch
git commit -m "feat(websearch): package skeleton + DDG SERP fixtures"
```

---

### Task 2: SERP parser (TDD against fixtures)

**Files:**
- Create: `go/internal/0/websearch/ddghtml_parse.go`
- Create: `go/internal/0/websearch/ddghtml_parse_test.go`

**Step 1: Write the failing tests**

`go/internal/0/websearch/ddghtml_parse_test.go`:

```go
package websearch

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDDGSERPResults(t *testing.T) {
	results, err := parseDDGSERP(mustFixture(t, "serp_golang_syncmap.html"), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) < 5 {
		t.Fatalf("expected >=5 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Title == "" {
			t.Errorf("result %d: empty title", i)
		}
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL not absolute: %q", i, r.URL)
		}
		if strings.Contains(r.URL, "duckduckgo.com/l/") {
			t.Errorf("result %d: uddg redirect not decoded: %q", i, r.URL)
		}
	}
	// The query is about Go's sync.Map; at least one result should
	// point at go.dev or pkg.go.dev.
	var sawGoDev bool
	for _, r := range results {
		if strings.Contains(r.URL, "go.dev") {
			sawGoDev = true
		}
	}
	if !sawGoDev {
		t.Errorf("expected at least one go.dev result, got %+v", results)
	}
}

func TestParseDDGSERPMaxResults(t *testing.T) {
	results, err := parseDDGSERP(mustFixture(t, "serp_golang_syncmap.html"), 3)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected exactly 3 results, got %d", len(results))
	}
}

func TestParseDDGSERPGenuinelyEmpty(t *testing.T) {
	results, err := parseDDGSERP(mustFixture(t, "serp_empty.html"), 0)
	if err != nil {
		t.Fatalf("genuinely-empty SERP must not error, got: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestParseDDGSERPMarkupDrift(t *testing.T) {
	notASERP := []byte("<html><body><p>hello world</p></body></html>")
	_, err := parseDDGSERP(notASERP, 0)
	if !errors.Is(err, ErrMarkupDrift) {
		t.Fatalf("expected ErrMarkupDrift, got: %v", err)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd go && go test ./internal/0/websearch/ -v`
Expected: compile FAIL — `parseDDGSERP` and `ErrMarkupDrift` undefined.

**Step 3: Implement the parser**

`go/internal/0/websearch/ddghtml_parse.go` — adjust selectors/markers to match the Task 1 fixtures if they differ:

```go
package websearch

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// ErrMarkupDrift is returned when the page parsed to zero results AND
// carries no recognizable DDG no-results indicator — the likely cause
// is DuckDuckGo changing their SERP markup, and the caller must surface
// it loudly rather than report "no results".
var ErrMarkupDrift = errors.New(
	"websearch: page contained no recognizable DuckDuckGo results markup (probable SERP markup drift)")

var (
	selResult    = cascadia.MustCompile("div.result")
	selTitleLink = cascadia.MustCompile("a.result__a")
	selSnippet   = cascadia.MustCompile(".result__snippet")
	selNoResults = cascadia.MustCompile(".no-results")
)

// parseDDGSERP parses a html.duckduckgo.com/html result page.
// maxResults <= 0 means no cap. Ad results (class result--ad) are
// skipped. uddg redirect URLs are decoded to the target URL.
func parseDDGSERP(body []byte, maxResults int) ([]Result, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("websearch: parse SERP html: %w", err)
	}

	var results []Result
	for _, node := range cascadia.QueryAll(doc, selResult) {
		if hasClass(node, "result--ad") {
			continue
		}
		link := cascadia.Query(node, selTitleLink)
		if link == nil {
			continue
		}
		u := decodeDDGHref(attr(link, "href"))
		if u == "" {
			continue
		}
		r := Result{
			Title: strings.TrimSpace(nodeText(link)),
			URL:   u,
		}
		if sn := cascadia.Query(node, selSnippet); sn != nil {
			r.Snippet = strings.TrimSpace(nodeText(sn))
		}
		if r.Title == "" {
			continue
		}
		results = append(results, r)
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
	}

	if len(results) == 0 {
		if cascadia.Query(doc, selNoResults) != nil {
			return nil, nil // genuinely empty result set
		}
		return nil, ErrMarkupDrift
	}
	return results, nil
}

// decodeDDGHref resolves a DDG result link to the target URL. DDG HTML
// wraps results in //duckduckgo.com/l/?uddg=<urlencoded>&rut=...
// redirects; direct links pass through. Returns "" for unusable hrefs.
func decodeDDGHref(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(parsed.Hostname(), "duckduckgo.com") &&
		strings.HasPrefix(parsed.Path, "/l/") {
		target := parsed.Query().Get("uddg")
		if target == "" {
			return ""
		}
		return target
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return href
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
```

Note: `cascadia.Query`/`QueryAll` exist in cascadia v1.3.x (used the same way in `go/internal/0/markdown/`). `url.Query().Get("uddg")` already percent-decodes.

**Step 4: Run tests to verify they pass**

Run: `cd go && go test ./internal/0/websearch/ -v`
Expected: all 4 PASS. If `TestParseDDGSERPResults` fails on selectors, re-inspect the fixture and fix selectors (not the test's assertions).

**Step 5: Commit**

```bash
git add go/internal/0/websearch
git commit -m "feat(websearch): DDG SERP parser with uddg decoding and drift detection"
```

---

### Task 3: DDG backend Search() over net/http

**Files:**
- Create: `go/internal/0/websearch/ddghtml.go`
- Create: `go/internal/0/websearch/ddghtml_test.go`

**Step 1: Write the failing tests** — use `httptest` so no live network in unit tests:

`go/internal/0/websearch/ddghtml_test.go`:

```go
package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDDGHTMLSearchParsesFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/serp_golang_syncmap.html")
	if err != nil {
		t.Fatal(err)
	}
	var gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotUA = r.Header.Get("User-Agent")
		w.Write(fixture)
	}))
	defer srv.Close()

	b := &DDGHTML{BaseURL: srv.URL}
	results, err := b.Search(context.Background(), "golang sync.Map", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if gotQuery != "golang sync.Map" {
		t.Errorf("query param: got %q", gotQuery)
	}
	if !strings.Contains(gotUA, "Mozilla") {
		t.Errorf("expected browser-like User-Agent, got %q", gotUA)
	}
}

func TestDDGHTMLSearchNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("blocked: anomalous traffic detected"))
	}))
	defer srv.Close()

	b := &DDGHTML{BaseURL: srv.URL}
	_, err := b.Search(context.Background(), "anything", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected status in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "blocked: anomalous") {
		t.Errorf("expected body preview in error, got: %v", err)
	}
}

func TestDDGHTMLSearchEmptyQuery(t *testing.T) {
	b := &DDGHTML{}
	if _, err := b.Search(context.Background(), "", 0); err == nil {
		t.Fatal("expected error for empty query")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd go && go test ./internal/0/websearch/ -v -run TestDDGHTML`
Expected: compile FAIL — `DDGHTML` undefined.

**Step 3: Implement the backend**

`go/internal/0/websearch/ddghtml.go`:

```go
package websearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultUserAgent is sent on SERP requests. The DDG HTML endpoint
// serves no-JS clients, but a browser-like UA avoids the cruder block
// heuristics. Tuning lever — revisit if DDG starts blocking (see the
// design doc's Tuning Levers table).
const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"

const defaultDDGBaseURL = "https://html.duckduckgo.com/html/"

// DDGHTML queries DuckDuckGo's no-JS HTML endpoint with plain net/http
// and parses the SERP. The zero value is ready to use.
type DDGHTML struct {
	// BaseURL overrides the DDG endpoint; tests point it at httptest.
	BaseURL string
	// Client overrides the HTTP client; nil gets a 30s-timeout default.
	Client *http.Client
}

func (b *DDGHTML) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if query == "" {
		return nil, fmt.Errorf("websearch: query is required")
	}
	base := b.BaseURL
	if base == "" {
		base = defaultDDGBaseURL
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	reqURL := base + "?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch: build request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("websearch: fetch SERP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("websearch: HTTP %d from %s: %s",
			resp.StatusCode, base, string(preview))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("websearch: read SERP body: %w", err)
	}
	return parseDDGSERP(body, maxResults)
}
```

**Step 4: Run tests to verify they pass**

Run: `cd go && go test ./internal/0/websearch/ -v`
Expected: all PASS.

**Step 5: Commit**

```bash
git add go/internal/0/websearch
git commit -m "feat(websearch): DDGHTML backend over net/http"
```

---

### Task 4: Backend factory + rendering (markdown/json)

**Files:**
- Create: `go/internal/0/websearch/render.go`
- Create: `go/internal/0/websearch/render_test.go`
- Modify: `go/internal/0/websearch/websearch.go` (add `NewBackend`, `Item`)

**Step 1: Write the failing tests**

`go/internal/0/websearch/render_test.go`:

```go
package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleItems() []Item {
	return []Item{
		{
			Result:     Result{Title: "Go sync.Map docs", URL: "https://pkg.go.dev/sync#Map", Snippet: "Map is like a Go map but safe for concurrent use."},
			Prefetched: true,
		},
		{
			Result:        Result{Title: "Some PDF", URL: "https://example.com/x.pdf", Snippet: "A binary thing."},
			PrefetchError: "web-fetch refused binary content-type",
		},
		{
			Result: Result{Title: "Not prefetched", URL: "https://example.com/3", Snippet: "Beyond N."},
		},
	}
}

func TestFormatMarkdown(t *testing.T) {
	out := FormatMarkdown("golang sync.Map", sampleItems())
	for _, want := range []string{
		"# Search: golang sync.Map",
		"1. **Go sync.Map docs**",
		"https://pkg.go.dev/sync#Map",
		"[prefetched]",
		"[prefetch failed: web-fetch refused binary content-type]",
		"3. **Not prefetched**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatJSON(t *testing.T) {
	out, err := FormatJSON("golang sync.Map", sampleItems())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Query   string `json:"query"`
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Snippet       string  `json:"snippet"`
			Prefetched    bool    `json:"prefetched"`
			PrefetchError *string `json:"prefetch_error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if parsed.Query != "golang sync.Map" || len(parsed.Results) != 3 {
		t.Fatalf("unexpected shape: %+v", parsed)
	}
	if !parsed.Results[0].Prefetched || parsed.Results[0].PrefetchError != nil {
		t.Errorf("result 0: %+v", parsed.Results[0])
	}
	if parsed.Results[1].PrefetchError == nil {
		t.Errorf("result 1 should carry prefetch_error")
	}
}

func TestNewBackend(t *testing.T) {
	if _, err := NewBackend(""); err != nil {
		t.Errorf("empty name must default to ddg-html: %v", err)
	}
	if _, err := NewBackend("ddg-html"); err != nil {
		t.Errorf("ddg-html: %v", err)
	}
	if _, err := NewBackend("bing"); err == nil {
		t.Error("unknown backend must error")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd go && go test ./internal/0/websearch/ -v -run 'TestFormat|TestNewBackend'`
Expected: compile FAIL.

**Step 3: Implement**

Append to `go/internal/0/websearch/websearch.go`:

```go
// Item is a Result annotated with its prefetch outcome, ready for
// rendering. PrefetchError is empty when prefetch succeeded or was
// not attempted.
type Item struct {
	Result
	Prefetched    bool
	PrefetchError string
}

// NewBackend resolves a backend by name (the CHREST_WEB_SEARCH_BACKEND
// env var). Empty name defaults to ddg-html. Future backends (searxng,
// full-JS engine scraping) register here.
func NewBackend(name string) (Backend, error) {
	switch name {
	case "", "ddg-html":
		return &DDGHTML{}, nil
	default:
		return nil, fmt.Errorf(
			"unknown CHREST_WEB_SEARCH_BACKEND=%q (expected ddg-html)", name)
	}
}
```

(add `"fmt"` to that file's imports.)

`go/internal/0/websearch/render.go`:

```go
package websearch

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatMarkdown renders the inline markdown result list for the
// web-search tool.
func FormatMarkdown(query string, items []Item) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Search: %s\n\n", query)
	if len(items) == 0 {
		sb.WriteString("No results.\n")
		return sb.String()
	}
	for i, it := range items {
		marker := ""
		switch {
		case it.Prefetched:
			marker = " [prefetched]"
		case it.PrefetchError != "":
			marker = fmt.Sprintf(" [prefetch failed: %s]", it.PrefetchError)
		}
		fmt.Fprintf(&sb, "%d. **%s**%s\n   %s\n", i+1, it.Title, marker, it.URL)
		if it.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", it.Snippet)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

type jsonResult struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Snippet       string  `json:"snippet"`
	Prefetched    bool    `json:"prefetched"`
	PrefetchError *string `json:"prefetch_error"`
}

// FormatJSON renders the inline JSON document for format=json.
func FormatJSON(query string, items []Item) ([]byte, error) {
	out := struct {
		Query   string       `json:"query"`
		Results []jsonResult `json:"results"`
	}{Query: query, Results: make([]jsonResult, 0, len(items))}
	for _, it := range items {
		jr := jsonResult{
			Title: it.Title, URL: it.URL, Snippet: it.Snippet,
			Prefetched: it.Prefetched,
		}
		if it.PrefetchError != "" {
			e := it.PrefetchError
			jr.PrefetchError = &e
		}
		out.Results = append(out.Results, jr)
	}
	return json.MarshalIndent(out, "", "  ")
}
```

**Step 4: Run tests to verify they pass**

Run: `cd go && go test ./internal/0/websearch/ -v`
Expected: all PASS.

**Step 5: Validate dagnabit tiering, then commit**

Run: `just validate-dagnabit-reposition`
Expected: pass (websearch has no internal deps → level 0 is correct). If it fails, run `just codemod-dagnabit-reposition apply`, inspect the move, include it in the commit.

```bash
git add go/internal/0/websearch
git commit -m "feat(websearch): backend factory + markdown/json rendering"
```

---

### Task 5: Extract fetchEntry helper in main.go (pure refactor)

**Files:**
- Modify: `go/cmd/chrest/main.go:276-302` (web-fetch handler dispatch switch)

**Step 1: Add the helper** next to `fetchViaFirefox` (around `main.go:476`):

```go
// fetchEntry runs one URL through the dispatch pipeline selected by
// CHREST_WEB_FETCH_DISPATCH (default bidi-intercept). Shared by the
// web-fetch handler and web-search prefetch.
func fetchEntry(ctx context.Context, url string) (*fetchCacheEntry, error) {
	dispatchMode := os.Getenv("CHREST_WEB_FETCH_DISPATCH")
	if dispatchMode == "" {
		dispatchMode = "bidi-intercept"
	}
	switch dispatchMode {
	case "firefox-only":
		return fetchViaFirefox(ctx, url)
	case "bidi-intercept":
		return fetchViaDispatch(ctx, url)
	default:
		return nil, fmt.Errorf(
			"unknown CHREST_WEB_FETCH_DISPATCH=%s (expected bidi-intercept or firefox-only)",
			dispatchMode)
	}
}
```

**Step 2: Replace the inline switch in the web-fetch handler** (`main.go:276-302`). The block from `if entry == nil {` through `fetchCache.Store(p0.URL, entry)` becomes:

```go
			if entry == nil {
				var err error
				if entry, err = fetchEntry(ctx, p0.URL); err != nil {
					return protocol.ErrorResultV1(err.Error()), nil
				}
				if entry == nil {
					return protocol.ErrorResultV1("web-fetch: empty result"), nil
				}
				fetchCache.Store(p0.URL, entry)
			}
```

(The unknown-dispatch error becomes a returned error instead of a direct `ErrorResultV1` — same isError surface to the client, message preserved.)

**Step 3: Compile + unit-test**

Run: `cd go && go build ./... && go test ./...`
Expected: build OK, tests pass (HOME quirks: if pdfcpu tests complain locally, that's pre-existing; the authoritative run is checkPhase).

**Step 4: Commit**

```bash
git add go/cmd/chrest/main.go
git commit -m "refactor: extract fetchEntry dispatch helper from web-fetch handler"
```

---

### Task 6: web-search MCP tool registration + handler

**Files:**
- Modify: `go/cmd/chrest/main.go` — add `searchCache` next to `fetchCache` (`main.go:159`), register `web-search` after the `web-fetch` registration block (after `main.go:408`), import `code.linenisgreat.com/chrest/go/internal/0/websearch`.

**Step 1: Add the cache declaration** beside `var fetchCache sync.Map` in `runMCP`:

```go
	var searchCache sync.Map // query string → *searchCacheEntry
```

and the entry type near `fetchCacheEntry`:

```go
// searchCacheEntry is the cached payload for a single web-search
// query: the full parsed result page (untrimmed — max_results trims at
// render so differing knobs on a repeat query don't re-hit the
// backend).
type searchCacheEntry struct {
	Results   []websearch.Result
	FetchedAt time.Time
}
```

**Step 2: Register the tool** after the web-fetch `registry.Register` block:

```go
	registry.Register(
		protocol.ToolV1{
			Name: "web-search",
			Description: "Search the web (DuckDuckGo) and return a structured result list " +
				"(title, URL, snippet). The top results (default 3) are pre-fetched through " +
				"the web-fetch pipeline: each successfully prefetched page is returned as a " +
				"resource_link (web-fetch://<url>#markdown, readable via read-resource) and " +
				"is already cached for subsequent web-fetch calls on the same URL. Prefetch " +
				"is best-effort — a failed prefetch annotates that result but does not fail " +
				"the search. Results are cached per query for the lifetime of the MCP " +
				"session; pass `refresh: true` to force a re-search.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The search query"},"max_results":{"type":"integer","description":"Cap on returned results. Default 10."},"prefetch":{"type":"integer","description":"Number of top results to pre-fetch through the web-fetch pipeline. Default 3; 0 disables prefetch."},"format":{"type":"string","description":"Inline output format: 'markdown' (default) or 'json'.","enum":["markdown","json"]},"refresh":{"type":"boolean","description":"Force a re-search even if this query was searched earlier in the session. Default false. Also re-runs prefetches."}},"required":["query"]}`),
			Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		},
		func(ctx context.Context, args json.RawMessage) (*protocol.ToolCallResultV1, error) {
			p0 := struct {
				Query      string `json:"query"`
				MaxResults *int   `json:"max_results"`
				Prefetch   *int   `json:"prefetch"`
				Format     string `json:"format"`
				Refresh    bool   `json:"refresh"`
			}{}
			if err := json.Unmarshal(args, &p0); err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}
			if p0.Query == "" {
				return protocol.ErrorResultV1("web-search: query is required"), nil
			}
			maxResults := 10
			if p0.MaxResults != nil {
				maxResults = *p0.MaxResults
			}
			prefetchN := 3
			if p0.Prefetch != nil {
				prefetchN = *p0.Prefetch
			}
			if p0.Format == "" {
				p0.Format = "markdown"
			}
			if p0.Format != "markdown" && p0.Format != "json" {
				return protocol.ErrorResultV1("unknown format: " + p0.Format + " (expected markdown or json)"), nil
			}

			backend, err := websearch.NewBackend(os.Getenv("CHREST_WEB_SEARCH_BACKEND"))
			if err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}

			var entry *searchCacheEntry
			if !p0.Refresh {
				if v, ok := searchCache.Load(p0.Query); ok {
					entry = v.(*searchCacheEntry)
				}
			}
			if entry == nil {
				results, err := backend.Search(ctx, p0.Query, 0)
				if err != nil {
					// Includes websearch.ErrMarkupDrift — surfaced loudly
					// as a tool error, never as a silent empty list.
					// Search errors are not cached.
					return protocol.ErrorResultV1(err.Error()), nil
				}
				entry = &searchCacheEntry{Results: results, FetchedAt: time.Now()}
				searchCache.Store(p0.Query, entry)
			}

			trimmed := entry.Results
			if maxResults > 0 && len(trimmed) > maxResults {
				trimmed = trimmed[:maxResults]
			}

			if len(trimmed) == 0 {
				return &protocol.ToolCallResultV1{
					Content: []protocol.ContentBlockV1{
						protocol.TextContentV1(fmt.Sprintf(
							"DuckDuckGo returned an empty result set for %q. "+
								"Try a different query.", p0.Query)),
					},
				}, nil
			}

			// Best-effort sequential prefetch of the top N through the
			// shared web-fetch pipeline. Cache hits are free; failures
			// annotate the item but never fail the search.
			items := make([]websearch.Item, len(trimmed))
			for i, r := range trimmed {
				items[i] = websearch.Item{Result: r}
				if i >= prefetchN {
					continue
				}
				if !p0.Refresh {
					if _, ok := fetchCache.Load(r.URL); ok {
						items[i].Prefetched = true
						continue
					}
				}
				fetched, err := fetchEntry(ctx, r.URL)
				if err != nil {
					items[i].PrefetchError = err.Error()
					log.Printf("web-search: prefetch failed for %s: %v", scrubURL(r.URL), err)
					continue
				}
				if fetched == nil {
					items[i].PrefetchError = "empty fetch result"
					continue
				}
				fetchCache.Store(r.URL, fetched)
				items[i].Prefetched = true
			}

			var inline protocol.ContentBlockV1
			switch p0.Format {
			case "json":
				out, err := websearch.FormatJSON(p0.Query, items)
				if err != nil {
					return protocol.ErrorResultV1(err.Error()), nil
				}
				inline = protocol.TextContentV1(string(out))
			default:
				inline = protocol.TextContentV1(websearch.FormatMarkdown(p0.Query, items))
			}

			blocks := []protocol.ContentBlockV1{inline}
			for _, it := range items {
				if !it.Prefetched {
					continue
				}
				uri := fmt.Sprintf("web-fetch://%s#markdown", it.URL)
				blocks = append(blocks,
					protocol.ResourceLinkContent(uri, it.Title, "", mimeMarkdown))
			}
			return &protocol.ToolCallResultV1{Content: blocks}, nil
		},
	)
```

Notes for the implementer:

- `scrubURL` already exists in the main package (used at `main.go:586,630`).
- The `websearch` import goes in the `internal` import group alongside `internal/0/markdown`.
- `ResourceLinkContent(uri, name, description, mimeType)` — match the argument order used at `main.go:322`.

**Step 3: Compile + vet**

Run: `cd go && go build ./... && go vet ./...`
Expected: clean.

**Step 4: Smoke-test by hand against the real MCP surface**

```bash
just build-go
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"web-search","arguments":{"query":"golang sync.Map","prefetch":0}}}' |
  go/build/release/chrest mcp | grep '"id":2' | jq '.result.content[0].text' -r | head -30
```

Expected: a `# Search: golang sync.Map` markdown list with real results. (Requires network; prefetch:0 avoids needing Firefox.)

**Step 5: Commit**

```bash
git add go/cmd/chrest/main.go
git commit -m "feat: web-search MCP tool (DDG HTML backend, top-N prefetch)"
```

---

### Task 7: test-mcp annotation gate + bats integration suite

**Files:**
- Modify: `justfile:219` (add `web-search` to the readOnlyHint list)
- Create: `zz-tests_bats/web_search.bats`

**Step 1: Extend the test-mcp readOnlyHint loop** — `justfile:219`, append `web-search`:

```bash
  for tool in browser-info list-windows get-window list-tabs get-tab list-extensions items-get state-get read-resource web-fetch web-search; do
```

**Step 2: Write the bats suite**

`zz-tests_bats/web_search.bats` — mirror `web_fetch.bats` structure. The whole file is tagged `firefox` even though `prefetch:0` cases don't launch Firefox: the fence lane (`--filter-tags '!firefox'`) denies network, and every web-search test needs live DDG.

```bash
#!/usr/bin/env bats

# bats file_tags=firefox

# End-to-end coverage for the web-search MCP tool (DDG HTML backend).
# Tagged firefox for the no-sandbox lane: even the prefetch:0 cases
# need network reachability (live DuckDuckGo), which the fence lane
# denies. Assertions target structure, not specific result content,
# to stay robust against result-ranking churn.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  setup_test_home
}

teardown() {
  teardown_test_home
}

require_firefox() {
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi
}

function web_search_markdown_no_prefetch { # @test
  call=$(jq -nc '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"web-search",arguments:{query:"golang sync.Map documentation",prefetch:0}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  echo "$resp" | jq -e '.result.content[0].text | contains("# Search: golang sync.Map documentation")'
  echo "$resp" | jq -e '.result.content[0].text | contains("1. **")'
  echo "$resp" | jq -e '.result.content[0].text | test("https?://")'
}

function web_search_json_format { # @test
  call=$(jq -nc '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"web-search",arguments:{query:"golang sync.Map documentation",prefetch:0,format:"json",max_results:5}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # Inline block is parseable JSON with the documented shape.
  echo "$resp" | jq -e '.result.content[0].text | fromjson | .query == "golang sync.Map documentation"'
  echo "$resp" | jq -e '.result.content[0].text | fromjson | .results | length > 0 and length <= 5'
  echo "$resp" | jq -e '.result.content[0].text | fromjson | .results[0] | has("title") and has("url") and has("snippet") and has("prefetched")'
}

function web_search_prefetch_annotates_and_links { # @test
  require_firefox

  call=$(jq -nc '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"web-search",arguments:{query:"example domain",prefetch:1,max_results:5}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 120 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # Best-effort contract: the top result is annotated either way.
  echo "$resp" | jq -e '.result.content[0].text | (contains("[prefetched]") or contains("[prefetch failed:"))'
  # If prefetch succeeded, a readable resource_link must accompany it.
  if echo "$resp" | jq -e '.result.content[0].text | contains("[prefetched]")' >/dev/null; then
    echo "$resp" | jq -e '.result.content[] | select(.type == "resource_link") | .uri | test("^web-fetch://.*#markdown$")'
  fi
}

function web_search_empty_query_errors { # @test
  call=$(jq -nc '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"web-search",arguments:{query:""}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 30 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError == true'
  echo "$resp" | jq -e '.result.content[0].text | contains("query is required")'
}
```

Before finalizing, check `zz-tests_bats/common.bash` and `mcp.bats` for how `$INIT_MSG`/`$INITIALIZED_MSG`/`$CHREST_BIN` are provided (mirror `web_fetch.bats`, which already consumes them). Check how `resource_link` blocks serialize in existing bats assertions (`mcp.bats` / `web_fetch.bats` use `select(.type == "resource")` for embedded resources; resource_link type string is `"resource_link"` — verify against an actual response in Step 3 and adjust the jq if needed.

**Step 3: Run the new suite directly**

```bash
git add zz-tests_bats/web_search.bats justfile go/internal/0/websearch
nix build --no-link  # tracked-file build incl. unit tests in checkPhase
just test-mcp        # annotation gate incl. the new web-search entry
bats zz-tests_bats/web_search.bats  # live-network integration (from devshell)
```

Expected: all pass. If `bats` isn't directly invocable in the devshell, check `just test-mcp-bats`'s invocation (justfile:230+) for the wrapper and run the single file the same way.

**Step 4: Commit**

```bash
git add zz-tests_bats/web_search.bats justfile
git commit -m "test: web-search MCP annotation gate + bats integration suite"
```

---

### Task 8: Documentation

**Files:**
- Modify: `CLAUDE.md` — MCP Server section + Runtime configuration section
- Modify: `docs/plans/2026-06-06-web-search-tool-design.md` — record the URI-fragment deviation

**Step 1: CLAUDE.md — MCP Server section** (after the `read-resource` bullet under "**Tools**"):

```markdown
- `web-search` — DDG-HTML search returning a structured result list; pre-fetches
  the top N results through the web-fetch pipeline (shared cache, resource_links)
```

**Step 2: CLAUDE.md — Runtime configuration section** (after the `CHREST_WEB_FETCH_DISPATCH` block):

```markdown
`CHREST_WEB_SEARCH_BACKEND` selects the `web-search` MCP tool's backend:

- `ddg-html` (default) — DuckDuckGo's no-JS HTML endpoint via plain net/http;
  SERP parsing lives in `*/websearch/`. Planned alternates: `searxng`, full-JS
  engine scraping.
```

**Step 3: Design doc deviation note** — in `docs/plans/2026-06-06-web-search-tool-design.md`, in the "Output shape" section, amend the resource_link bullet:

```markdown
2. **One `resource_link` per successfully prefetched page** — URI
   `web-fetch://<url>#markdown` (fragmentless web-fetch URIs are not
   readable by read-resource, so the implementation links the
   reader-mode markdown slot directly), description carries the result
   title, readable via `read-resource`.
```

**Step 4: Commit**

```bash
git add CLAUDE.md docs/plans/2026-06-06-web-search-tool-design.md
git commit -m "docs: web-search tool surface + backend env var"
```

---

### Task 9: Merge

**Step 1:** Run the pre-merge skill attestations (`simplify`, `review`, `eng:loose-ends`, `eng:doc-drift`) against the accumulated diff, then `mcp__spinclass__nothing-but-the-truth`.

**Step 2:** `mcp__spinclass__merge-this-session` (or the async variant). Its pre-merge hook runs the full `just` suite — do NOT run `just` manually beforehand. The firefox-tagged bats lane exercises web_search.bats there; if the hook fails, investigate from the hook output.

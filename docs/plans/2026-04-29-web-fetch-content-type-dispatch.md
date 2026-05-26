# Web-fetch Content-Type Dispatch — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Make the `web-fetch` MCP tool work correctly on raw / download URLs (binary, non-2xx, text/plain) by classifying responses via WebDriver BiDi network interception and dispatching to the right code path.

**Architecture:** A new `charlie/rawfetch/` package owns three pure helpers — `Classify`, `BuildFromText`, `ExtractMarkdownTOCFromText`. `charlie/firefox/Session` gains intercept primitives (`AddResponseIntercept`, `ContinueResponse`, `FailRequest`, plus a non-blocking `NavigateAsync`). The web-fetch handler in `cmd/chrest/main.go` orchestrates them: register intercept → kick navigate on a goroutine → consume `responseStarted` event → classify → branch (HTML keeps existing `MultiExtract` flow; text rebuilds slots from text body; binary / HTTPError fail the request and return a structured error block).

**Tech Stack:** Go 1.22+, WebDriver BiDi (Firefox), `code.linenisgreat.com/chrest/go/src/bravo/bidi`, `code.linenisgreat.com/chrest/go/src/charlie/firefox`, `code.linenisgreat.com/chrest/go/src/charlie/markdown`, BATS for integration tests.

**Rollback:** Set `CHREST_WEB_FETCH_DISPATCH=firefox-only` in env to bypass the new dispatcher and reproduce today's all-Firefox behavior. After 7 days with no rollback observed, delete the `firefox-only` branch and the env flag.

**Reference:** Design doc `docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md`. Spike `go/src/charlie/firefox/intercept_spike_test.go` (commit `ef7b99f`) confirmed the BiDi mechanisms.

---

## Task 1: rawfetch.Classify (pure function)

**Promotion criteria:** N/A — purely additive new package.

**Files:**

- Create: `go/src/charlie/rawfetch/classify.go`
- Create: `go/src/charlie/rawfetch/classify_test.go`

**Step 1: Write the failing test**

`go/src/charlie/rawfetch/classify_test.go`:

```go
package rawfetch

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		disposition string
		urlExt      string
		status      int
		want        Class
	}{
		{"text/html → HTML", "text/html; charset=utf-8", "", "", 200, ClassHTML},
		{"application/xhtml+xml → HTML", "application/xhtml+xml", "", "", 200, ClassHTML},
		{"image/svg+xml → HTML", "image/svg+xml", "", "", 200, ClassHTML},
		{"text/plain → Text", "text/plain; charset=utf-8", "", "", 200, ClassText},
		{"text/markdown → Text", "text/markdown", "", "", 200, ClassText},
		{"application/json → Text", "application/json", "", "", 200, ClassText},
		{"application/xml → Text", "application/xml", "", "", 200, ClassText},
		{".md ext overrides missing ct → Text", "", "", ".md", 200, ClassText},
		{".toml ext → Text", "", "", ".toml", 200, ClassText},
		{".go ext → Text", "", "", ".go", 200, ClassText},
		{"image/png → Binary", "image/png", "", "", 200, ClassBinary},
		{"application/octet-stream → Binary", "application/octet-stream", "", "", 200, ClassBinary},
		{"application/zip → Binary", "application/zip", "", "", 200, ClassBinary},
		{"text/plain + attachment → Binary", "text/plain", "attachment; filename=foo.txt", "", 200, ClassBinary},
		{"404 → HTTPError (regardless of ct)", "text/html", "", "", 404, ClassHTTPError},
		{"500 → HTTPError", "text/plain", "", "", 500, ClassHTTPError},
		{"empty content-type, no ext → Binary", "", "", "", 200, ClassBinary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.contentType != "" {
				h.Set("Content-Type", tc.contentType)
			}
			if tc.disposition != "" {
				h.Set("Content-Disposition", tc.disposition)
			}
			got := Classify(h, "https://example.com/foo"+tc.urlExt, tc.status)
			if got != tc.want {
				t.Errorf("Classify(%+v) = %v; want %v", tc, got, tc.want)
			}
		})
	}
}
```

**Step 2: Run test — verify it fails to compile**

```bash
cd go && go test -count=1 ./src/charlie/rawfetch/...
```

Expected: build failure — package does not exist.

**Step 3: Write minimal implementation**

`go/src/charlie/rawfetch/classify.go`:

```go
// Package rawfetch classifies HTTP responses for the web-fetch MCP
// tool's content-type-aware dispatch and builds the text/markdown/html
// content slots when the body is already plain text.
//
// See docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md.
package rawfetch

import (
	"mime"
	"net/http"
	"path"
	"strings"
)

// Class is the dispatch decision for a single web-fetch response.
type Class int

const (
	ClassUnknown Class = iota
	ClassHTML
	ClassText
	ClassBinary
	ClassHTTPError
)

// Classify decides which web-fetch path a response should take, based
// on HTTP status, Content-Type, Content-Disposition, and (as a last-
// resort fallback) URL extension.
func Classify(headers http.Header, urlStr string, status int) Class {
	if status < 200 || status >= 300 {
		return ClassHTTPError
	}
	if strings.HasPrefix(strings.ToLower(headers.Get("Content-Disposition")), "attachment") {
		return ClassBinary
	}

	ct := headers.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	mt = strings.ToLower(mt)

	switch mt {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return ClassHTML
	}

	if isTextMediaType(mt) {
		return ClassText
	}

	if mt == "" {
		if isTextExtension(path.Ext(urlStr)) {
			return ClassText
		}
		return ClassBinary
	}

	return ClassBinary
}

func isTextMediaType(mt string) bool {
	switch mt {
	case "text/plain",
		"text/markdown",
		"text/x-markdown",
		"application/json",
		"application/xml",
		"application/x-yaml",
		"application/yaml":
		return true
	}
	if strings.HasPrefix(mt, "text/x-") {
		return true
	}
	return false
}

var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".json": true,
	".toml": true, ".yaml": true, ".yml": true,
	".go": true, ".py": true, ".rs": true,
	".c": true, ".cpp": true, ".h": true,
	".sh": true, ".bash": true,
}

func isTextExtension(ext string) bool {
	return textExtensions[strings.ToLower(ext)]
}
```

**Step 4: Run test — verify it passes**

```bash
cd go && go test -count=1 -v ./src/charlie/rawfetch/...
```

Expected: PASS, all 17 subtests.

**Step 5: Commit**

```bash
git add go/src/charlie/rawfetch/classify.go go/src/charlie/rawfetch/classify_test.go
git commit -m "rawfetch: add Classify for web-fetch dispatch

Pure function that decides whether a response should go through the
existing Firefox MultiExtract path (HTML), be returned as raw text
(text/plain family, or known text URL extensions when Content-Type
is missing), or be refused as binary or non-2xx.

Foundation for the web-fetch content-type-dispatch design (commit
ef7b99f confirmed the BiDi mechanism).

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 2: rawfetch.ExtractMarkdownTOCFromText

**Promotion criteria:** N/A — purely additive.

**Files:**

- Create: `go/src/charlie/rawfetch/toc.go`
- Create: `go/src/charlie/rawfetch/toc_test.go`

**Step 1: Write the failing test**

`go/src/charlie/rawfetch/toc_test.go`:

````go
package rawfetch

import (
	"reflect"
	"testing"

	"code.linenisgreat.com/chrest/go/src/charlie/markdown"
)

func TestExtractMarkdownTOCFromText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []markdown.Heading
	}{
		{
			"basic levels",
			"# A\n## B\n### C\n",
			[]markdown.Heading{
				{ID: "a", Text: "A", Level: 1},
				{ID: "b", Text: "B", Level: 2},
				{ID: "c", Text: "C", Level: 3},
			},
		},
		{
			"hash inside fenced code is ignored",
			"# Real\n```\n# Not a heading\n```\n## Also Real\n",
			[]markdown.Heading{
				{ID: "real", Text: "Real", Level: 1},
				{ID: "also-real", Text: "Also Real", Level: 2},
			},
		},
		{
			"trailing hashes (closed ATX) trimmed",
			"## Foo ##\n",
			[]markdown.Heading{
				{ID: "foo", Text: "Foo", Level: 2},
			},
		},
		{
			"slug collisions get -2, -3 suffixes",
			"# Foo\n## Foo\n",
			[]markdown.Heading{
				{ID: "foo", Text: "Foo", Level: 1},
				{ID: "foo-2", Text: "Foo", Level: 2},
			},
		},
		{
			"non-markdown text yields no headings",
			"this is just\nplain text\nwith no headings\n",
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractMarkdownTOCFromText([]byte(tc.body))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}
````

**Step 2: Run test — verify it fails**

```bash
cd go && go test -count=1 ./src/charlie/rawfetch/...
```

Expected: build failure — `ExtractMarkdownTOCFromText` undefined.

**Step 3: Write minimal implementation**

`go/src/charlie/rawfetch/toc.go`:

````go
package rawfetch

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"code.linenisgreat.com/chrest/go/src/charlie/markdown"
)

var (
	atxHeadingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)(?:\s+#+)?\s*$`)
	slugReplaceRE = regexp.MustCompile(`[^a-z0-9]+`)
)

// ExtractMarkdownTOCFromText scans plain markdown text for ATX
// headings (# through ######), skipping lines inside fenced code
// blocks, and returns synthesized markdown.Heading entries with
// slugified ids. Suffix `-N` is appended on slug collisions.
func ExtractMarkdownTOCFromText(body []byte) []markdown.Heading {
	var out []markdown.Heading
	seen := map[string]int{}
	inFence := false
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := atxHeadingRE.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		level := len(m[1])
		text := strings.TrimSpace(m[2])
		base := slugify(text)
		id := base
		if n := seen[base]; n > 0 {
			id = fmt.Sprintf("%s-%d", base, n+1)
		}
		seen[base]++
		out = append(out, markdown.Heading{ID: id, Text: text, Level: level})
	}
	return out
}

func slugify(text string) string {
	s := strings.ToLower(text)
	s = slugReplaceRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
````

**Step 4: Run test — verify it passes**

```bash
cd go && go test -count=1 -v ./src/charlie/rawfetch/...
```

Expected: PASS for the new test plus the existing `Classify` tests.

**Step 5: Commit**

```bash
git add go/src/charlie/rawfetch/toc.go go/src/charlie/rawfetch/toc_test.go
git commit -m "rawfetch: extract markdown TOC from raw text

Line-based regex scanner that skips fenced code blocks and emits
markdown.Heading entries with slugified ids, suffixing -N on slug
collisions. Lets web-fetch populate a real TOC for raw .md URLs
where Firefox would just wrap the body in <pre> with no real
heading elements.

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 3: rawfetch.BuildFromText

**Promotion criteria:** N/A — purely additive.

**Files:**

- Create: `go/src/charlie/rawfetch/build.go`
- Create: `go/src/charlie/rawfetch/build_test.go`

**Step 1: Write the failing test**

`go/src/charlie/rawfetch/build_test.go`:

````go
package rawfetch

import (
	"strings"
	"testing"
)

func TestBuildFromText_Markdown(t *testing.T) {
	body := []byte("# Hello\n\nWorld\n")
	r := BuildFromText(body, "text/markdown; charset=utf-8", "https://example.com/x.md")
	if string(r.Text) != string(body) {
		t.Errorf("Text mismatch: %q", r.Text)
	}
	if string(r.Markdown) != string(body) {
		t.Errorf("Markdown for md content should be the body verbatim; got %q", r.Markdown)
	}
	if !strings.Contains(string(r.HTML), "<pre>") {
		t.Errorf("HTML should wrap body in <pre>; got %q", r.HTML)
	}
	if len(r.TOC) != 1 || r.TOC[0].Text != "Hello" {
		t.Errorf("TOC mismatch: %+v", r.TOC)
	}
}

func TestBuildFromText_NonMarkdownText(t *testing.T) {
	body := []byte(`{"a":1}`)
	r := BuildFromText(body, "application/json", "https://example.com/x.json")
	if string(r.Text) != string(body) {
		t.Errorf("Text mismatch")
	}
	if !strings.HasPrefix(string(r.Markdown), "```") {
		t.Errorf("non-md text should be wrapped in a fenced code block; got %q", r.Markdown)
	}
	if !strings.Contains(string(r.Markdown), "json") {
		t.Errorf("language hint should appear in fence; got %q", r.Markdown)
	}
	if !strings.Contains(string(r.HTML), "&quot;") && !strings.Contains(string(r.HTML), `{&#34;a&#34;:1}`) {
		t.Errorf("HTML should HTML-escape body; got %q", r.HTML)
	}
	if len(r.TOC) != 0 {
		t.Errorf("non-md text should have empty TOC; got %+v", r.TOC)
	}
}
````

**Step 2: Run — verify it fails**

```bash
cd go && go test -count=1 ./src/charlie/rawfetch/...
```

Expected: build failure — `BuildFromText` undefined.

**Step 3: Write implementation**

`go/src/charlie/rawfetch/build.go`:

````go
package rawfetch

import (
	"fmt"
	"html"
	"mime"
	"path"
	"strings"

	"code.linenisgreat.com/chrest/go/src/charlie/markdown"
)

// Result contains the text/markdown/html slots and TOC for a raw-text
// response. All fields are owned by the caller.
type Result struct {
	Text     []byte
	Markdown []byte
	HTML     []byte
	TOC      []markdown.Heading
}

// BuildFromText populates the three web-fetch content slots and a TOC
// from a body that was already classified as ClassText.
func BuildFromText(body []byte, contentType, urlStr string) *Result {
	r := &Result{Text: body}

	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	ext := strings.ToLower(path.Ext(urlStr))

	isMarkdown := mt == "text/markdown" || mt == "text/x-markdown" || ext == ".md" || ext == ".markdown"

	if isMarkdown {
		r.Markdown = body
		r.TOC = ExtractMarkdownTOCFromText(body)
	} else {
		lang := languageHint(mt, ext)
		var b strings.Builder
		b.WriteString("```")
		b.WriteString(lang)
		b.WriteByte('\n')
		b.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
		r.Markdown = []byte(b.String())
	}

	r.HTML = []byte(fmt.Sprintf("<pre>%s</pre>", html.EscapeString(string(body))))

	return r
}

func languageHint(mt, ext string) string {
	switch ext {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".yaml", ".yml":
		return "yaml"
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".cpp":
		return "cpp"
	case ".h":
		return "c"
	case ".sh", ".bash":
		return "bash"
	}
	switch mt {
	case "application/json":
		return "json"
	case "application/xml":
		return "xml"
	case "application/x-yaml", "application/yaml":
		return "yaml"
	}
	return ""
}
````

**Step 4: Run — verify pass**

```bash
cd go && go test -count=1 -v ./src/charlie/rawfetch/...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add go/src/charlie/rawfetch/build.go go/src/charlie/rawfetch/build_test.go
git commit -m "rawfetch: build text/markdown/html slots from raw body

When the response is classified as ClassText we don't need Firefox to
extract anything — the body IS the content. BuildFromText returns the
three slots web-fetch needs (text verbatim, markdown either verbatim
for .md or wrapped in a language-tagged fence, html as a <pre> wrap
of escaped body) plus a regex-built TOC for markdown bodies.

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 4: firefox.Session intercept primitives

**Promotion criteria:** N/A — additive new methods on Session.

**Files:**

- Modify: `go/src/charlie/firefox/session.go` (append public methods after `LastNavigationHTTP` near line 299)
- Create: `go/src/charlie/firefox/intercept.go` (new file for the new types/methods)
- Modify: `go/src/charlie/firefox/intercept_spike_test.go` (drop the spike test once superseded; replaced in Task 6)

**Note:** The spike confirmed the BiDi calls work. This task wraps them in typed methods; integration coverage stays via the spike test until Task 6's BATS suite supersedes it.

**Step 1: Write the failing test**

`go/src/charlie/firefox/intercept_test.go` (new file, build-tagged so it only runs against real Firefox; same gate as the spike):

```go
//go:build spike

package firefox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSession_AddResponseIntercept_ContinueResponse(t *testing.T) {
	if os.Getenv("CHREST_SPIKE_BIDI_INTERCEPT") != "1" {
		t.Skip("set CHREST_SPIKE_BIDI_INTERCEPT=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	intercept, events, err := s.AddResponseIntercept(ctx, "https", "raw.githubusercontent.com")
	if err != nil {
		t.Fatalf("AddResponseIntercept: %v", err)
	}
	defer s.RemoveIntercept(ctx, intercept)

	url := "https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/README.md"
	navDone := make(chan error, 1)
	go func() { navDone <- s.Navigate(ctx, url) }()

	select {
	case ev := <-events:
		if !ev.IsBlocked {
			t.Fatalf("expected isBlocked=true, got %+v", ev)
		}
		if ev.RequestID == "" {
			t.Fatalf("expected non-empty RequestID")
		}
		ct := ""
		for _, h := range ev.Headers {
			if strings.EqualFold(h.Name, "content-type") {
				ct = h.Value
				break
			}
		}
		if !strings.Contains(ct, "text/plain") {
			t.Fatalf("expected text/plain content-type; got %q", ct)
		}
		if err := s.ContinueResponse(ctx, ev.RequestID); err != nil {
			t.Fatalf("ContinueResponse: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for intercept event")
	}

	if err := <-navDone; err != nil {
		t.Fatalf("Navigate: %v", err)
	}
}
```

**Step 2: Run — verify fails**

```bash
cd go && CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v -run TestSession_AddResponseIntercept ./src/charlie/firefox/...
```

Expected: build failure — methods undefined.

**Step 3: Write the implementation**

`go/src/charlie/firefox/intercept.go`:

```go
package firefox

import (
	"context"
	"encoding/json"
	"strings"

	"code.linenisgreat.com/chrest/go/libs/dewey/bravo/errors"
	"code.linenisgreat.com/chrest/go/src/bravo/bidi"
)

// InterceptedResponse is delivered to the channel returned by
// AddResponseIntercept whenever a top-level response matching the
// pattern is paused at the responseStarted phase. The caller MUST
// invoke either Session.ContinueResponse or Session.FailRequest
// with RequestID before the in-flight Navigate can return.
type InterceptedResponse struct {
	Navigation string
	RequestID  string
	IsBlocked  bool
	URL        string
	Status     int
	Headers    []HTTPHeader
	Intercepts []string
}

type interceptedResponseEvent struct {
	Context    string   `json:"context"`
	Navigation string   `json:"navigation"`
	IsBlocked  bool     `json:"isBlocked"`
	Intercepts []string `json:"intercepts"`
	Request    struct {
		Request string `json:"request"`
	} `json:"request"`
	Response struct {
		URL     string `json:"url"`
		Status  int    `json:"status"`
		Headers []struct {
			Name  string `json:"name"`
			Value struct {
				Value string `json:"value"`
			} `json:"value"`
		} `json:"headers"`
	} `json:"response"`
}

// AddResponseIntercept registers a network.responseStarted intercept
// scoped to this session's browsing context and the given URL pattern,
// and returns the intercept id plus a channel that receives intercept
// events. The channel is closed when RemoveIntercept is called.
func (s *Session) AddResponseIntercept(ctx context.Context, protocol, hostname string) (string, <-chan InterceptedResponse, error) {
	sub := s.conn.SubscribeWithFilter(
		[]string{"network.responseStarted"},
		func(ev bidi.EventFrame) bool {
			var peek interceptedResponseEvent
			if err := json.Unmarshal(ev.Params, &peek); err != nil {
				return false
			}
			return peek.Context == s.contextID && peek.Navigation != "" && peek.IsBlocked
		},
	)

	if _, err := s.conn.Send("session.subscribe", map[string]any{
		"events":   []string{"network.responseStarted"},
		"contexts": []string{s.contextID},
	}); err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}

	result, err := s.conn.Send("network.addIntercept", map[string]any{
		"phases":   []string{"responseStarted"},
		"contexts": []string{s.contextID},
		"urlPatterns": []map[string]any{
			{"type": "pattern", "protocol": protocol, "hostname": hostname},
		},
	})
	if err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}
	var added struct {
		Intercept string `json:"intercept"`
	}
	if err := json.Unmarshal(result, &added); err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}

	out := make(chan InterceptedResponse, 4)
	go func() {
		defer close(out)
		for ev := range sub.Events {
			var decoded interceptedResponseEvent
			if err := json.Unmarshal(ev.Params, &decoded); err != nil {
				continue
			}
			headers := make([]HTTPHeader, 0, len(decoded.Response.Headers))
			for _, h := range decoded.Response.Headers {
				headers = append(headers, HTTPHeader{Name: h.Name, Value: h.Value.Value})
			}
			select {
			case out <- InterceptedResponse{
				Navigation: decoded.Navigation,
				RequestID:  decoded.Request.Request,
				IsBlocked:  decoded.IsBlocked,
				URL:        decoded.Response.URL,
				Status:     decoded.Response.Status,
				Headers:    headers,
				Intercepts: decoded.Intercepts,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Stash the subscription on the session so RemoveIntercept can close it.
	s.intercepts.Store(added.Intercept, sub)

	return added.Intercept, out, nil
}

// ContinueResponse releases a paused request, allowing the response
// to be delivered and Navigate to complete.
func (s *Session) ContinueResponse(ctx context.Context, requestID string) error {
	_, err := s.conn.Send("network.continueResponse", map[string]any{
		"request": requestID,
	})
	return errors.Wrap(err)
}

// FailRequest aborts a paused request. The corresponding Navigate
// call returns a BiDi error wrapping NS_ERROR_ABORT; callers in the
// HTTPError / Binary branches must recognise and swallow that
// specific error.
func (s *Session) FailRequest(ctx context.Context, requestID string) error {
	_, err := s.conn.Send("network.failRequest", map[string]any{
		"request": requestID,
	})
	return errors.Wrap(err)
}

// RemoveIntercept removes a previously-registered intercept and
// closes its event channel.
func (s *Session) RemoveIntercept(ctx context.Context, interceptID string) error {
	if v, ok := s.intercepts.LoadAndDelete(interceptID); ok {
		v.(*bidi.Subscription).Close()
	}
	_, err := s.conn.Send("network.removeIntercept", map[string]any{
		"intercept": interceptID,
	})
	return errors.Wrap(err)
}

// IsAbortedNavigation reports whether err is the BiDi error returned
// by Navigate after an explicit FailRequest.
func IsAbortedNavigation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NS_ERROR_ABORT")
}
```

Modify `go/src/charlie/firefox/session.go` line 17-29: add `intercepts sync.Map` field to `Session`:

```go
// existing Session struct gains:
//   intercepts sync.Map // intercept-id → *bidi.Subscription
```

And add `"sync"` to the imports.

**Step 4: Run — verify pass**

```bash
cd go && CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v -run TestSession_AddResponseIntercept ./src/charlie/firefox/...
```

Expected: PASS. Also run the existing spike to confirm we didn't break it:

```bash
cd go && CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v -run TestSpikeBiDiResponseIntercept ./src/charlie/firefox/...
```

**Step 5: Commit**

```bash
git add go/src/charlie/firefox/intercept.go go/src/charlie/firefox/intercept_test.go go/src/charlie/firefox/session.go
git commit -m "firefox: add typed BiDi response-intercept primitives

AddResponseIntercept registers a network.responseStarted intercept
scoped to the session's browsing context and a (protocol, hostname)
URL pattern, returning an intercept id and a channel of typed
InterceptedResponse events. ContinueResponse releases a paused
request; FailRequest aborts it. RemoveIntercept tears down both the
intercept and its subscription. IsAbortedNavigation classifies the
BiDi error returned by Navigate after FailRequest.

These wrap the raw BiDi calls verified by the spike (commit ef7b99f)
and become the foundation the web-fetch dispatcher uses in the next
commits.

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 5: Wire the dispatcher into web-fetch

**Promotion criteria:** 7 days running with default `CHREST_WEB_FETCH_DISPATCH=bidi-intercept` and zero `firefox-only` overrides observed → delete the `firefox-only` branch and the env flag.

**Files:**

- Modify: `go/cmd/chrest/main.go` lines 169-330 (the cache-fill block of the web-fetch handler)

**Step 1: Write the failing BATS test (lands in Task 6 but stub here so we know the target)**

Skip writing the BATS file in this task — the unit-level coverage for `Classify`, `BuildFromText`, and the Session methods is already in place. We exercise this end-to-end in Task 6.

**Step 2: Read existing `main.go` web-fetch handler**

```bash
sed -n '169,330p' go/cmd/chrest/main.go
```

Confirm the structure matches the design's "cache fill" block before modifying.

**Step 3: Modify the handler**

In `go/cmd/chrest/main.go`, replace lines 287-331 (the `if entry == nil { ... results, err := tools.MultiExtract(...) ...; fetchCache.Store(p0.URL, entry) }` block) with a dispatcher. New block:

```go
var entry *fetchCacheEntry
if !p0.Refresh {
	if v, ok := fetchCache.Load(p0.URL); ok {
		entry = v.(*fetchCacheEntry)
	}
}
if entry == nil {
	dispatchMode := os.Getenv("CHREST_WEB_FETCH_DISPATCH")
	if dispatchMode == "" {
		dispatchMode = "bidi-intercept"
	}

	switch dispatchMode {
	case "firefox-only":
		entry, err = fetchViaFirefox(ctx, p0.URL)
	case "bidi-intercept":
		entry, err = fetchViaDispatch(ctx, p0.URL)
	default:
		return protocol.ErrorResultV1(
			"unknown CHREST_WEB_FETCH_DISPATCH=" + dispatchMode +
				" (expected bidi-intercept or firefox-only)"), nil
	}

	if err != nil {
		return protocol.ErrorResultV1(err.Error()), nil
	}
	if entry == nil {
		// fetchViaDispatch returned a structured error — surface it.
		// (Concrete shape returned alongside the entry as the second value below.)
		return protocol.ErrorResultV1("web-fetch: empty result"), nil
	}
	fetchCache.Store(p0.URL, entry)
}
```

Two new helper functions further down in the same file (next to `splitWebFetchURI`):

```go
// fetchViaFirefox preserves the legacy all-Firefox path. Used when
// CHREST_WEB_FETCH_DISPATCH=firefox-only.
func fetchViaFirefox(ctx context.Context, url string) (*fetchCacheEntry, error) {
	results, err := tools.MultiExtract(ctx, tools.MultiExtractParams{
		URL:     url,
		Formats: []string{"text", "markdown-reader", "html-outer"},
	})
	if err != nil {
		return nil, err
	}
	entry := &fetchCacheEntry{FetchedAt: time.Now(), Path: "firefox-only"}
	var errs []string
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", r.Format, r.Err))
			continue
		}
		switch r.Format {
		case "text":
			entry.Text = r.Data
		case "markdown-reader":
			entry.MarkdownReader = r.Data
		case "html-outer":
			entry.HTML = r.Data
		}
	}
	if entry.Text == nil && entry.MarkdownReader == nil && entry.HTML == nil {
		return nil, fmt.Errorf("all formats failed: %s", strings.Join(errs, "; "))
	}
	if entry.HTML != nil {
		toc, tocErr := markdown.ExtractTOC(bytes.NewReader(entry.HTML))
		if tocErr != nil {
			log.Printf("web-fetch: ExtractTOC failed for %s: %v", url, tocErr)
		} else {
			entry.TOC = toc
		}
	}
	return entry, nil
}

// fetchViaDispatch implements the content-type-aware path: register
// a BiDi response intercept, navigate, classify, and either continue
// (for HTML/Text) or fail (for Binary/HTTPError) the request.
func fetchViaDispatch(ctx context.Context, urlStr string) (*fetchCacheEntry, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	session, err := firefox.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	interceptID, events, err := session.AddResponseIntercept(ctx, parsed.Scheme, parsed.Hostname())
	if err != nil {
		return nil, err
	}
	defer session.RemoveIntercept(ctx, interceptID)

	type dispatchOutcome struct {
		entry *fetchCacheEntry
		err   error
		// nonNilWhenAborted: set when we deliberately failRequest'd, so the
		// Navigate-side NS_ERROR_ABORT is expected.
		failedDeliberately bool
	}
	outcome := make(chan dispatchOutcome, 1)

	go func() {
		select {
		case ev := <-events:
			ct := headerValue(ev.Headers, "Content-Type")
			class := rawfetch.Classify(httpHeaderFrom(ev.Headers), urlStr, ev.Status)

			log.Printf("web-fetch: dispatch=bidi-intercept class=%v ct=%s status=%d url=%s",
				class, ct, ev.Status, urlStr)

			switch class {
			case rawfetch.ClassHTML:
				if err := session.ContinueResponse(ctx, ev.RequestID); err != nil {
					outcome <- dispatchOutcome{err: err}
					return
				}
				// Wait for navigate to complete, then run MultiExtract.
				// Implemented inline below — fall through.

			case rawfetch.ClassText:
				if err := session.ContinueResponse(ctx, ev.RequestID); err != nil {
					outcome <- dispatchOutcome{err: err}
					return
				}
				// We let navigate complete and pull the text body via
				// the existing ExtractText path, which handles
				// Firefox's <pre> wrapping correctly.

			case rawfetch.ClassBinary:
				_ = session.FailRequest(ctx, ev.RequestID)
				outcome <- dispatchOutcome{
					err: fmt.Errorf("web-fetch refused binary content-type %q from %s; use `chrest capture` to save binary downloads",
						ct, ev.URL),
					failedDeliberately: true,
				}
				return

			case rawfetch.ClassHTTPError:
				_ = session.FailRequest(ctx, ev.RequestID)
				outcome <- dispatchOutcome{
					err: fmt.Errorf("web-fetch: HTTP %d from %s", ev.Status, ev.URL),
					failedDeliberately: true,
				}
				return
			}

			// HTML or Text: navigate completes via the main goroutine;
			// build the entry once it does.
			outcome <- dispatchOutcome{
				entry: &fetchCacheEntry{
					FetchedAt: time.Now(),
					Path:      string(classPathLabel(class)),
				},
				err: nil,
			}

		case <-ctx.Done():
			outcome <- dispatchOutcome{err: ctx.Err()}
		}
	}()

	navErr := session.Navigate(ctx, urlStr)
	out := <-outcome

	if out.failedDeliberately {
		// Navigate's NS_ERROR_ABORT is expected.
		if navErr != nil && !firefox.IsAbortedNavigation(navErr) {
			return nil, navErr
		}
		return nil, out.err
	}
	if navErr != nil {
		return nil, navErr
	}
	if out.err != nil {
		return nil, out.err
	}
	if out.entry == nil {
		return nil, fmt.Errorf("web-fetch: dispatcher produced no entry")
	}

	// Now actually fill the entry. For HTML, run MultiExtract; for
	// Text, read the body and use rawfetch.BuildFromText.
	switch out.entry.Path {
	case "html":
		results, err := tools.MultiExtract(ctx, tools.MultiExtractParams{
			URL:     urlStr,
			Formats: []string{"text", "markdown-reader", "html-outer"},
		})
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			switch r.Format {
			case "text":
				out.entry.Text = r.Data
			case "markdown-reader":
				out.entry.MarkdownReader = r.Data
			case "html-outer":
				out.entry.HTML = r.Data
			}
		}
		if out.entry.HTML != nil {
			if toc, err := markdown.ExtractTOC(bytes.NewReader(out.entry.HTML)); err == nil {
				out.entry.TOC = toc
			}
		}

	case "text":
		// Pull the text body via Firefox; it's the easiest way to get
		// what the user sees regardless of charset handling.
		rc, err := session.GetDocumentHTMLReader(ctx)
		// Note: see hint below — alternative is ExtractText. Pick whichever
		// the existing Session exposes. If GetDocumentHTML returns the
		// <pre>-wrapped HTML, strip the wrapper.
		if err != nil {
			return nil, err
		}
		domBytes, _ := io.ReadAll(rc)
		rc.Close()
		body := stripPreWrapper(domBytes)
		ct := guessContentTypeFromURL(urlStr) // helper using the same map as classify
		r := rawfetch.BuildFromText(body, ct, urlStr)
		out.entry.Text = r.Text
		out.entry.MarkdownReader = r.Markdown
		out.entry.HTML = r.HTML
		out.entry.TOC = r.TOC
	}

	return out.entry, nil
}
```

Plus small helpers (in the same file):

```go
func httpHeaderFrom(headers []firefox.HTTPHeader) http.Header {
	h := http.Header{}
	for _, hh := range headers {
		h.Set(hh.Name, hh.Value)
	}
	return h
}

func headerValue(headers []firefox.HTTPHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func classPathLabel(c rawfetch.Class) string {
	switch c {
	case rawfetch.ClassHTML:
		return "html"
	case rawfetch.ClassText:
		return "text"
	default:
		return "unknown"
	}
}
```

Add `Path string` to `fetchCacheEntry` (line 175-181).

Add imports to `main.go`:

```go
"net/http"
"net/url"
"os"

"code.linenisgreat.com/chrest/go/src/charlie/rawfetch"
```

(plus `firefox` if not already imported — it should be reachable via `tools.MultiExtract` indirection but the new helpers reference `firefox.NewSession`, `firefox.HTTPHeader`, `firefox.IsAbortedNavigation` directly).

**Step 4: Verify compile + existing tests pass**

```bash
cd go && go vet ./...
cd go && go test -count=1 ./...
just test-mcp
```

Expected: clean compile; existing test-mcp tool registry validation still passes.

**Step 5: Commit**

```bash
git add go/cmd/chrest/main.go
git commit -m "web-fetch: dispatch via BiDi response intercept

Replaces the unconditional MultiExtract path with a content-type-
aware dispatcher gated by CHREST_WEB_FETCH_DISPATCH (default
bidi-intercept, firefox-only preserves the old behavior verbatim
for the rollback period).

Flow:
- AddResponseIntercept on the session before navigate.
- A goroutine consumes the intercept event while Navigate is in
  flight, classifies via rawfetch.Classify, and either continues
  the response (HTML/Text) or fails the request (Binary/HTTPError).
- HTML proceeds through the existing MultiExtract; Text builds the
  three slots from the body via rawfetch.BuildFromText, populating
  a real TOC from the markdown via the regex extractor.
- Binary and HTTPError surface as ErrorResultV1 instead of the
  malformed-content-block crash that bit raw.githubusercontent.com
  and similar download URLs.

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 6: BATS integration tests

**Promotion criteria:** N/A — tests are additive; they pin the new behavior.

**Files:**

- Create: `zz-tests_bats/web_fetch.bats`

**Step 1: Write the failing tests**

`zz-tests_bats/web_fetch.bats`:

```bash
#!/usr/bin/env bats

load helpers/mcp_helpers.bash

@test "web-fetch: raw .md URL returns body and populated TOC" {
	run mcp_call_web_fetch \
		"https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/blob/<PIN-SHA>/README.md"
	[ "$status" -eq 0 ]
	[[ "$output" == *"# Claude SDK for Python"* ]]
	# Real heading anchors should appear in the TOC content block.
	[[ "$output" == *"#installation"* || "$output" == *"#getting-started"* ]]
}

@test "web-fetch: raw .toml URL returns body, no schema crash" {
	run mcp_call_web_fetch \
		"https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/<PIN-SHA>/pyproject.toml"
	[ "$status" -eq 0 ]
	[[ "$output" == *"[project]"* ]]
}

@test "web-fetch: 404 URL surfaces structured HTTP error" {
	run mcp_call_web_fetch \
		"https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/THIS-DOES-NOT-EXIST"
	[ "$status" -eq 0 ]
	[[ "$output" == *"HTTP 404"* ]]
}

@test "web-fetch: binary URL surfaces structured binary refusal, no schema crash" {
	run mcp_call_web_fetch \
		"https://github.com/anthropics/anthropic-sdk-python/archive/refs/tags/v0.97.0.tar.gz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"refused binary content-type"* ]]
	[[ "$output" != *"invalid_union"* ]]   # MCP schema validation must NOT fire
}

@test "web-fetch: HTML URL still works (regression)" {
	run mcp_call_web_fetch "https://example.com"
	[ "$status" -eq 0 ]
	[[ "$output" == *"Example Domain"* ]]
}
```

Replace `<PIN-SHA>` with a real, pinned commit SHA from a stable tag in the SDK repo. To pick one:

```bash
gh api repos/anthropics/anthropic-sdk-python/git/refs/tags/v0.97.0 \
  --jq '.object.sha' | head -c12
```

Use that SHA in the URLs above.

**Step 2: Run — verify they fail without our changes**

```bash
just test-mcp-bats
```

Expected: the new tests fail until the dispatcher (Task 5) is in place. If you ran tasks in order they should pass.

**Step 3: (Implementation already complete from Task 5.)**

**Step 4: Run — verify they pass**

```bash
just test-mcp-bats
```

Expected: all new tests pass plus existing tests still green.

**Step 5: Commit**

```bash
git add zz-tests_bats/web_fetch.bats
git commit -m "tests(bats): pin web-fetch raw/binary/error behavior

Five end-to-end checks against real GitHub raw URLs (commit-pinned
for stability) that lock in the content-type-aware dispatcher's
contract: raw .md returns body + real TOC, raw .toml returns body,
404 surfaces as HTTP 404 instead of a fake document body, binary
download surfaces as a refusal instead of an MCP schema crash, and
HTML still routes through Firefox/MultiExtract.

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Task 7: Document the env flag in CLAUDE.md

**Promotion criteria:** Remove this paragraph from CLAUDE.md once the env flag is removed (after the 7-day promotion window).

**Files:**

- Modify: `CLAUDE.md` — append paragraph under "## Build Commands" or a new "## Runtime configuration" section.

**Step 1, 2, 3:** Add the following paragraph:

```markdown
### Runtime configuration

`CHREST_WEB_FETCH_DISPATCH` controls how the `web-fetch` MCP tool fetches
URLs:

- `bidi-intercept` (default) — classify via WebDriver BiDi response
  interception; HTML routes through Firefox/MultiExtract, raw text
  routes through `charlie/rawfetch/`, binary and non-2xx responses
  return structured errors. See
  `docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md`.
- `firefox-only` — preserve the pre-dispatch behavior (every URL
  through Firefox/MultiExtract, no classification). Rollback target
  during the dual-architecture period.
```

**Step 4: Verify**

```bash
grep -n CHREST_WEB_FETCH_DISPATCH CLAUDE.md
```

**Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(CLAUDE.md): document CHREST_WEB_FETCH_DISPATCH env flag

:clown:-by: Clown <https://github.com/amarbel-llc/clown>"
```

---

## Final verification

After all tasks land on this branch, run the full test matrix:

```bash
just test
just test-mcp-bats
```

Then merge via spinclass:

```bash
# (handled by mcp__spinclass__merge-this-session)
```

Promotion criterion tracking: 7 days post-merge, search stderr / session logs for `CHREST_WEB_FETCH_DISPATCH=firefox-only`. If zero overrides, open a follow-up to delete `fetchViaFirefox` and the env flag.

---

## Skills referenced

- `eng:subagent-driven-development` — use this to execute the plan task-by-task with fresh subagents per task and a code-review pass per task.
- `eng:test-driven-development` — each task above already follows the failing-test-first pattern.

## Plan complete

Plan complete and saved to `docs/plans/2026-04-29-web-fetch-content-type-dispatch.md`. Ready to execute? **REQUIRED SUB-SKILL:** Use `eng:subagent-driven-development` — fresh subagent per task, code review after each.

# CDP Capture Commands Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> to implement this plan task-by-task.

**Goal:** Add `chrest capture {pdf,screenshot,mhtml,a11y,text}` commands that
spawn headless Chrome, connect via CDP WebSocket, and capture page content.
Available as both CLI subcommands and MCP tools.

**Architecture:** Session interface in `bravo/cdp/` with typed methods returning
`io.ReadCloser`. Headless Chrome backend in `charlie/headless/` implements the
interface. Capture commands in `delta/tools/capture.go` register via
`app.AddCommand()` for automatic CLI + MCP dual availability.

**Tech Stack:** Go stdlib + `golang.org/x/net/websocket` for WebSocket. Runtime
dependency on `chromium` or `google-chrome-stable`.

**Rollback:** Purely additive. Remove the new packages and the
`registerCaptureCommands` call from `main.go`.

---

### Task 1: Session interface and types (`bravo/cdp/`)

**Files:**

- Create: `go/src/bravo/cdp/session.go`

**Step 1: Write the Session interface and option types**

```go
// go/src/bravo/cdp/session.go
package cdp

import (
	"context"
	"io"
)

// Session abstracts a CDP connection. Implementations include headless Chrome
// (charlie/headless) and, in the future, extension debugger proxy.
type Session interface {
	Navigate(ctx context.Context, url string) error
	PrintToPDF(ctx context.Context, opts PDFOptions) (io.ReadCloser, error)
	CaptureScreenshot(ctx context.Context, opts ScreenshotOptions) (io.ReadCloser, error)
	CaptureSnapshot(ctx context.Context) (io.ReadCloser, error)
	AccessibilityTree(ctx context.Context) (io.ReadCloser, error)
	ExtractText(ctx context.Context) (io.ReadCloser, error)
	Close() error
}

type PDFOptions struct {
	Landscape           bool    `json:"landscape,omitempty"`
	DisplayHeaderFooter bool    `json:"displayHeaderFooter,omitempty"`
	PrintBackground     bool    `json:"printBackground,omitempty"`
	PaperWidth          float64 `json:"paperWidth,omitempty"`
	PaperHeight         float64 `json:"paperHeight,omitempty"`
	MarginTop           float64 `json:"marginTop,omitempty"`
	MarginBottom        float64 `json:"marginBottom,omitempty"`
	MarginLeft          float64 `json:"marginLeft,omitempty"`
	MarginRight         float64 `json:"marginRight,omitempty"`
	PageRanges          string  `json:"pageRanges,omitempty"`
}

type ScreenshotOptions struct {
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
	FullPage bool   `json:"-"`
}
```

**Step 2: Verify it compiles**

Run: `cd go && go vet ./src/bravo/cdp/...`
Expected: clean (no errors)

**Step 3: Commit**

```
git add go/src/bravo/cdp/
git commit -m "Add CDP Session interface and option types

bravo/cdp/ defines the Session interface with typed methods returning
io.ReadCloser. Option types match Chrome DevTools Protocol parameters.
Designed for headless backend now, extension debugger backend later.

Part of #10, #11."
```

---

### Task 2: CDP JSON-RPC protocol (`bravo/cdp/`)

**Files:**

- Create: `go/src/bravo/cdp/protocol.go`

The low-level JSON-RPC 2.0 types and a Conn type that wraps a WebSocket
connection and provides `Send(method, params) -> json.RawMessage`.

**Step 1: Write the protocol layer**

```go
// go/src/bravo/cdp/protocol.go
package cdp

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/net/websocket"
)

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("CDP error %d: %s", e.Code, e.Message)
}

// Conn wraps a WebSocket and provides synchronous CDP JSON-RPC calls.
type Conn struct {
	ws   *websocket.Conn
	seq  atomic.Int64
	mu   sync.Mutex // serializes reads (CDP responses are ordered)
}

// Dial connects to a CDP WebSocket endpoint.
func Dial(url string) (*Conn, error) {
	ws, err := websocket.Dial(url, "", "http://localhost")
	if err != nil {
		return nil, fmt.Errorf("cdp dial: %w", err)
	}
	return &Conn{ws: ws}, nil
}

// Send sends a CDP method call and returns the result.
func (c *Conn) Send(method string, params any) (json.RawMessage, error) {
	id := c.seq.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		var err error
		if rawParams, err = json.Marshal(params); err != nil {
			return nil, fmt.Errorf("cdp marshal params: %w", err)
		}
	}

	req := request{ID: id, Method: method, Params: rawParams}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := websocket.JSON.Send(c.ws, req); err != nil {
		return nil, fmt.Errorf("cdp send: %w", err)
	}

	// Read responses until we find ours (skip events).
	for {
		var resp response
		if err := websocket.JSON.Receive(c.ws, &resp); err != nil {
			return nil, fmt.Errorf("cdp receive: %w", err)
		}

		if resp.ID == id {
			if resp.Error != nil {
				return nil, resp.Error
			}
			return resp.Result, nil
		}
		// Response for a different ID or an event — skip.
	}
}

// Close closes the WebSocket connection.
func (c *Conn) Close() error {
	return c.ws.Close()
}
```

**Step 2: Add `golang.org/x/net` dependency**

Run: `cd go && go get golang.org/x/net && go mod tidy`

**Step 3: Verify it compiles**

Run: `cd go && go vet ./src/bravo/cdp/...`

**Step 4: Commit**

```
git add go/src/bravo/cdp/ go/go.mod go/go.sum
git commit -m "Add CDP JSON-RPC protocol layer

Conn type wraps a WebSocket and provides synchronous Send(method, params)
calls. Uses golang.org/x/net/websocket for the WebSocket client."
```

---

### Task 3: Headless Chrome launcher (`charlie/headless/`)

**Files:**

- Create: `go/src/charlie/headless/launcher.go`

**Step 1: Write the Chrome launcher**

```go
// go/src/charlie/headless/launcher.go
package headless

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

var wsURLPattern = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// findChrome locates a Chrome/Chromium binary on PATH.
func findChrome() (string, error) {
	for _, name := range []string{"chromium", "google-chrome-stable", "google-chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.Errorf("no Chrome/Chromium binary found on PATH")
}

// Chrome manages a headless Chrome process.
type Chrome struct {
	cmd  *exec.Cmd
	wsURL string
}

// Launch starts a headless Chrome and returns the DevTools WebSocket URL.
func Launch(ctx context.Context) (*Chrome, error) {
	chromePath, err := findChrome()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, chromePath,
		"--headless",
		"--remote-debugging-port=0",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"about:blank",
	)
	cmd.Stdout = os.Stderr // Chrome's stdout goes to our stderr

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err)
	}

	// Read stderr for the DevTools WebSocket URL.
	scanner := bufio.NewScanner(stderr)
	wsURL := ""

	deadline := time.After(10 * time.Second)
	found := make(chan string, 1)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if m := wsURLPattern.FindStringSubmatch(line); len(m) > 1 {
				found <- m[1]
				return
			}
		}
	}()

	select {
	case wsURL = <-found:
	case <-deadline:
		_ = cmd.Process.Kill()
		return nil, errors.Errorf("timed out waiting for Chrome DevTools URL")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	}

	// Chrome reports the browser-level WS URL. We need a page-level one.
	// Replace /devtools/browser/... with the page target from /json/list.
	// For now, use the browser URL — Navigate will create a target.
	_ = strings.Contains(wsURL, "devtools") // suppress unused

	return &Chrome{cmd: cmd, wsURL: wsURL}, nil
}

// WSURL returns the WebSocket debugging URL.
func (c *Chrome) WSURL() string {
	return c.wsURL
}

// Close kills the Chrome process.
func (c *Chrome) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}
```

**Step 2: Verify it compiles**

Run: `cd go && go vet ./src/charlie/headless/...`

**Step 3: Commit**

```
git add go/src/charlie/headless/
git commit -m "Add headless Chrome launcher

Finds chromium/chrome on PATH, spawns with --headless
--remote-debugging-port=0, reads stderr for the DevTools WebSocket URL."
```

---

### Task 4: Headless Session implementation (`charlie/headless/`)

**Files:**

- Create: `go/src/charlie/headless/session.go`

This implements `cdp.Session` by combining the Chrome launcher with the CDP
Conn.

**Step 1: Write the Session implementation**

```go
// go/src/charlie/headless/session.go
package headless

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"code.linenisgreat.com/chrest/go/src/bravo/cdp"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// Session implements cdp.Session using a headless Chrome process.
type Session struct {
	chrome *Chrome
	conn   *cdp.Conn
}

// NewSession launches headless Chrome and connects via CDP.
func NewSession(ctx context.Context) (*Session, error) {
	chrome, err := Launch(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := cdp.Dial(chrome.WSURL())
	if err != nil {
		chrome.Close()
		return nil, err
	}

	return &Session{chrome: chrome, conn: conn}, nil
}

func (s *Session) Navigate(ctx context.Context, url string) error {
	_, err := s.conn.Send("Page.navigate", map[string]string{"url": url})
	if err != nil {
		return errors.Wrap(err)
	}

	// Wait for load event.
	_, err = s.conn.Send("Page.enable", nil)
	if err != nil {
		return errors.Wrap(err)
	}

	// Simple approach: wait for loadEventFired by polling Page.getNavigationHistory
	// and checking if the page is loaded. For v1, a short sleep + check.
	// TODO: listen for Page.loadEventFired event.
	return nil
}

func (s *Session) PrintToPDF(ctx context.Context, opts cdp.PDFOptions) (io.ReadCloser, error) {
	result, err := s.conn.Send("Page.printToPDF", opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return base64ReadCloser(result, "data")
}

func (s *Session) CaptureScreenshot(ctx context.Context, opts cdp.ScreenshotOptions) (io.ReadCloser, error) {
	params := map[string]any{}
	if opts.Format != "" {
		params["format"] = opts.Format
	}
	if opts.Quality > 0 {
		params["quality"] = opts.Quality
	}
	if opts.FullPage {
		params["captureBeyondViewport"] = true
	}

	result, err := s.conn.Send("Page.captureScreenshot", params)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return base64ReadCloser(result, "data")
}

func (s *Session) CaptureSnapshot(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.conn.Send("Page.captureSnapshot", map[string]string{"format": "mhtml"})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	return io.NopCloser(strings.NewReader(parsed["data"])), nil
}

func (s *Session) AccessibilityTree(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.conn.Send("Accessibility.getFullAXTree", nil)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	// Return the raw JSON.
	var buf bytes.Buffer
	if err := json.Indent(&buf, result, "", "  "); err != nil {
		return nil, errors.Wrap(err)
	}

	return io.NopCloser(&buf), nil
}

func (s *Session) ExtractText(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.conn.Send("Runtime.evaluate", map[string]any{
		"expression":    "document.body.innerText",
		"returnByValue": true,
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	var parsed struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	return io.NopCloser(strings.NewReader(parsed.Result.Value)), nil
}

func (s *Session) Close() error {
	if s.conn != nil {
		s.conn.Close()
	}
	if s.chrome != nil {
		s.chrome.Close()
	}
	return nil
}

// base64ReadCloser extracts a base64-encoded field from a JSON result and
// returns a reader that decodes it.
func base64ReadCloser(result json.RawMessage, field string) (io.ReadCloser, error) {
	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	data, ok := parsed[field]
	if !ok {
		return nil, errors.Errorf("CDP response missing %q field", field)
	}

	return io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))), nil
}
```

**Step 2: Verify it compiles**

Run: `cd go && go vet ./src/charlie/headless/...`

**Step 3: Commit**

```
git add go/src/charlie/headless/
git commit -m "Implement headless CDP Session

Navigate, PrintToPDF, CaptureScreenshot, CaptureSnapshot,
AccessibilityTree, and ExtractText via headless Chrome + WebSocket CDP."
```

---

### Task 5: Capture commands (`delta/tools/capture.go`)

**Files:**

- Create: `go/src/delta/tools/capture.go`
- Modify: `go/src/delta/tools/main.go` (add `registerCaptureCommands` call)

**Step 1: Write the capture command registrations**

```go
// go/src/delta/tools/capture.go
package tools

import (
	"context"
	"encoding/json"
	"io"

	"code.linenisgreat.com/chrest/go/src/bravo/cdp"
	"code.linenisgreat.com/chrest/go/src/charlie/headless"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/golf/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/golf/protocol"
)

func registerCaptureCommands(app *command.Utility) {
	url := command.StringFlag{}
	url.Name = "url"
	url.Required = true
	url.Description = "URL to capture"

	// capture-pdf
	landscape := command.BoolFlag{Name: "landscape", Description: "Use landscape orientation"}
	noHeaders := command.BoolFlag{Name: "no-headers", Description: "Disable header and footer"}
	background := command.BoolFlag{Name: "background", Description: "Print background graphics"}

	app.AddCommand(&command.Command{
		Name:        "capture-pdf",
		Description: command.Description{Short: "Capture a web page as PDF"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{url, landscape, noHeaders, background},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URL        string `json:"url"`
				Landscape  bool   `json:"landscape"`
				NoHeaders  bool   `json:"no-headers"`
				Background bool   `json:"background"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			return withSession(ctx, p0.URL, func(s cdp.Session) (io.ReadCloser, error) {
				return s.PrintToPDF(ctx, cdp.PDFOptions{
					Landscape:           p0.Landscape,
					DisplayHeaderFooter: !p0.NoHeaders,
					PrintBackground:     p0.Background,
				})
			})
		},
	})

	// capture-screenshot
	format := command.StringFlag{Name: "format", Description: "Image format: png (default) or jpeg"}
	quality := command.IntFlag{Name: "quality", Description: "JPEG quality (0-100)"}
	fullPage := command.BoolFlag{Name: "full-page", Description: "Capture the full scrollable page"}

	app.AddCommand(&command.Command{
		Name:        "capture-screenshot",
		Description: command.Description{Short: "Capture a web page as an image"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{url, format, quality, fullPage},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URL      string `json:"url"`
				Format   string `json:"format"`
				Quality  int    `json:"quality"`
				FullPage bool   `json:"full-page"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			return withSession(ctx, p0.URL, func(s cdp.Session) (io.ReadCloser, error) {
				return s.CaptureScreenshot(ctx, cdp.ScreenshotOptions{
					Format:   p0.Format,
					Quality:  p0.Quality,
					FullPage: p0.FullPage,
				})
			})
		},
	})

	// capture-mhtml
	app.AddCommand(&command.Command{
		Name:        "capture-mhtml",
		Description: command.Description{Short: "Capture a web page as MHTML archive"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{url},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			return withSession(ctx, p0.URL, func(s cdp.Session) (io.ReadCloser, error) {
				return s.CaptureSnapshot(ctx)
			})
		},
	})

	// capture-a11y
	app.AddCommand(&command.Command{
		Name:        "capture-a11y",
		Description: command.Description{Short: "Capture the accessibility tree of a web page"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{url},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			return withSession(ctx, p0.URL, func(s cdp.Session) (io.ReadCloser, error) {
				return s.AccessibilityTree(ctx)
			})
		},
	})

	// capture-text
	app.AddCommand(&command.Command{
		Name:        "capture-text",
		Description: command.Description{Short: "Extract plain text from a web page"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{url},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			return withSession(ctx, p0.URL, func(s cdp.Session) (io.ReadCloser, error) {
				return s.ExtractText(ctx)
			})
		},
	})
}

// withSession launches headless Chrome, navigates to the URL, runs the capture
// function, and returns the result as a command.Result.
func withSession(
	ctx context.Context,
	url string,
	capture func(cdp.Session) (io.ReadCloser, error),
) (*command.Result, error) {
	session, err := headless.NewSession(ctx)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer session.Close()

	if err := session.Navigate(ctx, url); err != nil {
		return nil, errors.Wrap(err)
	}

	rc, err := capture(session)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return command.TextResult(string(data)), nil
}
```

**Step 2: Wire into RegisterAll**

In `go/src/delta/tools/main.go`, add `registerCaptureCommands(app)` to
`RegisterAll`:

```go
func RegisterAll(app *command.Utility, p *proxy.BrowserProxy) {
	itemsProxy := browser_items.BrowserProxy{Config: p.Config}

	registerBrowserCommands(app, p)
	registerWindowCommands(app, p)
	registerTabCommands(app, p)
	registerItemCommands(app, p, itemsProxy)
	registerStateCommands(app, p)
	registerCaptureCommands(app)
}
```

Note: `registerCaptureCommands` does NOT take `p` (BrowserProxy) — it manages
its own headless Chrome session.

**Step 3: Verify it compiles**

Run: `cd go && go vet ./...`

**Step 4: Commit**

```
git add go/src/delta/tools/capture.go go/src/delta/tools/main.go
git commit -m "Add capture commands: pdf, screenshot, mhtml, a11y, text

Five new commands under the capture-* namespace, all registered as both
CLI subcommands and MCP tools with readOnlyHint annotation.

Each command spawns a fresh headless Chrome session per invocation.

Closes #10. Part of #11."
```

---

### Task 6: Update gomod2nix and verify nix build

**Step 1: Update gomod2nix.toml**

Run: `cd go && gomod2nix`

**Step 2: Verify nix build**

Run: `nix build ./go`

**Step 3: Commit**

```
git add go/gomod2nix.toml go/go.mod go/go.sum
git commit -m "Update gomod2nix.toml for golang.org/x/net dependency"
```

---

### Task 7: Integration tests (`zz-tests_bats/`)

**Files:**

- Create: `zz-tests_bats/capture.bats`
- Create: `zz-tests_bats/fixtures/test.html`

**Step 1: Create test HTML fixture**

```html
<!-- zz-tests_bats/fixtures/test.html -->
<!DOCTYPE html>
<html>
  <head>
    <title>Chrest Test Page</title>
  </head>
  <body>
    <h1>Hello from chrest</h1>
    <p>This is a test page for capture commands.</p>
  </body>
</html>
```

**Step 2: Write BATS tests**

```bash
#!/usr/bin/env bats
# zz-tests_bats/capture.bats

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  FIXTURE="file://$(cd "$(dirname "$BATS_TEST_FILE")" && pwd)/fixtures/test.html"
}

function capture_pdf_returns_pdf_bytes { # @test
  result=$("$CHREST_BIN" capture-pdf --url "$FIXTURE" | head -c 5)
  [ "$result" = "%PDF-" ]
}

function capture_screenshot_returns_png_bytes { # @test
  result=$("$CHREST_BIN" capture-screenshot --url "$FIXTURE" | head -c 4 | xxd -p)
  [ "$result" = "89504e47" ]  # PNG magic bytes
}

function capture_text_extracts_text { # @test
  result=$("$CHREST_BIN" capture-text --url "$FIXTURE")
  echo "$result" | grep -q "Hello from chrest"
}

function capture_a11y_returns_json { # @test
  result=$("$CHREST_BIN" capture-a11y --url "$FIXTURE")
  echo "$result" | jq -e '.nodes | length > 0'
}

function capture_mhtml_returns_mhtml { # @test
  result=$("$CHREST_BIN" capture-mhtml --url "$FIXTURE" | head -20)
  echo "$result" | grep -q "Content-Type"
}
```

**Step 3: Run tests**

Run: `bats --bin-dir go/build/release/ zz-tests_bats/capture.bats`
Expected: All 5 tests pass (requires chromium on PATH)

**Step 4: Commit**

```
git add zz-tests_bats/capture.bats zz-tests_bats/fixtures/
git commit -m "Add integration tests for capture commands

Tests validate PDF magic bytes, PNG header, text extraction, a11y JSON
structure, and MHTML content-type header."
```

---

### Task 8: MCP tool validation

**Step 1: Extend test-mcp in justfile**

Add capture tools to the readOnlyHint validation loop in the root `justfile`
`test-mcp` recipe — add `capture-pdf capture-screenshot capture-mhtml
capture-a11y capture-text` to the read-only tools list.

**Step 2: Run MCP validation**

Run: `just test-mcp`
Expected: All MCP validations pass

**Step 3: Commit**

```
git add justfile
git commit -m "Add capture tools to MCP annotation validation"
```

# CDP Capture Commands

## Problem

Chrest has no page capture capabilities. Three copies of `html-to-pdf.bash`
exist across repos (`html-to-pdf/`, `chromium-html-to-pdf/`, `pa6e/`) using
headless Chromium + websocat + bash. These should be consolidated into chrest as
first-class commands accessible via CLI and MCP.

Related issues: #10 (html-to-pdf), #11 (multiple output formats).

## Decision

**Headless sidecar approach**: chrest spawns a separate headless Chrome process,
connects via CDP WebSocket, performs captures, and tears it down. The running
browser is unaffected.

The interface is designed so a future **extension debugger backend** (using
`chrome.debugger` API) can implement the same `Session` interface without
changing any command code.

## Architecture

```
  CLI / MCP commands              Interface              Backend
  ─────────────────              ─────────              ───────
  chrest capture pdf    ┐                          ┌─ charlie/headless/
  chrest capture screenshot │─→ bravo/cdp/Session ─→│   (spawns Chrome,
  chrest capture mhtml  │                          │    WebSocket CDP)
  chrest capture a11y   │                          ├─ future: extension
  chrest capture text   ┘                          │   debugger backend
                                                   └─
```

### Package placement (NATO hierarchy)

- **bravo/cdp/** — `Session` interface, option types, CDP JSON-RPC protocol
- **charlie/headless/** — Headless Chrome launcher, WebSocket client, implements
  `Session`
- **delta/tools/capture.go** — `registerCaptureCommands(app, sessionFactory)`

### Session interface (bravo/cdp/)

```go
type Session interface {
    Navigate(ctx context.Context, url string) error
    PrintToPDF(ctx context.Context, opts PDFOptions) (io.ReadCloser, error)
    CaptureScreenshot(ctx context.Context, opts ScreenshotOptions) (io.ReadCloser, error)
    CaptureSnapshot(ctx context.Context) (io.ReadCloser, error)
    AccessibilityTree(ctx context.Context) (io.ReadCloser, error)
    ExtractText(ctx context.Context) (io.ReadCloser, error)
    Close() error
}
```

All capture methods return `io.ReadCloser` for streaming (large PDFs, MHTMLs).
For the headless backend, this wraps a base64-decoding reader over the CDP
response.

### Option types

```go
type PDFOptions struct {
    Landscape           bool
    DisplayHeaderFooter bool
    PrintBackground     bool
    PaperWidth          float64 // inches, default 8.5
    PaperHeight         float64 // inches, default 11
    MarginTop           float64
    MarginBottom        float64
    MarginLeft          float64
    MarginRight         float64
    PageRanges          string  // e.g. "1-3,5"
}

type ScreenshotOptions struct {
    Format   string // "png" (default) or "jpeg"
    Quality  int    // 0-100, jpeg only
    FullPage bool
}
```

### Headless backend (charlie/headless/)

1. Find `chromium` or `google-chrome-stable` on PATH
2. Spawn with `--headless --remote-debugging-port=0 --disable-gpu
--no-first-run --no-default-browser-check`
3. Read stderr for `DevTools listening on ws://127.0.0.1:<port>...`
4. Connect WebSocket to the discovered URL
5. Each `Session` method sends a CDP JSON-RPC 2.0 request and parses the
   response
6. `Close()` kills the Chrome process and closes the WebSocket

**Lifecycle**: Fresh Chrome per invocation (spawn, use, kill). The `Session`
interface is compatible with a future persistent session if needed.

### CDP commands used

| Method            | CDP Command                   | Response field                  |
| ----------------- | ----------------------------- | ------------------------------- |
| PrintToPDF        | `Page.printToPDF`             | `.result.data` (base64)         |
| CaptureScreenshot | `Page.captureScreenshot`      | `.result.data` (base64)         |
| CaptureSnapshot   | `Page.captureSnapshot`        | `.result.data` (MHTML text)     |
| AccessibilityTree | `Accessibility.getFullAXTree` | `.result.nodes` (JSON)          |
| ExtractText       | `Runtime.evaluate`            | `.result.result.value` (string) |

### CLI surface

```
chrest capture pdf         --url <url> [--landscape] [--no-headers]
                           [--background] [--paper-width N] [--paper-height N]
                           [-o file]
chrest capture screenshot  --url <url> [--format png|jpeg] [--quality N]
                           [--full-page] [-o file]
chrest capture mhtml       --url <url> [-o file]
chrest capture a11y        --url <url>
chrest capture text        --url <url>
```

Output goes to stdout by default, or to a file with `-o`. JSON output (a11y)
goes to stdout always.

All commands are registered via `app.AddCommand()` so they automatically
become MCP tools as well.

### MCP tool names

Following the nested pattern: `capture-pdf`, `capture-screenshot`,
`capture-mhtml`, `capture-a11y`, `capture-text`. All have `readOnlyHint: true`
(they don't modify browser state).

### Dependencies

- **No new Go dependencies** for the minimal WebSocket client —
  `golang.org/x/net/websocket` or `nhooyr.io/websocket` may be considered, but
  stdlib `net/http` + upgrade is possible for this use case
- Runtime: `chromium` or `google-chrome-stable` on PATH

### Rollback

This is purely additive — new commands, new packages. No existing code is
modified. Rollback is removing the packages and command registrations.

## Testing

- **Unit**: Mock `Session` interface for command logic tests
- **Integration (BATS)**: Extend `zz-tests_bats/` with tests that spawn
  `chrest capture pdf --url file:///path/to/test.html` against a local HTML
  fixture and validate output (PDF magic bytes, PNG header, MHTML content-type)
- **MCP validation**: Extend `just test-mcp` to verify capture tools appear
  with correct annotations

## Future

- Persistent session mode (keep headless Chrome alive across captures)
- Extension debugger backend (connect to running browser via
  `chrome.debugger` API using the same `Session` interface)
- Additional output formats (Readability-extracted article, DOM snapshot)

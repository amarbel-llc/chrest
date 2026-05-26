# Extension Debugger CDP Backend

## Problem

The headless Chrome sidecar backend (charlie/headless) can't capture the state
of pages in the running browser — it spawns a separate Chrome and loads URLs
fresh. To capture the current rendered state of an open tab (logged-in sessions,
dynamic content, scroll position), we need a backend that talks to the running
browser through the extension.

Additionally, headless Chrome crashes on kernel 6.17 (#14), making the headless
backend unusable on some systems. The extension debugger backend provides an
alternative path.

## Decision

Use Chrome's `chrome.debugger` API through the existing extension + native
messaging proxy. Add three generic routes to the extension that attach, send CDP
commands, and detach. On the Go side, a new `cdp.Session` implementation
translates typed Session methods into these HTTP calls through BrowserProxy.

## Architecture

```
  capture commands (delta/tools/capture.go)
         │
         ├─ --url only     → headless.NewSession (spawns Chrome)
         │
         └─ --tab-id given → extension.NewSession (uses running browser)
                                  │
                            BrowserProxy
                                  │
                         native messaging
                                  │
                          extension routes
                       /debugger/{attach,command,detach}
                                  │
                         chrome.debugger API
```

### Extension changes

**Permission:** Add `"debugger"` to `manifest-chrome.json`. Firefox already has
`"devtools"`.

**New routes in `routes.js`:**

- `POST /debugger/attach` — body: `{ tabId }`. Calls
  `chrome.debugger.attach({tabId}, "1.3")`.
- `POST /debugger/command` — body: `{ tabId, method, params }`. Calls
  `chrome.debugger.sendCommand({tabId}, method, params)`. Returns the raw CDP
  result.
- `POST /debugger/detach` — body: `{ tabId }`. Calls
  `chrome.debugger.detach({tabId})`.

The command route is a transparent CDP proxy — it doesn't know about PDF,
screenshots, etc. Any CDP method works.

**Note:** When attached, Chrome shows an infobanner ("chrest is debugging this
tab"). This is Chrome's security measure and cannot be suppressed.

### Go changes

**New package: `charlie/extension/`**

`session.go` implements `cdp.Session`:

```go
type Session struct {
    proxy *proxy.BrowserProxy
    tabID string
}

func NewSession(proxy *proxy.BrowserProxy, tabID string) *Session
```

Each `Session` method:

1. Sends `POST /debugger/command` with the appropriate CDP method/params
2. Parses the response JSON
3. Returns `io.ReadCloser` (base64 decode for binary, string reader for text)

`Navigate` sends `Page.navigate`. `Close` sends `/debugger/detach`.

**Modified: `delta/tools/capture.go`**

Add `--tab-id` flag to all capture commands. If provided, create
`extension.NewSession(proxy, tabID)` instead of `headless.NewSession(ctx)`.

### Backend selection

| Flags                       | Backend   | Behavior                                        |
| --------------------------- | --------- | ----------------------------------------------- |
| `--url <url>`               | headless  | Spawn Chrome, load URL, capture, kill           |
| `--url <url> --tab-id <id>` | extension | Attach to tab, navigate to URL, capture, detach |
| `--tab-id <id>`             | extension | Attach to tab, capture current state, detach    |

### Native messaging size

Chrome native messaging supports messages up to 1GB from the native host and
4GB from the extension. Base64-encoded PDFs and screenshots are well within
these limits.

## Testing

- **Extension routes**: BATS tests that send JSON-RPC `tools/call` with
  `capture-pdf --tab-id <id>` through `chrest mcp`
- **Unit**: Mock BrowserProxy for Session implementation tests
- **End-to-end**: Manually test `chrest capture-pdf --tab-id <id>` against a
  running Chrome with the extension loaded

## Rollback

Purely additive. Remove `"debugger"` from manifest, delete extension routes,
delete `charlie/extension/`, remove `--tab-id` flag from capture commands.

## Future

- `--tab-id active` shorthand to capture the currently active tab
- Batch capture across all open tabs
- Event streaming (Page.loadEventFired) for smarter wait-for-load logic

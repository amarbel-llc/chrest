# Extension Debugger Backend Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> to implement this plan task-by-task.

**Goal:** Add a second `cdp.Session` implementation that uses the running
browser's `chrome.debugger` API through the extension, selected via `--tab-id`
on capture commands.

**Architecture:** Three new `/debugger` routes in the extension proxy arbitrary
CDP commands via `chrome.debugger.sendCommand()`. A new Go package
`charlie/extension/` implements `cdp.Session` by calling these routes through
`BrowserProxy`. Capture commands gain a `--tab-id` flag that selects this
backend.

**Tech Stack:** JavaScript `chrome.debugger` API (extension), Go HTTP proxy
(existing BrowserProxy).

**Rollback:** Purely additive. Remove `"debugger"` from manifest, delete
extension routes, delete `charlie/extension/`, revert capture.go changes.

---

### Task 1: Add `debugger` permission to Chrome manifest

**Files:**

- Modify: `extension/manifest-chrome.json`

**Step 1: Add the permission**

Add `"debugger"` after `"notifications"` in the permissions array:

```json
  "permissions": [
    "background",
    "idle",
    "history",
    "bookmarks",
    "tabs",
    "nativeMessaging",
    "management",
    "storage",
    "unlimitedStorage",
    "notifications",
    "debugger"
  ],
```

**Step 2: Rebuild extension**

Run: `just build-extension`

**Step 3: Commit**

```
git add extension/manifest-chrome.json
git commit -m "Add debugger permission to Chrome extension manifest

Required for chrome.debugger.attach/sendCommand/detach API access."
```

---

### Task 2: Per-route timeout overrides in extension

**Files:**

- Modify: `extension/src/main.js`

The extension has a hardcoded 1-second timeout (`main.js:47`) on all route
handlers. CDP operations like `Page.printToPDF` can take 10+ seconds. Routes
need to be able to declare a custom timeout.

**Step 1: Add route-level timeout support**

In `main.js`, change `onMessageHTTP` to look up the matched route's `__timeout`
property before falling back to the default:

```javascript
async function onMessageHTTP(req) {
  let results = await browser.storage.sync.get("browser_id");

  if (results === undefined || results["browser_id"] === undefined) {
    // TODO ERROR
  } else {
    req.browser_id = {
      browser: browserType,
      id: results["browser_id"],
    };
  }

  let routeTimeout = getRouteTimeout(req.path);
  let response = await Promise.race([timeout(routeTimeout), runRoute(req)]);

  response.headers = {
    "X-Chrest-Startup-Time": now.toISOString(),
    "X-Chrest-Browser-Type": browserType,
  };

  response.type = "http";

  port.postMessage(response);
}
```

Add the helper function:

```javascript
const DEFAULT_TIMEOUT = 1000;

function getRouteTimeout(path) {
  for (let route of routes.sortedRoutes) {
    if (route.__match(path)) {
      return route.__timeout || DEFAULT_TIMEOUT;
    }
  }
  return DEFAULT_TIMEOUT;
}
```

**Step 2: Rebuild extension and verify existing tests**

Run: `just build-extension`
Run: `just test-mcp-bats` (existing MCP tests should still pass — they use
the default 1s timeout)

**Step 3: Commit**

```
git add extension/src/main.js
git commit -m "Support per-route timeout overrides in extension

Routes can declare __timeout (ms) to override the default 1s timeout.
Needed for CDP debugger operations that can take 10+ seconds."
```

---

### Task 3: Add debugger routes to extension

**Files:**

- Modify: `extension/src/routes.js`

**Step 1: Add the three routes**

Append before the regex compilation block (before `for (let key in Routes)` at
line 486) in `routes.js`. The `/debugger/command` route uses a 30-second timeout
since CDP operations like `Page.printToPDF` can be slow:

```javascript
//  ____       _
// |  _ \  ___| |__  _   _  __ _  __ _  ___ _ __
// | | | |/ _ \ '_ \| | | |/ _` |/ _` |/ _ \ '__|
// | |_| |  __/ |_) | |_| | (_| | (_| |  __/ |
// |____/ \___|_.__/ \__,_|\__, |\__, |\___|_|
//                         |___/ |___/
//

Routes["/debugger/attach"] = {
  async post(req) {
    const tabId = parseInt(req.body.tabId);
    await browser.debugger.attach({ tabId }, "1.3");
    return { status: 200, body: { tabId, attached: true } };
  },
};

Routes["/debugger/command"] = {
  __timeout: 30000,
  async post(req) {
    const tabId = parseInt(req.body.tabId);
    const method = req.body.method;
    const params = req.body.params || {};
    const result = await browser.debugger.sendCommand(
      { tabId },
      method,
      params,
    );
    return { status: 200, body: result };
  },
};

Routes["/debugger/detach"] = {
  async post(req) {
    const tabId = parseInt(req.body.tabId);
    await browser.debugger.detach({ tabId });
    return { status: 200, body: { tabId, detached: true } };
  },
};
```

**Step 2: Rebuild extension**

Run: `just build-extension`

**Step 3: Commit**

```
git add extension/src/routes.js
git commit -m "Add /debugger routes to extension

Three routes for transparent CDP proxy:
- POST /debugger/attach {tabId} - chrome.debugger.attach
- POST /debugger/command {tabId, method, params} - chrome.debugger.sendCommand
- POST /debugger/detach {tabId} - chrome.debugger.detach"
```

---

### Task 4: Extension Session implementation (`charlie/extension/`)

**Files:**

- Create: `go/src/charlie/extension/session.go`

**Step 1: Write the Session implementation**

```go
// go/src/charlie/extension/session.go
package extension

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"code.linenisgreat.com/chrest/go/src/bravo/cdp"
	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// Session implements cdp.Session using the browser extension's
// chrome.debugger API, proxied through BrowserProxy.
type Session struct {
	proxy *proxy.BrowserProxy
	tabID string
}

// Verify interface compliance at compile time.
var _ cdp.Session = (*Session)(nil)

// NewSession creates a session that will attach to the given tab via the
// extension's chrome.debugger API.
func NewSession(ctx context.Context, p *proxy.BrowserProxy, tabID string) (*Session, error) {
	s := &Session{proxy: p, tabID: tabID}

	if _, err := s.proxy.RequestAllBrowsers(ctx, "POST", "/debugger/attach", map[string]any{
		"tabId": tabID,
	}); err != nil {
		return nil, errors.Wrap(err)
	}

	return s, nil
}

func (s *Session) sendCommand(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body := map[string]any{
		"tabId":  s.tabID,
		"method": method,
	}

	if params != nil {
		body["params"] = params
	}

	result, err := s.proxy.RequestAllBrowsers(ctx, "POST", "/debugger/command", body)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return json.RawMessage(result), nil
}

func (s *Session) Navigate(ctx context.Context, url string) error {
	_, err := s.sendCommand(ctx, "Page.navigate", map[string]string{"url": url})
	return err
}

func (s *Session) PrintToPDF(ctx context.Context, opts cdp.PDFOptions) (io.ReadCloser, error) {
	result, err := s.sendCommand(ctx, "Page.printToPDF", opts)
	if err != nil {
		return nil, err
	}

	return base64Field(result, "data")
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

	result, err := s.sendCommand(ctx, "Page.captureScreenshot", params)
	if err != nil {
		return nil, err
	}

	return base64Field(result, "data")
}

func (s *Session) CaptureSnapshot(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.sendCommand(ctx, "Page.captureSnapshot", map[string]string{"format": "mhtml"})
	if err != nil {
		return nil, err
	}

	return textField(result, "data")
}

func (s *Session) AccessibilityTree(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.sendCommand(ctx, "Accessibility.getFullAXTree", nil)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, result, "", "  "); err != nil {
		return nil, errors.Wrap(err)
	}

	return io.NopCloser(&buf), nil
}

func (s *Session) ExtractText(ctx context.Context) (io.ReadCloser, error) {
	result, err := s.sendCommand(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "document.body.innerText",
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
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
	// Best-effort detach — ignore errors since the tab may already be closed.
	_, _ = s.proxy.RequestAllBrowsers(context.Background(), "POST", "/debugger/detach", map[string]any{
		"tabId": s.tabID,
	})
	return nil
}

// base64Field extracts a base64-encoded field from a JSON result.
func base64Field(result json.RawMessage, field string) (io.ReadCloser, error) {
	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	data, ok := parsed[field]
	if !ok {
		return nil, errors.Errorf("CDP response missing %q field", field)
	}

	return io.NopCloser(
		base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)),
	), nil
}

// textField extracts a plain text field from a JSON result.
func textField(result json.RawMessage, field string) (io.ReadCloser, error) {
	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	data, ok := parsed[field]
	if !ok {
		return nil, errors.Errorf("CDP response missing %q field", field)
	}

	return io.NopCloser(strings.NewReader(data)), nil
}
```

**Step 2: Verify it compiles**

Run: `cd go && go vet ./src/charlie/extension/...`

**Step 3: Commit**

```
git add go/src/charlie/extension/
git commit -m "Implement extension debugger CDP Session

charlie/extension/ implements cdp.Session by proxying CDP commands
through the browser extension's chrome.debugger API via BrowserProxy.

Attach on NewSession, sendCommand for each operation, detach on Close."
```

---

### Task 5: Add `--tab-id` flag to capture commands

**Files:**

- Modify: `go/src/delta/tools/capture.go`
- Modify: `go/src/delta/tools/main.go`

**Step 1: Update `registerCaptureCommands` to accept BrowserProxy**

In `main.go`, change:

```go
registerCaptureCommands(app)
```

to:

```go
registerCaptureCommands(app, p)
```

**Step 2: Update capture.go**

Change the function signature and add `--tab-id` to every command's params.
Update `withSession` to accept proxy + tabID and select the backend.

Key changes to `capture.go`:

1. Change `registerCaptureCommands(app *command.Utility)` to
   `registerCaptureCommands(app *command.Utility, p *proxy.BrowserProxy)`

2. Add `tabID` flag:

   ```go
   tabID := command.StringFlag{Name: "tab-id", Description: "Tab ID to capture (uses extension debugger instead of headless Chrome)"}
   ```

3. Add `tabID` to every command's `Params` list.

4. Make `url` not required (it's optional when `--tab-id` is given):

   ```go
   url := command.StringFlag{Name: "url", Description: "URL to capture"}
   ```

5. Each command handler parses `TabID string \`json:"tab-id"\``and passes it
to`withSession`.

6. Update `withSession` signature:

   ```go
   func withSession(
       ctx context.Context,
       p *proxy.BrowserProxy,
       url string,
       tabID string,
       capture func(cdp.Session) (io.ReadCloser, error),
   ) (*command.Result, error)
   ```

7. Backend selection logic in `withSession`:

   ```go
   var session cdp.Session
   var err error

   if tabID != "" {
       session, err = extension.NewSession(ctx, p, tabID)
   } else {
       if url == "" {
           return command.TextErrorResult("--url is required when --tab-id is not specified"), nil
       }
       session, err = headless.NewSession(ctx)
   }
   ```

8. Navigation: only call `Navigate` if `url != ""`:
   ```go
   if url != "" {
       if err := session.Navigate(ctx, url); err != nil {
           return nil, errors.Wrap(err)
       }
   }
   ```

**Step 3: Verify it compiles**

Run: `cd go && go vet ./...`

**Step 4: Commit**

```
git add go/src/delta/tools/capture.go go/src/delta/tools/main.go
git commit -m "Add --tab-id flag to capture commands

When --tab-id is provided, use extension debugger backend instead of
headless Chrome. When only --url is given, headless behavior is
unchanged. When both are given, attach to the tab and navigate to the
URL before capturing."
```

---

### Task 6: Update gomod2nix and verify nix build

**Step 1: Update gomod2nix.toml**

Run: `cd go && gomod2nix`

**Step 2: Build and verify**

Run: `just build` (full build including extension)
Run: `nix build ./go`

**Step 3: Commit (if gomod2nix.toml changed)**

```
git add go/gomod2nix.toml
git commit -m "Update gomod2nix.toml"
```

---

### Task 7: MCP validation and test updates

**Step 1: Run MCP validation**

Run: `just test-mcp`
Expected: All validations pass (capture tools still have readOnlyHint)

**Step 2: Run full test suite**

Run: `just test`
Expected: All tests pass (capture BATS tests skip due to headless Chrome issue)

**Step 3: Manual end-to-end test**

With the extension loaded and a tab open:

```bash
# Get a tab ID
go/build/release/chrest list-tabs | jq '.[0].id'

# Capture text from that tab
go/build/release/chrest capture-text --tab-id <ID>
```

**Step 4: Commit any test updates if needed**

# Web-fetch Content-Type-Aware Dispatch

## Problem

The `web-fetch` MCP tool fails or degrades on URLs that don't render as
JavaScript-rendered HTML — the case it was designed for. Empirical testing
of the current implementation against `https://raw.githubusercontent.com/...`
and similar download/raw URLs reveals three distinct failure modes:

| URL shape                                                                         | Current behavior                                                                                                    |
| --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Raw text/plain (`.md`, `.toml`, source code)                                      | Body comes through (Firefox auto-wraps in `<pre>`), but TOC is always empty so `selector` is broken on these pages. |
| Non-2xx responses (404, 500)                                                      | Tool silently returns the error body as if it were the document. No HTTP status surfaced.                           |
| Binary downloads (`.tar.gz`, image, archive served as `application/octet-stream`) | MCP schema validation crashes — the `text` field is undefined. Tool is unusable.                                    |

The shared root cause: the pipeline assumes every URL is HTML renderable by
Firefox, so it sends every URL through the same `MultiExtract` flow without
inspecting the response. The right behavior depends on HTTP status and
`Content-Type`.

## Decision

**Content-type-aware dispatch via WebDriver BiDi network interception.**
Before Firefox processes a top-level navigation, intercept the response at the
`responseStarted` phase, classify by status + content-type +
`Content-Disposition`, and branch:

- **HTML family** → release intercept, existing `MultiExtract` flow unchanged.
- **Text family** → release intercept, do `MultiExtract` for the body, then
  rebuild the markdown/html/TOC slots from the text body so the TOC has real
  heading anchors.
- **Binary or attachment** → fail the request via BiDi
  (`network.failRequest`); return a structured error block.
- **Non-2xx** → fail the request; return a structured error block with the
  status code and a body preview.

This is one fetch per URL, reuses the existing Firefox + BiDi infrastructure
already in `go/src/charlie/firefox/`, and addresses all three failure modes
with a single mechanism.

## Architecture

```
  web-fetch handler                           BiDi
  ─────────────────                           ────
  main.go web-fetch       ┐                  ┌─ network.addIntercept (responseStarted)
                          │                  │
   1. add intercept       │                  │
   2. browsingContext.    │── firefox.Session ─→│   network.responseStarted event
      navigate            │                  │   (status + headers)
   3. wait for response   │                  │
      event               │                  │
   4. rawfetch.Classify   │                  │
   5a. continueResponse → │                  │   navigate completes
       extract via        │                  │
       MultiExtract       │                  │
   5b. failRequest →      │                  │
       error block        │                  │
```

### Package placement (NATO hierarchy)

- **charlie/rawfetch/** — new package
  - `classify.go` — pure function: `Classify(headers, urlStr, status) → Class`
    (`HTML`, `Text`, `Binary`, `HTTPError`)
  - `build_from_text.go` — `BuildFromText(body, contentType, finalURL) → Result`
    populating `text`, `markdown`, `html` slots
  - `toc.go` — `ExtractMarkdownTOC(body) → []markdown.TOCEntry` via regex
    over `^#{1,6}\s+...` lines, skipping fenced code regions
- **charlie/firefox/** — extend `Session` with intercept primitives
  - `AddResponseIntercept(ctx, urlPattern) → InterceptID`
  - `WaitInterceptedResponse(ctx, id) → InterceptedResponse`
  - `ContinueResponse(ctx, request)` / `FailRequest(ctx, request)`
- **cmd/chrest/main.go** — web-fetch handler orchestrates intercept setup,
  navigation, classification, and branches to either MultiExtract or error
  blocks.

### Cache

Existing per-URL session cache stays as-is. Cache entry gains a `Path string`
field (`"firefox"` | `"raw-text"` | `"intercepted-error"`) for diagnostics.
Errors are not cached. `refresh: true` re-fetches as today.

## Classification

Explicit table, not heuristic:

| Content-Type prefix / disposition / extension                                                                                                                                      | Class     | Path                                           |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- | ---------------------------------------------- |
| `text/html`, `application/xhtml+xml`, `image/svg+xml`                                                                                                                              | HTML      | Firefox MultiExtract                           |
| `text/plain`, `text/markdown`, `text/x-*`, `application/json`, `application/xml`, `application/x-yaml`, OR URL ext in `.md .txt .json .toml .yaml .yml .go .py .rs .c .cpp .h .sh` | Text      | Firefox extract + rebuild slots from text body |
| `Content-Disposition: attachment` (any type)                                                                                                                                       | Binary    | `failRequest` + error block                    |
| Status not in 200–299                                                                                                                                                              | HTTPError | `failRequest` + error block                    |
| Anything else (`application/octet-stream`, `image/*`, `application/zip`, etc.)                                                                                                     | Binary    | `failRequest` + error block                    |

## Error Handling

| Case                                                     | Response                                                                                                                   |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| Non-2xx HTTP status                                      | `failRequest`. Error block: `"HTTP <status> from <finalURL>"` + first 1 KB of body if available. No resource_links.        |
| Binary content-type or `Content-Disposition: attachment` | `failRequest`. Error block names content-type and (if known) content-length, suggests `chrest capture` for binary capture. |
| `Content-Length` > 10 MB cap (when present)              | `failRequest`. Error block.                                                                                                |
| Body cap with no Content-Length                          | Cannot pre-cap at intercept phase; rely on Firefox limits + post-extract size check. Error if extracted body > cap.        |
| Network error (DNS, TLS, timeout)                        | Error block with wrapped underlying error.                                                                                 |
| Cache invariant                                          | Errors are not cached. `refresh: true` re-fetches.                                                                         |
| Diagnostics                                              | `log.Printf("web-fetch: dispatch=%s ct=%s status=%d url=%s", path, ct, status, url)` per request.                          |

## Testing

Two layers:

### Unit (Go, fast, deterministic)

- `rawfetch/classify_test.go` — table-driven over content-type × URL extension
  × disposition × status.
- `rawfetch/toc_test.go` — fixture markdown bodies → expected `[]TOCEntry`.
  Cover ATX headings, code fences (`#` inside fences must not match), HTML
  comments.
- `rawfetch/build_test.go` — text/markdown/html slot construction.
- BiDi intercept driver tests with `httptest.Server` standing in, asserting
  the right BiDi commands fire for each classification.

### BATS integration (`zz-tests_bats/web_fetch.bats`, new file)

URLs pinned to commit SHAs on GitHub for stability:

1. HTML URL (real page) → existing TOC + body. Regression check.
2. Raw `.md` URL (pinned SHA) → body present + TOC populated with real
   heading anchors. New capability.
3. Raw `.toml` URL (pinned SHA) → text body, empty TOC, no error.
4. URL that 404s → structured error with `404`, no resource_links, no schema
   crash.
5. Binary URL (small PNG pinned SHA) → structured error mentioning content-
   type, no schema crash.

## Rollback / Dual-architecture

**Env flag:** `CHREST_WEB_FETCH_DISPATCH`, two values:

- `bidi-intercept` (default, new path).
- `firefox-only` (today's all-Firefox-no-classification behavior, preserved
  verbatim for one promotion period).

**Rollback procedure:** set `CHREST_WEB_FETCH_DISPATCH=firefox-only` in the
env block of the MCP server config (`~/.claude.json`) or the shell env, then
restart the session. Single-config flip. No commit revert.

**Promotion criterion:** 7 days running with default + zero `firefox-only`
overrides observed in stderr logs → delete the env flag and the
`firefox-only` code path entirely.

**Diagnostics during dual-arch:** every web-fetch request logs
`web-fetch: dispatch=<path> ct=<content-type> status=<n> url=<url>` to stderr.
Lets us observe path distribution and confirm classification matches
expectations.

## Spike Results

The BiDi interception spike (`go/src/charlie/firefox/intercept_spike_test.go`,
runnable via `just explore-bidi-intercept`) confirmed every BiDi mechanism
this design depends on against Firefox 150.0:

- `network.addIntercept` with phase `responseStarted` accepted, returns an
  intercept id.
- `network.responseStarted` event fires for top-level navigations with
  `isBlocked=true`, the matching `intercepts: [id]` list, the request id we
  need for continue/fail, and full headers (including `Content-Type`).
- `network.continueResponse` releases the response; `Navigate` then
  completes normally.
- `network.failRequest` aborts the request cleanly; `Navigate` returns a
  BiDi error wrapping `NS_ERROR_ABORT`.

Spike total wall-clock: 1.5 s.

### Implementation Gotchas Surfaced by the Spike

1. **Navigate blocks on `wait: complete`.** Classification and dispatch must
   happen on a separate goroutine that consumes the `responseStarted` event
   while the navigate call is in flight. The spike uses this pattern and it
   works; the production handler should follow suit.

2. **`failRequest` makes Navigate return an error.** The web-fetch handler
   must recognise a `NS_ERROR_ABORT`-class BiDi error following an explicit
   `failRequest` as an _expected_ outcome of the binary / HTTPError branch,
   not as a failure to surface. Suggested approach: track "we issued
   `failRequest`" alongside the intercept dispatch, and swallow the
   corresponding Navigate error in that case only.

## Open Questions

- **Markdown → HTML rendering for the `html` slot.** v1 wraps raw text in a
  minimal `<pre>` for the html slot. Future work could add a real markdown→
  HTML renderer (e.g. goldmark) for full-fidelity html slots on `.md` URLs.
  Out of scope for this design.

# Graceful Degradation When a Page's `load` Event Never Fires

## Problem

`fetchViaDispatch` (the MCP `capture` tool's markdown/text/html path, added by
the [content-type dispatch design](2026-04-29-web-fetch-content-type-dispatch-design.md))
calls `firefox.Session.Navigate`, which sends `browsingContext.navigate` with
`wait: "complete"` — a hard block on Firefox's native `load` event. Some
real-world pages never fire `load` within the 30s BiDi RPC timeout, even
though every subresource chrest observes is classified and released
correctly.

Confirmed via `CHREST_BIDI_DEBUG=1` tracing (amarbel-llc/eng#251) against
`https://web.dev`: the top-level navigation classifies as HTML at +0.4s,
~30 subresources (images, CSS, fonts, JS) are continued cleanly through
+2s, then three waves of delayed analytics/RUM beacons arrive and are
continued at +2s, +7s, and +11s — then total silence for the remaining
~18s. `navHandled` is already `true` (the page is classified and
extractable) when the RPC finally times out at 30s. The most likely cause:
a JS-scheduled beacon fired 15-30s after load, targeting a host that never
completes a BiDi response cycle at all (unreachable, hanging, or otherwise
invisible to the intercept) — so there is nothing for the dispatcher to
see, classify, or release. This is not the chrest#66 buffer-overflow bug
(that fix works as designed here); it's a distinct gap where the page
simply never signals "done."

**Symptom today:** the caller gets a hard error after 30s with no content,
even though a real, extractable HTML document was observed and classified
within the first second.

## Scope

This design covers only the MCP `capture` tool's `fetchViaDispatch` path
(markdown/text/html formats) — the one path with a BiDi response-intercept
stream to drive an idle-based heuristic. CLI `chrest capture`, pdf/
screenshot-png, and the cutting-garden `capture-batch`/`capture-serve`
plugin all route through `tools.MultiExtract` → `Session.Navigate` directly,
with no intercept stream to observe; they keep today's strict `wait:
"complete"` behavior. Converging all three capture surfaces onto one
capability set is tracked separately (amarbel-llc/eng#253) and is
explicitly out of scope here — folding it in would turn a bug fix into an
open-ended architecture change.

## Decision

**Default to a network-idle heuristic; keep strict `load`-wait as an
explicit opt-out.**

Stop blocking `Navigate` on Firefox's own `wait: "complete"`. Instead:

1. Send `browsingContext.navigate` with `wait: "none"` — returns almost
   immediately once the navigation is committed.
2. The dispatcher goroutine in `fetchViaDispatch` (which already consumes
   every intercept event to classify and continue/fail requests) gains a
   resettable idle timer alongside its existing `select` cases
   (`<-events`, `<-ctx.Done()`). Every intercept event — nav or subresource
   — resets the timer.
3. Once the top-level nav has classified (`navHandled == true`), if the
   idle timer elapses with no further events, treat the page as settled:
   stop waiting and proceed to extraction. `ExtractText`/`GetDocumentHTML`
   read DOM state at call time regardless of how "done" was decided, so no
   extraction-side change is needed.
4. If the idle timer elapses *before* `navHandled` (the top-level response
   itself never arrives), that's still a real failure — surface today's
   error, unchanged.

New MCP `capture` tool param, `wait-strategy` (default `graceful`):

- `graceful` (default) — the idle-heuristic behavior above.
- `strict` — today's behavior verbatim: real `wait: "complete"`, hard
  30s error, no idle escape hatch. Selects the pre-this-design code path.

## Diagnostics

When the idle-timeout path is what ended the wait (as opposed to a genuine
Firefox-signaled completion), the tool result gets an additional text
block: `"page did not settle within Ns of network silence; content
reflects the last observed network activity and may be incomplete"` —
matching the existing diagnostic-block pattern used by
`capture_empty_extraction_returns_diagnostic` (`zz-tests_bats/capture_mcp.bats`).
This makes the degraded case observably different from a fully-settled
capture without treating it as an error.

Every dispatch already logs `capture: dispatch=bidi-intercept class=...`
(main.go). Add one line when the idle timer fires:
`capture: idle-timeout after Nms since last event, treating as settled
url=<url>`.

## Tuning Levers

- **Idle window, default 15s.** Chosen from the observed trace: real gaps
  between legitimate late beacons reached 11s; 15s gives headroom above
  that without waiting close to the old 30s ceiling. Exposed as an
  optional MCP param (`idle-timeout-ms`) so it's adjustable without a code
  change. Revisit if real-world captures either (a) cut off pages that are
  still legitimately loading past 15s of silence, or (b) still time out
  because some sites have idle gaps longer than 15s between real
  deliveries.

## Rollback / Dual-architecture

**Param flag:** `wait-strategy: "graceful" | "strict"` on the `capture` MCP
tool, defaulting to `graceful`.

**Rollback procedure:** callers pass `wait-strategy: "strict"` per-call to
opt back into today's exact behavior; no server restart or config change
needed since it's a per-request param, not an env var. If the default
itself needs to flip back, that's a one-line change to the param's default
value.

**Promotion criterion:** none needed in the traditional sense — `strict`
isn't slated for deletion, since it remains a legitimate opt-in for
callers who need real `load`-event semantics (e.g. verifying a page fully
finished loading before treating a capture as canonical). This is a
permanent two-mode surface, not a temporary migration.

## Testing

Hermetic fixture (replaces the live `capture_web_dev_navigate_timeout` bats
test once landed): a local HTTP server serving a page whose JS schedules
`fetch()` to a non-routable target (e.g. `10.255.255.1`, or a local
listener that accepts and never responds) at a delay past the idle window
but within the old 30s ceiling — mirroring the `setTimeout`-deferred-beacon
shape observed against `web.dev`. Asserts:

1. `wait-strategy: graceful` (default) → capture succeeds, returns the
   real page content, and includes the "did not settle" diagnostic block.
2. `wait-strategy: strict` → capture still hard-times-out after 30s,
   preserving today's behavior for callers who ask for it.
3. A normal fixture with no hanging request → both strategies succeed
   identically, with no diagnostic block in either case (regression guard
   against false-triggering the idle path on ordinary pages).

The existing `capture_subresource_heavy_page_completes` and
`capture_many_subresources_overflow_buffer` tests are unaffected — they
already complete well within any idle window.

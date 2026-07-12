---
status: accepted
date: 2026-04-20
promotion-criteria:
---

# Web-archive capture (cutting-garden RFC 0003 binding)

## Problem Statement

Preserving a web page at a moment in time — for archival, for offline reading, for feeding to downstream tools (LLMs, full-text search, reader apps) — requires a patchwork of shell scripts, browser plug-ins, and manual browser operations. Chrest already launches browsers and talks to them; it's the natural place to collapse those tasks into one tool that produces reproducible outputs in whichever format the consumer wants.

## Role

Chrest is the reference implementation of the **web-archive binding** of the **Capture Plugin Protocol**:

- [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) — the abstract protocol (orchestrator / capture plugin / writer; merkle tree of typed hyphence blobs; subprocess form canonical).
- [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) — pins the `web` capture kind's plugin-defined node-type schemas (browser/DNS/extensions/isolation in identity; HTTP response in outcome), the payload format catalog, per-format normalization rules, and the capabilities artifact.

A web-archive plugin's receipt blob carries the type line `! cutting_garden-capture-receipt-web-v1`; the plugin discriminator inside the merkle tree (`environment.binary.name`) identifies chrest as the binary that produced the bytes. Other web-archive plugins (hypothetical wkhtmltopdf-based, monolith-only) slot into the same kind.

This FDR documents chrest's capture-pipeline surface — the interactive `chrest capture --format` and orchestrator-driven `chrest capture-batch` / `chrest capture-serve` commands. Both transports are merged and emit the RFC 0002+0003 conforming receipt shape (chrest#83, chrest#98); RFC 0008 was accepted by cutting-garden 2026-07-12. See [Implementation status](#implementation-status-migration-to-rfc-00020003) for the remaining RFC 0003 gaps (capabilities artifact, DNS/fetched-extension fields) and the node-body schema history (v1→v2, chrest#102).

## Interface

Two top-level commands, one interactive and one batch-oriented:

### `chrest capture --format <kind> [flags]`

Single-page capture. Streams bytes straight to stdout (or to `--output <path>` atomically).

**Formats (10):**

| Format              | Payload                                                                   | Media type                     |
| ------------------- | ------------------------------------------------------------------------- | ------------------------------ |
| `pdf`               | PDF document from the browser's print pipeline                            | `application/pdf`              |
| `screenshot-png`    | Full-page or viewport PNG                                                 | `image/png`                    |
| `screenshot-jpeg`   | Full-page or viewport JPEG (tunable `--quality`)                          | `image/jpeg`                   |
| `mhtml`             | Firefox MHTML snapshot (not yet functional — returns unsupported error)   | `multipart/related`            |
| `a11y`              | Accessibility tree JSON (not yet functional — returns unsupported error)  | `application/json`             |
| `text`              | `document.body.innerText`                                                 | `text/plain; charset=utf-8`    |
| `html-monolith`     | Rendered DOM inlined by `monolith` — every asset as a `data:` URI         | `text/html; charset=utf-8`     |
| `markdown-full`     | Rendered DOM converted to markdown                                        | `text/markdown; charset=utf-8` |
| `markdown-reader`   | Readability-extracted article converted to markdown                       | `text/markdown; charset=utf-8` |
| `markdown-selector` | CSS-selector-scoped element converted to markdown (requires `--selector`) | `text/markdown; charset=utf-8` |

**Backend:** headless Firefox via WebDriver BiDi (sole backend since chrest#47).

**Flags:**

- `--url <url>` — page to navigate to (required).
- `--output <path>` — atomic tmpfile + rename; no file left behind on failure.
- `--timeout <dur>` (default 60s) — deadline-backed context; cancels the browser and writer on expiry.
- `--selector <css>` — required for `markdown-selector`; first match wins.
- `--reader-engine <readability|browser>` — `markdown-reader` only. `readability` (default) uses the embedded Go Readability port. `browser` is reserved and rejects with `not-yet-implemented`.
- Format-specific flags: `--landscape`, `--no-headers`, `--background` (PDF), `--quality` (JPEG), `--full-page` (screenshots).

The CLI exits non-zero on any error.

### `chrest capture-batch`

JSON-stdin / JSON-stdout batch capture. This subcommand fills the **capture plugin** role of [cutting-garden RFC 0002 §Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md#capture-plugin-protocol) under the **web-archive binding** ([RFC 0003](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md)) over the subprocess (v1) transport — stdin/stdout batch, one `writer.cmd` subprocess per blob. See `chrest capture-serve` below for the RFC 0008 JSON-RPC transport (v2, SEQPACKET + fd-passing).

Wire shape (`capture-plugin/v1`, chrest#83):

- Input: `{schema, writer:{cmd}, target, defaults:{normalize, plugin:{browser}}, captures:[{name, format, options?}]}`.
- Runs every capture sequentially: navigate, capture the format's bytes, optionally normalize (byte-stability residue moves into the outcome subtree), then assemble the capture's **receipt**.
- Each capture's receipt is a merkle tree of typed hyphence blobs — `invocation → host → binary → plugin-environment → environment → plugin-outcome → outcome → payload → identity → receipt`, built via the shared `capture_plugin.WriteReceipt` so the bytes are identical to an in-process binding's. Every node is streamed through the orchestrator-supplied `writer.cmd` subprocess (one invocation per blob) as it's built.
- Output: `{schema, plugin:{name,version}, errors:[], captures:[{name, receipt:{id,size}} | {name, error:{kind,message}}]}` — exactly one `receipt` ref per capture (down from the legacy `spec`/`envelope`/`payload` triple); every other node is recoverable by tree-walking the receipt.
- chrest's two plugin-namespaced node bodies (schema v2, chrest#102): `!jcs-chrest-capture-environment-v2` (browser name/version/user-agent/platform, `extensions:[]`, `isolation:"fresh"` — chrest opens a fresh Firefox session per capture; deliberately carries no `command_line`) and `!jcs-chrest-capture-outcome-v2` / `-v2-preview` (`process.command_line` — the browser's launch argv as observed, a per-run observation, present whenever available — plus `http.{status, headers (lowercased names, order+dupes preserved), timing_ms:{load}, final_url?}` when an HTTP response was observed; the node type is the full `-v2` schema iff `http.*` was observed, else `-v2-preview` — `process` presence never affects that choice).
- Per-capture options echo into the invocation node's `options` field via JCS canonicalization (defaults to `{}`, never omitted) so downstream consumers can reproduce the exact extraction parameters.

See [RFC 0002 §Migration from web-capture-archive/v0+v1](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) and [RFC 0003 §Compatibility](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md#compatibility) for the field-by-field relocation table from the legacy nebulous RFC 0001 shape.

### `chrest capture-serve`

The RFC 0008 (`capture-plugin/v2`) JSON-RPC transport for the same capture-plugin role — merged and conformance-verified (chrest#98; see [Implementation status](#implementation-status-migration-to-rfc-00020003)). Launched by an orchestrator, never invoked directly by a human.

Bring-up: the orchestrator sets `CAPTURE_PLUGIN_COOKIE` and spawns `chrest capture-serve`; the process reads the cookie (refusing to serve without it — exit 1, nothing on stdout), binds a fresh `unixpacket` rendezvous socket, prints exactly one announce line on stdout, and accepts the orchestrator's dial. From there `initialize` and `capture.batch` are answered exactly like `capture-batch`'s receipt assembly (same `runOneWithWriter` code path), except each node blob travels over the RFC 0008 blob protocol (`blob.begin`/`blob.finish` + `SCM_RIGHTS` fd-passing) instead of a spawned `writer.cmd`. `stdin` EOF, a `shutdown` notification, or `SIGTERM` end the session cleanly; a control-socket EOF with no `shutdown` is cancellation (nonzero exit).

cutting-garden's web plugin orchestrator always tries `capture-serve` first, falling back to `capture-batch` on a clean bring-up failure — with the binary on `PATH`, cutting-garden selects it automatically, no flag or config needed on either side.

## Examples

Single-page captures piped to stdout or files:

    $ chrest capture --format pdf --url https://example.com > page.pdf

    $ chrest capture --format screenshot-png --full-page \
        --url https://en.wikipedia.org/wiki/Markdown \
        --output wiki.png

    $ chrest capture --format html-monolith \
        --url https://simonwillison.net/ \
        --output blog.html

    $ chrest capture --format markdown-reader \
        --url https://simonwillison.net/2026/Feb/15/gwtar/ \
        --output gwtar.md

    $ chrest capture --format markdown-selector --selector "#bodyContent" \
        --url https://en.wikipedia.org/wiki/Markdown \
        --output wiki.md

Batch capture (`capture-plugin/v1` shape):

    $ echo '{
        "schema":   "capture-plugin/v1",
        "writer":   {"cmd": ["madder", "--format=json", "write", "--store", "archive"]},
        "target":   "https://en.wikipedia.org/wiki/Ferris_wheel",
        "defaults": {"normalize": true, "plugin": {"browser": "firefox"}},
        "captures": [
          {"name": "pdf",     "format": "pdf"},
          {"name": "md",      "format": "markdown-reader"},
          {"name": "archive", "format": "html-monolith"}
        ]
      }' | chrest capture-batch | jq

Every capture produces one `receipt` ArtifactRef — a root markl-id over the capture's whole merkle tree — so the archive is content-addressed and re-derivable by walking the receipt.

## Limitations

- **`mhtml` and `a11y` are not yet functional.** Both return an unsupported error; they were Chrome-only and Chrome was removed in chrest#47. Implementing them over Firefox/BiDi is a future follow-up.
- **`markdown-selector` takes the first match only.** No `--selector-mode=all` or similar. Selector misses are a typed error that names the selector.
- **`--reader-engine=browser` is reserved but not implemented.** The Firefox `about:reader` engine is accepted as a valid flag value so the spec surface stays stable but rejects with `not-yet-implemented` at runtime.
- **`html-monolith` requires the `monolith` binary on `PATH`.** The nix-built `chrest` wraps it in via `flake.nix` `postFixup`; a `go install`-ed chrest relies on the user's PATH.
- **`capture-batch` only has a byte-stability normalizer for `text`, `screenshot`, `pdf`, and `mhtml`.** `html-monolith` and `markdown-*` are recorded verbatim regardless of `defaults.normalize`; no normalization residue moves into the outcome subtree for those formats.
- **BiDi network-event buffer drops events on heavy pages** (chrest#33). Can cause a capture to miss `LastNavigationHTTP()`, which drops the `http.*` half of that capture's plugin-outcome node (the node itself still emits, as the `-v2-preview` variant, since `process.command_line` is available independent of HTTP observation) — harmless for the payload itself.
- **No capabilities artifact.** RFC 0003 defines `!jcs-chrest-capture-capabilities-v1` (`formats`, `browsers`, `normalizes`, `honors_dns`, `honors_preinstalled_extensions` / `honors_fetched_extensions`, `transport`) referenced from `environment.binary.capabilities_id`; chrest doesn't emit one yet (#53).
- **`dns` is omitted from the plugin-environment node** — chrest doesn't honor or observe DNS resolution (#56).
- **Only `source: "preinstalled"` extensions are reported**, and only because the orchestrator can request them — chrest always emits `extensions: []` today since `CaptureSpec` carries no extensions field on the wire yet. `source: "fetched"` (plugin-driven URL fetch into the blob store) is unimplemented (#55).
- **`resolved_ip` is omitted from the outcome's `http` object** — BiDi has no remote-IP field to source it from (#52).

## Implementation status: migration to RFC 0002+0003

### Phase 1 — subprocess (v1) receipt emitter: landed (chrest#83)

`capture-batch` emits the `capture-plugin/v1` receipt shape described above.
Implementation lives in `go/internal/echo/capturebatch/` (`types.go` wire
structs, `mapping.go` chrest-owned node bodies, `receipt.go` builds the tree
via `cutting-garden/pkgs/capture_plugin.WriteReceipt` + a `cmdWriter` adapter
over `writer.cmd`, `runner.go` drives the batch). The legacy
`web-capture-archive/v0+v1` emitters (`spec.go`, `envelope.go`,
`fingerprint.go`) are deleted; `capture_plugin.JCS` replaces the homegrown
canonicalizer on the receipt path (`jcs.go` survives only to back the
standalone `chrest-jcs` byte-stability tool).

Remaining gaps against a fully RFC 0003-conformant plugin, tracked as
follow-ups and listed in [Limitations](#limitations): no capabilities
artifact (#53), `dns` omitted (#56), only `source: "preinstalled"`
extensions and always `[]` today since the wire carries no extensions field
yet (#55), `resolved_ip` omitted from the outcome (#52).

**Dependency note (updated 2026-07-12):** `cutting-garden` (module path
`code.linenisgreat.com/cutting-garden`, following its own rename off
`github.com/amarbel-llc/cutting-garden`) is bridged through the
flake-input-go_mod bridge (`go/gomod.nix`), the same mechanism chrest
uses for its other amarbel-llc dependencies — a real flake input fetched
over SSH from the forge at nix-eval time, with no `subPath` (cutting-garden's
module is at its repo root). Its transitive bridges (`madder/go`,
`hyphence/go`, `piggy/go`, `tap/go`, `crap/go-crap/v2`, `tommy`) are
inherited automatically at depth-1 through cutting-garden's own
`passthru.goFlakeInputs` — chrest does not re-declare them.

This was previously blocked (chrest#98): bridging cutting-garden inherits
its `/v2+` transitive deps (`crap/go-crap/v2`), and igloo's `mkMergedGoMod`
used to synthesize an invalid `v0.0.0` sentinel for a `/v2` module in that
path. Fixed as of igloo `d1c081d` (igloo#38's major-aware `sentinelFor` +
the conditional-require path). Cross-repo diagnosis credit: igloo/plain-ebony
(igloo#54).

A second, distinct gap surfaced once the bridge itself worked: the flake
bridge only fixed the _nix build_ path — plain devshell Go tooling
(`go build`, `go test`, `go vet`, `dagnabit`) still couldn't resolve
`pkgs/capture_plugin` / `pkgs/capture_serve`, since there was no `go get`
path to the forge (no vanity `go-import` meta, and the
`amarbel-llc/cutting-garden` GitHub mirror is archived/frozen at
`v0.1.24`). **Resolved 2026-07-12**: `code.linenisgreat.com` now serves
`go-import` meta (`linenisgreat#64`, verified by `circus#100`).
`go/go.mod` carries a plain `require code.linenisgreat.com/cutting-garden
<pseudo-version>` (no `replace` needed — the module path itself is now
canonical); `flake.nix`'s devShell `shellHook` sets
`GOPRIVATE=code.linenisgreat.com` so plain `go` tooling skips
`GOPROXY`/`GOSUMDB` for that host and resolves it directly, same as the
nix build already did via the flake bridge.

### Phase 2 — `capture-serve` JSON-RPC transport (RFC 0008): merged

**RFC 0008 was accepted by cutting-garden on 2026-07-12**, citing
chrest's real-capture conformance run (below) as ratification evidence
alongside cutting-garden's own pinned-input strict-identity tests.

Implementation: `go/cmd/chrest/capture_serve.go` (bring-up — `CookieFromEnv`
→ `ListenRendezvous` → print `AnnounceLine` on stdout → `AcceptUnix` →
`Serve`, with a lifecycle wrapper for stdin-EOF/SIGTERM, including closing
the listener on lifecycle-context cancellation so a pending `AcceptUnix`
doesn't block forever if the orchestrator never dials — a real gap this
implementation and cutting-garden's own reference plugin
(`internal/capture_serve_testpeer`) each found independently) +
`go/internal/echo/capturebatch/serve.go` (`NewBatchHandler` adapts the
same `runOneWithWriter` receipt-assembly path `capture-batch` uses to
cutting-garden's `capture_serve.BatchFunc` — `capture_plugin.WriteReceipt`
really is reused unchanged; the only wire-level difference is per-capture
`options`, pre-parsed as `map[string]any` on the v2 wire and JSON-round-tripped
back to reach the shared `Resolved` type).

Verified in three layers, all in `go/cmd/chrest/`:

- `capture_serve_test.go` — real end-to-end capture (real handshake, real
  headless Firefox, real receipt through the real blob-protocol transport)
  and the `CAPTURE_PLUGIN_COOKIE` guard (missing cookie → exit 1, empty
  stdout, per RFC 0008 §Handshake), plus `TestCaptureServeExitsOnStdinEOFBeforeDial`
  (the `AcceptUnix`-lifecycle gap above).
- `capture_serve_conformance_test.go` — RFC 0008 §Conformance: the same
  target/format captured through both `capture-batch` and `capture-serve`
  produce equivalent receipt node sequences. A literal byte-for-byte diff
  of two independent real captures is the wrong test (it would fail even
  v1-vs-v1): two fields are legitimately per-run — cutting-garden's own
  `outcome.datetime` (by design) and chrest's `outcome.process.command_line`
  (chrest#102, below — an intentional per-run observation, not
  identity). Excluding exactly those two, every node's type, ref
  structure, and body match exactly. Passes.

RFC 0008's own text was revised during this work: §Launch now specifies
the announce/dial rendezvous-socket handshake (cookie env var, six-field
announce line, short `0700` rendezvous socket, stdin-EOF lifecycle,
accept-unblock as an explicit MUST) as normative, superseding an earlier
inherited-fd (`CAPTURE_PLUGIN_CONTROL_FD` + `socketpair`) design that never
shipped. With `chrest capture-serve` on `PATH`, cutting-garden's web
plugin auto-selects it — no cutting-garden-side flag or config needed.

`ListenRendezvous` (cutting-garden's own helper, linked unchanged) handles
the socket-path-length constraint internally (`sun_path` is ~108 bytes; it
prefers `$XDG_RUNTIME_DIR`, falls back to `/tmp`, and skips a base whose
path would overflow — never a deep worktree-relative tmp path), so this is
not chrest's own concern to get right.

### chrest#102 — `command_line` relocated to outcome (RFC 0003 v2): fixed

Building the RFC 0008 conformance test above surfaced a real bug that
turned out to be an RFC 0003 spec issue, not just a chrest one:
`environmentBody`'s `browser.command_line` (sourced from
`firefox.BrowserInfo.CommandLine` = the real Firefox launch argv)
embedded a randomly-generated profile temp-directory path per launch.
RFC 0003 §Identity Tree explicitly classified `command_line` as
identity-affecting — so this random per-launch data sat in a field the
spec expected to be stable, meaning two functionally-identical captures
never shared an identity markl-id, undermining the RFC 0002
identity/dedup model's whole purpose.

The RFC author (the operator) and cutting-garden (sharp-hazel) agreed
this was a real, spec-level gap, not just an implementation quirk: RFC
0003 already states the governing principle one row up
(`browser.prefs`'s rationale — "non-rendering settings excluded... so
identity is not polluted with... noise"), and every other identity field
is stable config or stable fact except this one. cutting-garden owns and
made the RFC edit (commit `a6548e5`); chrest implements against it here.

**Spec change:** `command_line` moved out of §Identity Tree into
§Outcome Tree — plugin Slot, as `process.command_line`, a sibling of
`http`. Both plugin node type strings bumped: `!jcs-<plugin>-capture-
environment-v2` (loses `command_line`) and `!jcs-<plugin>-capture-
outcome-v2` / `-v2-preview` (gains `process`). §Preview Schema was
clarified: the `-v2-preview` marker is keyed on `http.*` completeness
**alone** — `process.command_line` presence neither requires nor lifts
it; a preview node still SHOULD carry `process.command_line`. RFC 0010
governs the v1→v2 bump: v1 readers are retained, new captures MUST
write v2, no stored receipt is rewritten.

**chrest implementation:** `mapping.go`'s `environmentBody` no longer
emits `command_line` under any circumstance; `outcomeBody` (renamed from
`outcomeHTTPBody`) always includes `process.command_line` when
`firefox.BrowserInfo.CommandLine` is non-empty (true for essentially
every real capture — a session is always open by the time `buildReceipt`
runs) and selects `-v2` vs `-v2-preview` purely on whether an HTTP
response was observed. The plugin-outcome node is now emitted for
`file://` and other non-HTTP targets too (previously omitted entirely
when there was no HTTP response) — see
`TestBuildReceiptEmitsPreviewOutcomeWithoutHTTP`.

## More Information

- [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) — canonical abstract protocol (orchestrator / capture plugin / writer; merkle tree of typed hyphence blobs). Link points at the archived GitHub mirror (frozen at v0.1.24, still readable); canonical development has moved to the self-hosted forge (see Implementation status below).
- [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) — canonical web-kind binding chrest implements. Same archived-mirror caveat as above.
- cutting-garden RFC 0008 — `capture-serve` JSON-RPC transport, `accepted` 2026-07-12 (chrest's conformance run cited as ratification evidence) — not yet mirrored to a stable GitHub link; see the forge repo for the current doc.
- [nebulous RFC 0001 — Web Capture Archive Protocol](https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md) — origin RFC; superseded by cutting-garden RFC 0002+0003 paired. Retained as historical reference.
- `code.linenisgreat.com/cutting-garden/pkgs/capture_plugin` — the shared Go package chrest imports for `WriteReceipt`/`BuildNode`/`JCS`/`Writer` (see `go/internal/echo/capturebatch/receipt.go`).
- Related chrest issues: chrest#10 (original html-to-pdf migration, closed), chrest#11 (multi-format aggregator, closed, superseded), chrest#26 (html-monolith, closed), chrest#29 (markdown variants, closed), chrest#33 (BiDi buffer drops), chrest#34 (capture exit-code, closed), chrest#47 (Chrome CDP removal, closed), chrest#83 (RFC 0002+0003 receipt-emitter migration, landed), chrest#98 (igloo `/v2` sentinel bug blocking the cutting-garden flake-input bridge, fixed and merged), chrest#102 (`command_line` identity-node volatility → RFC 0003 v2, fixed and merged).
- `linenisgreat#64` — vanity `go-import` endpoint for forge-migrated repos + module-path migration (first half — the endpoint — landed and resolved chrest's devshell Go-tooling gap; the module-path-rename second half is cutting-garden's own, also landed).

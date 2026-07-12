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

This FDR documents chrest's capture-pipeline surface — the interactive `chrest capture --format` and orchestrator-driven `chrest capture-batch` commands. `capture-batch` emits the RFC 0002+0003 conforming receipt shape (chrest#83); see [Implementation status](#implementation-status-migration-to-rfc-00020003) for the remaining gaps (capabilities artifact, DNS/fetched-extension fields) and for the RFC 0008 `capture-serve` transport, which is not yet implemented on the chrest side.

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

JSON-stdin / JSON-stdout batch capture. This subcommand fills the **capture plugin** role of [cutting-garden RFC 0002 §Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md#capture-plugin-protocol) under the **web-archive binding** ([RFC 0003](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md)) over the subprocess (v1) transport — stdin/stdout batch, one `writer.cmd` subprocess per blob. The RFC 0008 `capture-serve` JSON-RPC transport (v2, SEQPACKET + fd-passing) is a separate, not-yet-implemented command; see [Implementation status](#implementation-status-migration-to-rfc-00020003).

Wire shape (`capture-plugin/v1`, chrest#83):

- Input: `{schema, writer:{cmd}, target, defaults:{normalize, plugin:{browser}}, captures:[{name, format, options?}]}`.
- Runs every capture sequentially: navigate, capture the format's bytes, optionally normalize (byte-stability residue moves into the outcome subtree), then assemble the capture's **receipt**.
- Each capture's receipt is a merkle tree of typed hyphence blobs — `invocation → host → binary → plugin-environment → environment → plugin-outcome (if HTTP observed) → outcome → payload → identity → receipt`, built via the shared `cutting-garden/pkgs/capture_plugin.WriteReceipt` so the bytes are identical to an in-process binding's. Every node is streamed through the orchestrator-supplied `writer.cmd` subprocess (one invocation per blob) as it's built.
- Output: `{schema, plugin:{name,version}, errors:[], captures:[{name, receipt:{id,size}} | {name, error:{kind,message}}]}` — exactly one `receipt` ref per capture (down from the legacy `spec`/`envelope`/`payload` triple); every other node is recoverable by tree-walking the receipt.
- chrest's two plugin-namespaced node bodies: `!jcs-chrest-capture-environment-v1` (browser name/version/user-agent/platform, `extensions:[]`, `isolation:"fresh"` — chrest opens a fresh Firefox session per capture) and `!jcs-chrest-capture-outcome-v1` (`http.{status, headers (lowercased names, order+dupes preserved), timing_ms:{load}, final_url?}`; omitted entirely when no HTTP response was observed).
- Per-capture options echo into the invocation node's `options` field via JCS canonicalization (defaults to `{}`, never omitted) so downstream consumers can reproduce the exact extraction parameters.

See [RFC 0002 §Migration from web-capture-archive/v0+v1](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) and [RFC 0003 §Compatibility](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md#compatibility) for the field-by-field relocation table from the legacy nebulous RFC 0001 shape.

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
- **BiDi network-event buffer drops events on heavy pages** (chrest#33). Can cause a capture to miss `LastNavigationHTTP()`, which drops the plugin-outcome node (`http.*`) from that capture's receipt entirely rather than emitting a degraded one — harmless for the payload itself.
- **No capabilities artifact.** RFC 0003 defines `!jcs-chrest-capture-capabilities-v1` (`formats`, `browsers`, `normalizes`, `honors_dns`, `honors_preinstalled_extensions` / `honors_fetched_extensions`, `transport`) referenced from `environment.binary.capabilities_id`; chrest doesn't emit one yet (#53).
- **`dns` is omitted from the plugin-environment node** — chrest doesn't honor or observe DNS resolution (#56).
- **Only `source: "preinstalled"` extensions are reported**, and only because the orchestrator can request them — chrest always emits `extensions: []` today since `CaptureSpec` carries no extensions field on the wire yet. `source: "fetched"` (plugin-driven URL fetch into the blob store) is unimplemented (#55).
- **`resolved_ip` is omitted from the outcome's `http` object** — BiDi has no remote-IP field to source it from (#52).
- **The RFC 0008 `capture-serve` JSON-RPC transport (v2) is not implemented.** cutting-garden's orchestrator always tries `capture-serve` first and falls back to `capture-batch` on clean bring-up failure, so this is currently exercised via the v1 fallback path only. See [Implementation status](#implementation-status-migration-to-rfc-00020003).

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

**Dependency note:** `github.com/amarbel-llc/cutting-garden` and its
transitive `madder/go` resolve _organically_ through `go/gomod2nix.toml`,
not through the flake-input-go_mod bridge (`go/gomod.nix`) that chrest uses
for its other amarbel-llc dependencies. Bridging cutting-garden inherits its
`passthru.goFlakeInputs`, which drags in `/v2+` transitive deps (e.g.
`crap/go-crap/v2`) that igloo's `mkMergedGoMod` synthesizes with an
invalid `v0.0.0` sentinel for a `/v2` module (chrest#98). Re-bridging is
gated on igloo shipping a major-aware sentinel or `goFlakeInputsMode =
"workspace"`; until then, bump the pin with a normal `go get` +
`just build-gomod2nix` cycle.

**Repo-host note (2026-07-12):** cutting-garden's canonical remote moved
off GitHub to a self-hosted Forgejo forge (`git@code.linenisgreat.com:cutting-garden.git`);
the `amarbel-llc/cutting-garden` GitHub mirror is now archived and frozen
as of `v0.1.24`. Because chrest resolves the dependency organically via
the Go module path rather than a GitHub-pinned flake input, existing pins
keep working unmodified — but bumping past `v0.1.24` (needed once
`pkgs/capture_serve` for Phase 2 lands) will need the module path
repointed at the forge remote. File future cutting-garden issues on the
forge, not GitHub.

### Phase 2 — `capture-serve` JSON-RPC transport (RFC 0008): not started

RFC 0008 is still `proposed`, not yet implemented on the chrest side. cutting-garden's
web plugin orchestrator always tries `capture-serve` first and falls back to
`capture-batch` on a clean bring-up failure — so until this lands, and on
any future error path, a missing/failing `capture-serve` must fail fast and
unambiguously (nonzero exit, no hang, no partial stdout) rather than emit
anything malformed.

Coordination with cutting-garden (`sharp-hazel`) settled the handshake
contract and de-risked the core transport mechanism:

- **Phase 0 de-risk spike passed:** `SCM_RIGHTS` fd-passing over a
  `SOCK_SEQPACKET` (`"unixpacket"`) connection works in Go's `net` package
  on Linux — digest identity held across a sequential multi-blob run. No
  fallback transport is needed.
- **Handshake (go-plugin style, per madder RFC 0001):** the orchestrator
  sets `CAPTURE_PLUGIN_COOKIE`; the plugin MUST refuse to serve without it
  (exit 1, nothing on stdout). The plugin calls `net.Listen("unixpacket",
<path>)`, then prints exactly one stdout line:
  `<cookie>|capture-plugin/v2|unixpacket|<socket-path>|<metadata>|capture-plugin`.
  Any other stdout output before that line is a rejected handshake.
- **Socket path length:** `sun_path` is ~108 bytes; a nested worktree
  `$TMPDIR` overflows it (`bind: invalid argument`, confirmed empirically
  in the spike). The socket MUST be bound in a fresh `0700` directory under
  `/tmp` or `$XDG_RUNTIME_DIR`, never a deep repo-relative tmp path.
  - **Chrest-side risk:** this session's own `$CLAUDE_CODE_TMPDIR` lives
    under a spinclass worktree path (`.../.worktrees/<name>/.tmp/...`),
    which is exactly the deep-path shape the spike flagged as unsafe — the
    `capture-serve` implementation must NOT default its socket directory to
    a worktree-relative tmp; it needs its own short-path allocation
    independent of `$TMPDIR`.
- **Lifecycle:** stdin EOF, a `shutdown` notification, or `SIGTERM` → exit
  and unlink the socket.
- **Writer reuse:** `capture_plugin.WriteReceipt` is unchanged from Phase
  1 — only the `Writer` implementation differs (`WriteBlob` = `blob.begin`
  → copy to the passed fd → close → `blob.finish`, instead of one
  `writer.cmd` subprocess per blob). Byte-identical receipts vs. the v1
  path is the conformance bar that flips RFC 0008 from `proposed` to
  `accepted`.
- cutting-garden is building `pkgs/capture_serve` (wire types, SEQPACKET
  peer, handshake, plugin-side `Serve`) for chrest to link against; pin
  cutting-garden v0.1.24 in the meantime, and re-pin once `Serve` lands.

## More Information

- [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) — canonical abstract protocol (orchestrator / capture plugin / writer; merkle tree of typed hyphence blobs). Link points at the archived GitHub mirror (frozen at v0.1.24, still readable); canonical development has moved to the self-hosted forge (see Implementation status below).
- [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) — canonical web-kind binding chrest implements. Same archived-mirror caveat as above.
- cutting-garden RFC 0008 — `capture-serve` JSON-RPC transport (`proposed`, v1→v2 schema bump pending) — not yet mirrored to a stable GitHub link; see the forge repo (Implementation status below) for the current doc.
- [nebulous RFC 0001 — Web Capture Archive Protocol](https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md) — origin RFC; superseded by cutting-garden RFC 0002+0003 paired. Retained as historical reference.
- `github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin` — the shared Go package chrest imports for `WriteReceipt`/`BuildNode`/`JCS`/`Writer` (see `go/internal/echo/capturebatch/receipt.go`).
- Related chrest issues: chrest#10 (original html-to-pdf migration, closed), chrest#11 (multi-format aggregator, closed, superseded), chrest#26 (html-monolith, closed), chrest#29 (markdown variants, closed), chrest#33 (BiDi buffer drops), chrest#34 (capture exit-code, closed), chrest#47 (Chrome CDP removal, closed), chrest#83 (RFC 0002+0003 receipt-emitter migration, landed), chrest#98 (igloo `/v2` sentinel bug blocking the cutting-garden flake-input bridge).

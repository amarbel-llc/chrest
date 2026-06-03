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

This FDR documents chrest's capture-pipeline surface — the interactive `chrest capture --format` and orchestrator-driven `chrest capture-batch` commands. See [Implementation status](#implementation-status-migration-to-rfc-00020003) for the gap between current emitted bytes (legacy `web-capture-archive/v1`, inherited from nebulous RFC 0001) and the RFC 0002+0003 conforming shape.

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

JSON-stdin / JSON-stdout batch capture. This subcommand fills the **capture plugin** role of [cutting-garden RFC 0002 §Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md#capture-plugin-protocol) under the **web-archive binding** ([RFC 0003](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md)), emitting the RFC 0002+0003 merkle shape (schema `capture-plugin/v1`).

The shape chrest implements:

- Reads a single JSON document on stdin with shape `{schema, writer, target, defaults, captures[]}` (schema `capture-plugin/v1`).
- Runs every capture sequentially.
- For each capture, assembles an RFC 0002 receipt merkle tree of typed hyphence node blobs — `invocation`, `host`, `binary`, plugin-environment, `environment`, plugin-outcome, `outcome`, `payload`, `identity`, `receipt` — streaming every node to the orchestrator-supplied `writer.cmd` subprocess in post-order. A per-batch `capabilities` blob is written once and referenced from `environment.binary.capabilities_id`.
- Emits a single JSON result envelope on stdout: `{schema, plugin{name,version}, errors[], captures[{name, receipt{id,size}|error{kind,message}}]}` — one `receipt` ref per capture; all other markl-ids are recoverable by tree-walking the receipt.

The protocol-defined nodes, hyphence framing, JCS canonicalization, and type-signature registry come from cutting-garden's exported `pkgs/capture_plugin`, imported directly so chrest receipts are byte-identical to an in-process binding's. The receipt's type line is `! cutting_garden-capture-receipt-web-v1`; `environment.binary.name` is `chrest`. Web-binding node types: `jcs-chrest-capture-environment-v1`, `jcs-chrest-capture-outcome-v1` (`http.*`; `-v1-preview` when the backend can't observe the response), `jcs-chrest-capture-capabilities-v1`, and `chrest-capture-payload-<segment>-v1` (format segment, `-`→`_`).

Per-capture options echo into `identity → invocation.options` via JCS canonicalization so downstream consumers can reproduce the exact extraction parameters. The payload is written as a raw leaf (the type travels on the receipt's `payload` reference) so binary formats round-trip byte-exactly on restore.

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

Batch capture (RFC 0002+0003 `capture-plugin/v1` shape):

    $ echo '{
        "schema":   "capture-plugin/v1",
        "writer":   {"cmd": ["cutting-garden", "__write-blob", "--store", "archive"]},
        "target":   "https://en.wikipedia.org/wiki/Ferris_wheel",
        "defaults": {"normalize": true, "plugin": {"browser": "firefox"}},
        "captures": [
          {"name": "pdf",     "format": "pdf"},
          {"name": "md",      "format": "markdown-reader", "normalize": false},
          {"name": "archive", "format": "html-monolith",   "normalize": false}
        ]
      }' | chrest capture-batch | jq

Every capture produces one `receipt` ref keyed on its blake2b-256 content hash; the full identity/environment/outcome/payload subtree is recoverable by walking the receipt through the writer's store — so the archive is content-addressed and re-derivable. In practice the orchestrator is cutting-garden's web plugin (`cg capture web:https://…`), which supplies the `writer.cmd` and records the receipt.

## Limitations

- **`mhtml` and `a11y` are not yet functional.** Both return an unsupported error; they were Chrome-only and Chrome was removed in chrest#47. Implementing them over Firefox/BiDi is a future follow-up.
- **`markdown-selector` takes the first match only.** No `--selector-mode=all` or similar. Selector misses are a typed error that names the selector.
- **`--reader-engine=browser` is reserved but not implemented.** The Firefox `about:reader` engine is accepted as a valid flag value so the spec surface stays stable but rejects with `not-yet-implemented` at runtime.
- **`html-monolith` requires the `monolith` binary on `PATH`.** The nix-built `chrest` wraps it in via `flake.nix` `postFixup`; a `go install`-ed chrest relies on the user's PATH.
- **`capture-batch` normalizes only `text`, `pdf`, `screenshot`, `mhtml`.** RFC 0003 defers normalization rules for `html-monolith`, `a11y`, and `markdown-*`; `normalize=true` on those returns a per-capture `not-implemented` error. Pass `normalize=false` to capture them as-is.
- **BiDi network-event buffer drops events on heavy pages** (chrest#33). Affects `http.*` outcome fidelity for media-heavy pages; the outcome falls back to the `-v1-preview` plugin type when the response can't be observed.
- **Capture format selection from cutting-garden is coarse.** The `cg capture web:…` path captures one format (default `pdf`, overridable via `CUTTING_GARDEN_WEB_FORMAT`); a per-source options surface is a cutting-garden follow-up.

## Implementation status: migration to RFC 0002+0003

The hard cut to the RFC 0002+0003 merkle shape has landed. chrest imports
cutting-garden's exported `pkgs/capture_plugin` (which transitively pulls
`github.com/amarbel-llc/madder/go/pkgs/hyphence` + `markl`) and assembles the
receipt tree via `capture_plugin.WriteReceipt`. What changed:

- **Merkle-tree decomposition.** Each capture now produces the post-order node set — `invocation`, `host`, `binary`, plugin-environment, `environment`, plugin-outcome, `outcome`, `payload`, `identity`, `receipt` — plus a per-batch `capabilities` blob written once and reused (writer-side dedup).
- **Batch output reduction.** Schema is `capture-plugin/v1`; the per-capture entry is one `receipt` ref (`{id, size}`) or one `error` (`{kind, message}`). `plugin.{name,version}` replaces the old `capturer`.
- **Type-tag mapping.** `captures[].format` stays hyphenated; the payload type-string segment uses underscores (`chrest-capture-payload-markdown_reader-v1`) — see `payloadType` in `web_nodes.go`.
- **`timing_ms` object wrap.** Emitted as `{load: <int>}` (the only sub-key BiDi exposes), not a bare integer.
- **`http.headers` shape.** Array-of-`{name, value}` with lowercased names + preserved order/duplicates (unchanged from before).
- **Plugin-outcome node.** `http.*` moved from the old `envelope` into a plugin-outcome node referenced from `outcome.plugin` — `jcs-chrest-capture-outcome-v1`, or `-v1-preview` when the response wasn't observed. This required adding an optional `OutcomePlugin` to cutting-garden's `WriteReceipt` (backward-compatible; the git binding emits none).
- **Capabilities artifact.** `jcs-chrest-capture-capabilities-v1` referenced from `environment.binary.capabilities_id`; body lists `formats`, `browsers`, `normalizes`, `honors_dns`, `honors_extensions`, `transport`.

Deferred follow-ups: extension `fetched` mode and `preinstalled` extension
reporting (the environment node omits `extensions`/`dns` until gathered);
per-format normalization rules for `html-monolith` / `a11y` / `markdown-*`;
and `nix`-side `gomod2nix.toml` regeneration must be run in the devshell
(`just build-gomod2nix`) before `nix build` after the new dependency.

## More Information

- [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) — canonical abstract protocol (orchestrator / capture plugin / writer; merkle tree of typed hyphence blobs).
- [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) — canonical web-kind binding chrest implements.
- [nebulous RFC 0001 — Web Capture Archive Protocol](https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md) — origin RFC; superseded by cutting-garden RFC 0002+0003 paired. Retained as historical reference.
- Related chrest issues: chrest#10 (original html-to-pdf migration, closed), chrest#11 (multi-format aggregator, closed, superseded), chrest#26 (html-monolith, closed), chrest#29 (markdown variants, closed), chrest#33 (BiDi buffer drops), chrest#34 (capture exit-code, closed), chrest#47 (Chrome CDP removal, closed).

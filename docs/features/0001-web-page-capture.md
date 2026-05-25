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

| Format | Payload | Media type |
|---|---|---|
| `pdf` | PDF document from the browser's print pipeline | `application/pdf` |
| `screenshot-png` | Full-page or viewport PNG | `image/png` |
| `screenshot-jpeg` | Full-page or viewport JPEG (tunable `--quality`) | `image/jpeg` |
| `mhtml` | Firefox MHTML snapshot (not yet functional — returns unsupported error) | `multipart/related` |
| `a11y` | Accessibility tree JSON (not yet functional — returns unsupported error) | `application/json` |
| `text` | `document.body.innerText` | `text/plain; charset=utf-8` |
| `html-monolith` | Rendered DOM inlined by `monolith` — every asset as a `data:` URI | `text/html; charset=utf-8` |
| `markdown-full` | Rendered DOM converted to markdown | `text/markdown; charset=utf-8` |
| `markdown-reader` | Readability-extracted article converted to markdown | `text/markdown; charset=utf-8` |
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

JSON-stdin / JSON-stdout batch capture. This subcommand fills the **capture plugin** role of [cutting-garden RFC 0002 §Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md#capture-plugin-protocol) under the **web-archive binding** ([RFC 0003](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md)). At the wire level today it still emits the legacy `web-capture-archive/v1` byte shape inherited from nebulous RFC 0001 — see [Implementation status](#implementation-status-migration-to-rfc-00020003) for the gap.

The shape chrest implements today (legacy):

- Reads a single JSON document on stdin with shape `{schema, writer, url, defaults, captures[]}`.
- Runs every capture sequentially.
- Streams each artifact to the orchestrator-supplied `writer.cmd` subprocess for content-addressed storage.
- Emits a single JSON result envelope on stdout with per-capture `payload` / `spec` / `envelope` ArtifactRefs.

Legacy schema tokens: input/output `web-capture-archive/v1`; spec artifacts `web-capture-archive.spec/v1`; envelope artifacts `web-capture-archive.envelope/v1` (when HTTP fields are populated by a network-event-capable backend) or `v1-preview` (when they can't be).

Per-capture options echo into the spec artifact's `capture.options` via JCS canonicalization so downstream consumers can reproduce the exact extraction parameters.

Target shape (post-migration to RFC 0002+0003) is a merkle tree of typed hyphence blobs rooted at a per-run **receipt** (`!cutting_garden-capture-receipt-web-v1`) referencing an **identity** subtree (invocation / environment {host, binary, plugin}) and an **outcome** subtree (datetime, stripped normalization residue, plugin-namespaced HTTP response). The batch output collapses from three artifact refs per capture (`spec`, `envelope`, `payload`) to a single `receipt` ref per capture; all other artifacts are recoverable by tree traversal through the writer's underlying store. See [RFC 0002 §Migration from web-capture-archive/v0+v1](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) and [RFC 0003 §Compatibility](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md#compatibility) for the field-by-field relocation table.

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

Batch capture (current legacy `web-capture-archive/v1` shape; will hard-cut to RFC 0002+0003):

    $ echo '{
        "schema":   "web-capture-archive/v1",
        "writer":   {"cmd": ["madder", "--format=json", "write", "--store", "archive"]},
        "url":      "https://en.wikipedia.org/wiki/Ferris_wheel",
        "defaults": {"browser": "firefox", "split": false},
        "captures": [
          {"name": "pdf",     "format": "pdf"},
          {"name": "md",      "format": "markdown-reader"},
          {"name": "archive", "format": "html-monolith"}
        ]
      }' | chrest capture-batch | jq

Every capture produces a `payload` ArtifactRef keyed on its blake2b-256 content hash plus a `spec` ArtifactRef echoing the resolved options — so the archive is content-addressed and re-derivable.

## Limitations

- **`mhtml` and `a11y` are not yet functional.** Both return an unsupported error; they were Chrome-only and Chrome was removed in chrest#47. Implementing them over Firefox/BiDi is a future follow-up.
- **`markdown-selector` takes the first match only.** No `--selector-mode=all` or similar. Selector misses are a typed error that names the selector.
- **`--reader-engine=browser` is reserved but not implemented.** The Firefox `about:reader` engine is accepted as a valid flag value so the spec surface stays stable but rejects with `not-yet-implemented` at runtime.
- **`html-monolith` requires the `monolith` binary on `PATH`.** The nix-built `chrest` wraps it in via `flake.nix` `postFixup`; a `go install`-ed chrest relies on the user's PATH.
- **`capture-batch` only supports `split=false` for `html-monolith` and `markdown-*`** — no byte-stability normalizer has been wired for those formats. Existing formats (`text`, `pdf`, `screenshot`) do support `split=true`.
- **BiDi network-event buffer drops events on heavy pages** (chrest#33). Affects envelope fidelity for `split=true` captures of media-heavy pages; harmless for `split=false`.
- **No splitting of an `html-monolith` / `markdown-*` payload into a normalized form.** The payload is recorded verbatim; a future `split=true` path could strip asset bytes into the envelope and normalize the wrapper.
- **`capture-batch` emits legacy `web-capture-archive/v1` bytes, not the RFC 0002+0003 merkle shape.** The wire-format rewrite is a hard cut (no parallel emission window); existing nebulous archive records pointing at old-shape blobs remain readable as immutable historical bytes, but cross-version dedup against post-migration archives is gone by design. See [Implementation status](#implementation-status-migration-to-rfc-00020003).

## Implementation status: migration to RFC 0002+0003

cutting-garden RFC 0002 (Capture Plugin Protocol) and RFC 0003 (Web-Archive Binding) are merged on cutting-garden master. The chrest-side emitter rewrite is deferred to a follow-up. Concrete deltas the rewrite must absorb:

- **Hyphence emitter dependency.** Each blob the plugin writes is a hyphence document (dodder RFC 0001 framing) with typed blob refs (dodder FDR-0001). Chrest currently emits raw JCS-canonical JSON for `spec` and `envelope`. The rewrite adopts madder's hyphence package (`github.com/amarbel-llc/madder/go/pkgs/hyphence`) as a new Go dependency.
- **Merkle-tree decomposition.** A single capture currently produces 2 or 3 artifacts (`payload` always, `envelope` when split, `spec` always). The new shape produces ~10 blobs per capture in post-order — `invocation`, `host`, `binary`, plugin-environment, `environment`, plugin-outcome, `outcome`, `payload`, `identity`, `receipt` — with writer-side dedup for `host` / `binary` / plugin-environment across captures in the same batch.
- **Batch output reduction.** The output JSON's per-capture entry collapses from three artifact refs (`spec` / `envelope` / `payload`) to one `receipt` ref. All other markl-ids are recoverable by tree-walking the receipt blob.
- **Type-tag mapping convention.** The batch-input `captures[].format` keeps the hyphenated form (`markdown-reader`, `html-monolith`); the type-string segment uses underscores (`markdown_reader`, `html_monolith`) per RFC 0002's segment-internal-words rule. The emitter MUST map between the two surfaces and MUST NOT mix them.
- **`timing_ms` object wrap.** Current envelope emits `timing_ms` as a bare integer. RFC 0003 declares it an object `{dns, tcp, tls, ttfb, load}` with all sub-keys optional; chrest's BiDi-restricted backend will emit `{load: <int>}` only.
- **`http.headers` shape.** RFC 0003 confirms array-of-`{name, value}` objects with lowercased names + preserved order + duplicates as separate entries — matches chrest's current emission. No change.
- **Extension fetched mode.** Chrest currently only reports pre-installed extensions (mode `preinstalled` with `{source, id, version, manifest_digest?}`). RFC 0003 also defines a `fetched` mode with plugin-driven URL-fetch into the blob store (`{source, name, version, url, digest}`). The fetched mode is a new feature, not a wire-shape change; the `source` discriminator is identity-affecting.
- **Capabilities artifact.** Chrest does not currently emit a capabilities blob; the spec carries no `capabilities_id`. RFC 0003 specifies `!jcs-chrest-capture-capabilities-v1` referenced from `environment.binary.capabilities_id`. The body lists `formats`, `browsers`, `normalizes`, `honors_dns`, `honors_preinstalled_extensions` / `honors_fetched_extensions`, `transport`.
- **Drop legacy emitters.** `web-capture-archive/v0+v1` schema constants in `go/src/delta/capturebatch/{types.go, spec.go, envelope.go}` are removed once the new path lands; the `-v1-preview` outcome-plugin tag in `envelope.go` migrates to `!jcs-chrest-capture-outcome-v1-preview` per RFC 0003 §Preview Schema for Backends Without `http.*`.

## More Information

- [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) — canonical abstract protocol (orchestrator / capture plugin / writer; merkle tree of typed hyphence blobs).
- [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) — canonical web-kind binding chrest implements.
- [nebulous RFC 0001 — Web Capture Archive Protocol](https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md) — origin RFC; superseded by cutting-garden RFC 0002+0003 paired. Retained as historical reference.
- Related chrest issues: chrest#10 (original html-to-pdf migration, closed), chrest#11 (multi-format aggregator, closed, superseded), chrest#26 (html-monolith, closed), chrest#29 (markdown variants, closed), chrest#33 (BiDi buffer drops), chrest#34 (capture exit-code, closed), chrest#47 (Chrome CDP removal, closed).

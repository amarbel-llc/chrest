---
status: superseded
date: 2026-05-22
superseded-by:
  - https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md
  - https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md
  - docs/features/0001-web-page-capture.md
confidence: low — most of this is reconstructed from partial info; many sections are speculative
---

# Cutting-garden ↔ chrest integration — exploratory FDR

> **SUPERSEDED 2026-05-25.** The cutting-garden side is no longer a
> reconstruction-from-inference; the canonical specification now lives
> upstream as two RFCs on `amarbel-llc/cutting-garden` master:
>
> - [cutting-garden RFC 0002 — Capture Plugin Protocol](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md) (abstract, non-web-specific).
> - [cutting-garden RFC 0003 — Web-Archive Binding](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md) (chrest's binding).
>
> The chrest-side FDR has been retitled and re-anchored:
> [docs/features/0001-web-page-capture.md](../features/0001-web-page-capture.md)
> ("Web-archive capture (cutting-garden RFC 0003 binding)") is now the
> canonical chrest-side entry point and enumerates the deferred
> emitter-rewrite work.
>
> This file is preserved as a historical record of the strawman
> reconstruction process. Inferred sections tagged `UNKNOWN` /
> `ASSUMPTION` below are NOT a reliable description of the actual
> protocol — consult the cutting-garden RFCs instead.

> **Heavy disclaimer (original).** This was drafted without source-level access to
> `amarbel-llc/cutting-garden` or `amarbel-llc/nebulous` (both private,
> denied to this session's GitHub MCP scope and local git proxy). The
> only authoritative inputs were: (a) chrest's own source tree, (b)
> `docs/features/0001-web-page-capture.md`, and (c) the GitHub
> landing-page summaries of cutting-garden and nebulous. Anything
> beyond the chrest side is **inferred** and tagged **UNKNOWN /
> ASSUMPTION** below. Treat this as a strawman for the author to
> correct, not a design contract.

## Context

The driving ask was a "cutting-garden plugin implementation for chrest
… like nebulous capture, but generic." Per clarifications during
planning:

- **Code lives in `amarbel-llc/cutting-garden`**, not chrest.
- **Chrest stays in its current role**: the _capturer_ per RFC 0001
  (Web Capture Archive Protocol).
- "Plugin" in this context means "integration" — cutting-garden becomes
  a new RFC 0001 _orchestrator_, mirroring what nebulous does today but
  generic instead of NewsBlur-specific.

Why this exists (best reconstruction): nebulous proved the
chrest+madder pipeline works for archiving feed URLs. Cutting-garden
generalizes that win — anyone with a list of URLs (or a tree of them,
matching cutting-garden's "filesystem-tree capture/restore" framing)
should be able to drive chrest through the same protocol without
inheriting nebulous's NewsBlur coupling.

## What I know with reasonable confidence

These bits are grounded in chrest's source and in
`docs/features/0001-web-page-capture.md`:

### RFC 0001 roles (chrest side, established)

```
┌──────────────┐  JSON stdin   ┌────────────┐  artifact bytes  ┌────────┐
│ ORCHESTRATOR │ ────────────► │  CAPTURER  │ ───── stdin ───► │ WRITER │
│  (cutting-   │               │  (chrest   │                  │ (madder│
│  garden)     │ ◄──── JSON ── │  capture-  │ ◄── {id,size} ── │  TBD)  │
└──────────────┘   stdout      │  batch)    │     stdout       └────────┘
                               └────────────┘
```

- **Capturer contract** is implemented at
  `go/src/delta/capturebatch/runner.go:44-70`, with schema/envelope
  shapes in `types.go`, `envelope.go`, `spec.go`.
- Schema tokens: input/output `web-capture-archive/v1`; envelope
  `web-capture-archive.envelope/v1`; spec
  `web-capture-archive.spec/v1`; capturer name `chrest`.
- Writer-subprocess contract: `go/src/delta/capturebatch/writer.go:19-75`.
  Writer reads artifact bytes on stdin, writes
  `{"id": "<content-addr>", "size": N}` on stdout, exits 0 on success.
- Example invocation from the feature doc
  (`docs/features/0001-web-page-capture.md:86-96`):

  ```json
  {
    "schema": "web-capture-archive/v1",
    "writer": {
      "cmd": ["madder", "--format=json", "write", "--store", "archive"]
    },
    "url": "https://example.com",
    "defaults": { "browser": "firefox", "split": false },
    "captures": [
      { "name": "pdf", "format": "pdf" },
      { "name": "md", "format": "markdown-reader" }
    ]
  }
  ```

### What that means for cutting-garden

- Cutting-garden needs to **emit valid `web-capture-archive/v1` JSON**
  on chrest's stdin and **consume the JSON envelope** on chrest's stdout.
- Cutting-garden picks the **writer** in the JSON (madder is the
  obvious default; cutting-garden could ship its own writer that wraps
  madder with additional metadata).
- Cutting-garden does _not_ need to render pages, run a browser, or
  touch BiDi — chrest owns all of that.

## What is UNKNOWN / ASSUMPTION

### Cutting-garden's command surface — UNKNOWN

The landing page says "Filesystem-tree capture/restore CLI built on
top of madder's blob store" and "Phase 1 — framework bootstrap, no
commands implemented yet." The "filesystem-tree" framing is _not_
obviously a web-capture framing. **Assumption**: cutting-garden will
grow a `cutting-garden capture <urls...>` (or similar) subcommand that
maps onto chrest's batch protocol, plus a complementary restore path.
**Unknown**: whether URL capture is a first-class command, a flag on a
generic capture command, or implemented via a sub-tool.

### Whether cutting-garden uses chrest CLI or chrest MCP — UNKNOWN

Two viable wiring options exist:

1. **Subprocess** — cutting-garden execs `chrest capture-batch` and
   pipes JSON. Matches the documented contract exactly. No chrest
   changes needed.
2. **MCP client** — cutting-garden connects to `chrest mcp` over stdio
   and calls a hypothetical `capture-batch` MCP tool. **Caveat**:
   chrest does not currently expose `capture-batch` as an MCP tool
   (only `tools/capture.go` registers single-capture helpers; no batch
   tool was found in `go/src/delta/tools/`). Going this route would
   require a new chrest MCP tool — which contradicts the
   "chrest = capturer only, no chrest changes" scoping.

**Recommended assumption**: subprocess. If cutting-garden ever wants
MCP, that's a follow-up FDR in chrest, not part of this work.

### Input source — UNKNOWN

A "generic" orchestrator could accept URLs from:

- stdin (one per line) — most CLI-friendly
- a file (JSON list, CSV, or newline-delimited)
- a directory walk (matches the "filesystem-tree" framing — maybe
  cutting-garden discovers URLs from filenames or sidecar metadata?)
- another tool's output via pipe

**No way to know without seeing the repo or asking.**

### Tree semantics — UNKNOWN

"Filesystem-tree capture/restore" strongly implies cutting-garden
treats its content as a _tree_ (paths, hierarchy) rather than a flat
list of blobs. **Assumption**: cutting-garden maps each captured URL
to a node in a tree (perhaps `domain/path/segment/...`), with
per-capture artifacts (`payload`, `spec`, `envelope`) becoming leaves
or sidecar files. This is consistent with madder being a blob store —
cutting-garden may layer a tree index over madder's flat
content-addressed storage. **Speculation, not confirmed.**

### Writer subprocess identity — UNKNOWN

The chrest example uses `madder` directly. Cutting-garden could:

- Pass `madder` straight through as the writer (chrest stays unaware
  of cutting-garden).
- Use itself as the writer (`cutting-garden write …` subcommand) and
  internally call madder. Lets cutting-garden record tree-index entries
  alongside the blob writes.

The second option is cleaner — orchestrator and writer roles both live
in cutting-garden — but it adds a binary self-call. **Unknown which it
is doing.**

### Relationship to nebulous — UNKNOWN

Nebulous's landing page references "chrest + madder" but the public
summary did not expose its capture command. **Assumption**: nebulous
has an internal `capture` package that builds RFC 0001 batches from
NewsBlur stories. Cutting-garden's "generic" version likely
factors out the orchestrator boilerplate (batch-builder, writer
exec, retry/concurrency, error handling) so both tools can share it
via a Go library. **Unknown whether that shared library lives in
cutting-garden, in a third repo, or is duplicated.**

## Strawman architecture (low-confidence)

If the assumptions above hold, the chrest-side picture is trivial — no
chrest code changes — and the cutting-garden side looks roughly like:

```
cutting-garden/
├── cmd/cutting-garden/main.go         # cobra root; `capture`, `restore`, etc.
├── internal/capture/                  # NEW: RFC 0001 orchestrator role
│   ├── batch.go                       # build web-capture-archive/v1 input JSON
│   ├── chrest.go                      # exec `chrest capture-batch`, stream JSON
│   ├── result.go                      # parse output envelope, surface errors
│   └── tree.go                        # map URLs → tree nodes
├── internal/writer/                   # OPTIONAL: cutting-garden-as-writer shim
│   └── writer.go                      # implement RFC 0001 writer subprocess contract
├── internal/madder/                   # existing? talks to madder blob store
└── docs/
    └── plans/2026-05-xx-chrest-integration.md  # mirror of this FDR
```

The chrest side gets, at most:

- A doc cross-link in `docs/features/0001-web-page-capture.md`
  pointing at cutting-garden once the integration exists.
- Maybe one BATS test in `zz-tests_bats/` that exercises chrest with a
  shell-script orchestrator to lock in the contract (not
  cutting-garden-specific, just protocol fidelity).

## Suggested next steps

In priority order — pick whichever you actually want me to do next, none
of these are committed by this FDR:

1. **Grant access** to `amarbel-llc/cutting-garden` (add it to MCP
   scope or the local git proxy allowlist), then redo this FDR with
   real code in hand. Highest-value step — every "UNKNOWN" above goes
   away.
2. **Pin the integration contract** by writing a tiny shell-script
   orchestrator into `zz-tests_bats/capture_batch.bats` that exercises
   the documented JSON shape against chrest. Gives cutting-garden a
   live spec to copy from.
3. **Write the cutting-garden capture package** once #1 unblocks. Out
   of scope for this repo.

## Verification (once anything is implemented)

End-to-end smoke (would run from cutting-garden's repo, not chrest's):

```bash
# 1. produce a URL list
echo "https://example.com" > urls.txt

# 2. drive chrest through cutting-garden
cutting-garden capture --writer madder --output ./archive urls.txt

# 3. inspect the resulting tree
cutting-garden ls ./archive
cutting-garden restore ./archive/example.com > restored.html
```

Chrest-side check (already works today, unchanged):

```bash
just test-mcp-bats   # batch suite in zz-tests_bats/capture_batch.bats
```

## Out of scope for this FDR

- Implementing anything in chrest. The chrest role is scoped to
  "capturer only".
- Designing madder's tree-index layer. That belongs to madder /
  cutting-garden, not chrest.
- Backfilling nebulous onto whatever shared library cutting-garden
  produces.

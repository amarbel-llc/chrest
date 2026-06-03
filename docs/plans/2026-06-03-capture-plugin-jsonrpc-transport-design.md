# Capture plugin JSON-RPC + FD-passing transport — implementation design

- Date: 2026-06-03
- Status: design (no code yet)
- Spec: cutting-garden **RFC 0005 — Capture Plugin Transport: JSON-RPC +
  FD-passed blob sockets** (`docs/rfcs/0005-capture-plugin-jsonrpc-transport.md`)
- Supersedes the transport half of the current `capture-batch`
  implementation (see `docs/features/0001-web-page-capture.md`); the
  receipt-tree assembly (`capture_plugin.WriteReceipt`, `web_nodes.go`,
  `types_register.go`) is **unchanged**.

## Goal

Replace the RFC 0002 v1 transport — one-shot `chrest capture-batch`
(JSON on stdin/stdout) writing each blob by spawning
`cutting-garden __write-blob` — with the RFC 0005 v2 transport: a
persistent peer-to-peer JSON-RPC session over an inherited `AF_UNIX`
`SOCK_SEQPACKET` socket, with blob bytes flowing out of band through a
per-blob pipe whose write end the orchestrator passes via `SCM_RIGHTS`.

The bytes chrest stores and every markl id stay identical — this is a
transport swap, validated by the existing `receipt_tree_test.go`
(byte-for-byte tree assertions) continuing to pass against the new writer.

## chrest side

### New: `chrest capture-serve`

A subcommand that adopts the control fd from `CAPTURE_PLUGIN_CONTROL_FD`
and runs the JSON-RPC peer until `shutdown` or control-socket EOF.

- Resolve the fd: `os.NewFile(fd, "control")` → `net.FileConn` →
  `*net.UnixConn` (unixpacket). Error out if the env var is absent.
- Serve `initialize` (negotiate `capture-plugin/v2`, advertise
  `plugin{name,version}`, `formats`, `blob_concurrency: 1`).
- Serve `capture.batch`: same `capturebatch.Run` logic as today, but the
  `Writer` it threads is the new `rpcBlobWriter` (below) instead of
  `subprocessWriter`. `opts.Target` / `Defaults` come from the JSON-RPC
  params; there is no `writer.cmd`.
- Handle control-socket EOF/`EPIPE` as cancellation (cancel the batch
  ctx, exit non-zero).

The existing `cmd/chrest/capture_batch.go` (v1 stdin/stdout) MAY remain as
a deprecated compatibility path during rollout, then be removed.

### New: `internal/.../capturebatch` JSON-RPC peer + `rpcBlobWriter`

A small JSON-RPC 2.0 peer over `*net.UnixConn`:

- `WriteMsgUnix`/`ReadMsgUnix`, one message per datagram.
- Outgoing requests (`blob.begin`, `blob.finish`) correlated by `id`; a
  pending-call map keyed by id; a single read loop dispatches responses to
  waiters and incoming requests (`initialize`, `capture.batch`, `shutdown`)
  to handlers. Must be safe for the read loop to deliver a `blob.*`
  response while a `capture.batch` handler is mid-flight (the handler is
  the caller of `blob.*`).

`rpcBlobWriter` implements `capture_plugin.Writer`:

```
WriteBlob(ctx, r io.Reader) (id string, size int64, err error):
  resp := call("blob.begin", {})            // response carries a passed write-fd
  w := os.NewFile(fdFromAncillary(resp), "blob")
  io.Copy(w, r); w.Close()                  // EOF terminates the blob
  fin := call("blob.finish", {blob: resp.blob})
  return fin.id, fin.size, nil
```

Because `WriteReceipt` already drives all node writes through
`capture_plugin.Writer`, **no change to `runner.go`'s assembly** is needed
— only the writer implementation passed in. `subprocessWriter` (and its
`WriteThrough` fork/exec) is deleted.

The fiddly bit is reading the FD off the ancillary data of the precise
datagram that is the `blob.begin` response: `ReadMsgUnix` with an `oob`
buffer sized `syscall.CmsgSpace(4)`, then
`ParseSocketControlMessage` + `ParseUnixRights`. The read loop must always
pass an `oob` buffer so it never silently drops a passed fd.

### Retired on the chrest side

- `subprocessWriter` + `WriteThrough` (writer.go) — replaced by
  `rpcBlobWriter`.
- The stdin/stdout `capture-batch` command path, once `capture-serve` is
  the default (kept briefly for compat).
- `chrest-jcs` and the normalizers (`mhtml/pdf/png/normalize`,
  `web_nodes.go`, `types_register.go`, `jcs.go`) are **unaffected**.

## cutting-garden side (counterpart, for reference)

In `internal/cutting_garden_plugin_web` + a new transport helper:

- `CaptureProtocol` stops building a `writer.cmd` argv. Instead it:
  1. `socketpair(AF_UNIX, SOCK_SEQPACKET)`, `exec`s `chrest capture-serve`
     with the plugin end as `ExtraFiles[0]` (fd 3) and
     `CAPTURE_PLUGIN_CONTROL_FD=3`;
  2. runs a JSON-RPC peer on its end: sends `initialize` then
     `capture.batch`; **serves** `blob.begin`/`blob.finish` by creating a
     pipe, passing the write end via `WriteMsgUnix`+`UnixRights`, closing
     its copy, and streaming the read end through
     `plugin_blob_io.WriteReaderBlob(ctx, req.BlobStore, r)` to get the
     `{id, size}`;
  3. returns the receipt id from the `capture.batch` result.
- `internal/blob_writer` (the `__write-blob` subcommand) is **retired** —
  the orchestrator writes blobs in-process from the passed pipe, so there
  is no longer a child writer process and no store re-resolution.
- `StoreName` plumbing through `ProtocolCaptureRequest` is no longer needed
  for the writer (the orchestrator already holds `req.BlobStore`); it can
  be dropped or kept for diagnostics.

The web plugin's `RestoreProtocol`/`DiffProtocol` are unaffected (they
read the store directly; diff's re-capture goes through the same new
`CaptureProtocol`).

## Validation plan

- `receipt_tree_test.go` is repointed at `rpcBlobWriter` driven by an
  in-test JSON-RPC orchestrator that content-addresses the passed pipes —
  proving the tree bytes are unchanged from the fork/exec writer.
- A focused transport test: socketpair + a fake plugin that echoes one
  blob, asserting `SCM_RIGHTS` round-trips a writable fd and the
  `{id,size}` comes back over JSON-RPC.
- `capture_batch.bats` becomes `capture_serve.bats`: launch
  `chrest capture-serve` over a socketpair from a tiny harness, run a real
  Firefox capture, assert the receipt resolves (same tree assertions as
  today).

## Risks / notes

- **SEQPACKET in Go.** `net.FileConn` over a `SOCK_SEQPACKET` socketpair
  yields a `unixpacket` `*net.UnixConn`; confirm `WriteMsgUnix`/
  `ReadMsgUnix` preserve message boundaries + ancillary on this platform
  before building on it (spike first).
- **FD leak discipline.** Every passed fd must be closed exactly once on
  each side (orchestrator closes its `w` after send; plugin closes the
  received fd after writing). A leak stalls EOF and deadlocks the blob.
- **nix.** No new Go dependency beyond what's already vendored
  (`golang.org/x/sys/unix` is already in the tree); still requires
  `just build-gomod2nix` if `go.mod` shifts.
- **Rollout.** Land RFC 0005 + the cutting-garden orchestrator and chrest
  `capture-serve` together; keep v1 `capture-batch` one release for
  fallback, then delete it and `__write-blob`.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"

	capture_serve "code.linenisgreat.com/cutting-garden/pkgs/capture_serve"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/command"

	"code.linenisgreat.com/chrest/go/internal/echo/capturebatch"
)

// registerCaptureServeCommand registers a dewey entry for `capture-serve`
// so it appears in the top-level `chrest --help` listing, mirroring
// registerCaptureBatchCommand — the actual dispatch is a bypass in main.go
// because RFC 0008's contract (a persistent JSON-RPC session over a
// self-created rendezvous socket) doesn't fit dewey's Result path any more
// than capture-batch's JSON-stdin/stdout contract does.
func registerCaptureServeCommand(app *command.Utility) {
	app.AddCommand(&command.Command{
		Name: "capture-serve",
		Description: command.Description{
			Short: "Serve one RFC 0008 capture-serve session (cutting-garden capture-plugin/v2). Launched by an orchestrator, not invoked directly.",
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			return fmt.Errorf(
				"capture-serve should be invoked directly (chrest capture-serve); " +
					"the Result path does not support the RFC 0008 rendezvous-socket contract",
			)
		},
	})
}

// cmdCaptureServe implements the `chrest capture-serve` subcommand — the
// RFC 0008 (capture-plugin/v2) transport for the same capture-plugin role
// capture-batch fills under RFC 0002's subprocess (v1) transport. It is
// launched by an orchestrator, never invoked directly by a human:
//
//  1. CookieFromEnv — a missing CAPTURE_PLUGIN_COOKIE means this process
//     was not launched by an orchestrator; exit non-zero without touching
//     stdout (RFC 0008 §Handshake).
//  2. ListenRendezvous — bind a fresh unixpacket rendezvous socket at a
//     path short enough for sun_path.
//  3. Print exactly one AnnounceLine to stdout; stdout carries nothing
//     else under this protocol.
//  4. AcceptUnix the orchestrator's dial, then Serve the session — the
//     receipt-assembly path is identical to capture-batch, wired through
//     capturebatch.NewBatchHandler.
//
// Lifecycle: Serve itself returns nil on a graceful shutdown (the
// orchestrator's shutdown notification, or a socket close following one).
// This wrapper additionally watches stdin for EOF and SIGTERM — neither of
// which Serve observes on its own — and cancels the session's context on
// either, per RFC 0008 §Lifecycle.
func cmdCaptureServe(ctx context.Context, version string, args []string) error {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			printCaptureServeHelp(os.Stdout)
			return nil
		}
	}

	cookie, err := capture_serve.CookieFromEnv()
	if err != nil {
		// Per RFC 0008 §Handshake: refuse to serve without the launch
		// cookie, and MUST NOT print anything to stdout while doing so.
		return err
	}

	ln, socketPath, cleanup, err := capture_serve.ListenRendezvous()
	if err != nil {
		return fmt.Errorf("bind rendezvous socket: %w", err)
	}
	defer cleanup()

	line, err := capture_serve.AnnounceLine(cookie, capture_serve.Handshake{
		Version: capture_serve.SchemaV2,
		Network: capture_serve.HandshakeNetwork,
		Address: socketPath,
	})
	if err != nil {
		return fmt.Errorf("render announce line: %w", err)
	}

	// Same SIGPIPE rationale as cmdCapture/cmdCaptureBatch — the
	// orchestrator closing its read end of our stdout during error
	// handling MUST NOT kill us before session cleanup runs.
	signal.Ignore(syscall.SIGPIPE)

	sessionCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()

	// stdin EOF is a lifecycle trigger Serve does not itself observe (it
	// only watches the control socket) — RFC 0008 §Lifecycle requires it
	// alongside the shutdown notification and SIGTERM.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()

	// AcceptUnix has no context parameter and does not observe
	// sessionCtx on its own — if the orchestrator never dials (or stdin
	// closes / SIGTERM arrives before it does), a bare AcceptUnix would
	// block forever. Closing the listener on cancellation unblocks it;
	// cleanup (deferred above) closing it again afterward is a no-op.
	go func() {
		<-sessionCtx.Done()
		ln.Close()
	}()

	if _, err := os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write announce line: %w", err)
	}

	conn, err := ln.AcceptUnix()
	if err != nil {
		if sessionCtx.Err() != nil {
			// The listener was closed because the session ended before
			// the orchestrator ever dialed in — a clean exit, not a
			// bring-up failure.
			return nil
		}
		return fmt.Errorf("accept rendezvous connection: %w", err)
	}

	err = capture_serve.Serve(sessionCtx, conn, capture_serve.ServeConfig{
		Plugin: capture_serve.PluginInfo{
			Name:    capturebatch.CapturerName,
			Version: version,
		},
		Formats: capturePayloadFormatNames(),
		Batch:   capturebatch.NewBatchHandler(version),
	})
	if err != nil {
		return fmt.Errorf("capture-serve session: %w", err)
	}
	return nil
}

// capturePayloadFormatNames returns the sorted list of formats capture-batch
// and capture-serve both support, for the advisory Formats field in
// initialize's response (the authoritative capability surface remains the
// receipt tree's capabilities blob — RFC 0003 — not this field).
func capturePayloadFormatNames() []string {
	formats := make([]string, 0, len(capturebatch.PayloadMediaTypes))
	for f := range capturebatch.PayloadMediaTypes {
		formats = append(formats, f)
	}
	sort.Strings(formats)
	return formats
}

func printCaptureServeHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: chrest capture-serve")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Serve one RFC 0008 capture-serve session — the capture-plugin/v2 JSON-RPC")
	fmt.Fprintln(w, "transport for the same capture-plugin role capture-batch fills under RFC")
	fmt.Fprintln(w, "0002's subprocess (v1) transport. Launched by an orchestrator, which must")
	fmt.Fprintln(w, "set CAPTURE_PLUGIN_COOKIE in the environment; invoked without it, this")
	fmt.Fprintln(w, "command exits non-zero without printing to stdout.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "See also: chrest capture-batch  (the RFC 0002 subprocess transport)")
}

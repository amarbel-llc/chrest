package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"

	"code.linenisgreat.com/chrest/go/internal/delta/capturebatch"
)

// registerCaptureBatchCommand registers a dewey entry for `capture-batch`
// so it appears in the top-level `chrest --help` listing. The command is
// not dispatched through dewey at runtime — the bypass in main.go
// intercepts os.Args[1] == "capture-batch" before RunCLI is called,
// because the contract is JSON-on-stdin / JSON-on-stdout, which does
// not fit dewey's Result path. The RunCLI below is a guard that fires
// only if somebody constructs an invocation path that skips the
// bypass; it tells them what to do.
func registerCaptureBatchCommand(app *command.Utility) {
	app.AddCommand(&command.Command{
		Name: "capture-batch",
		Description: command.Description{
			Short: "Run a batch of web captures; reads JSON from stdin, writes JSON to stdout (RFC 0001).",
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			return fmt.Errorf(
				"capture-batch should be invoked directly (chrest capture-batch < input.json); " +
					"the Result path does not support the JSON-stdin / JSON-stdout contract",
			)
		},
	})
}

// cmdCaptureBatch implements the `chrest capture-batch` subcommand per
// RFC 0001 (Web Capture Archive Protocol). It reads a single JSON
// document from stdin, runs every capture sequentially, spawns a fresh
// writer subprocess per artifact to obtain its content-addressed ID,
// and writes a single JSON result object to stdout.
//
// Unlike `chrest capture`, this command is entirely machine-driven:
// its contract is JSON-on-stdin / JSON-on-stdout, not flags.
func cmdCaptureBatch(ctx context.Context, version string, args []string) error {
	// Honor --help / -h before anything else, so `chrest capture-batch
	// --help` doesn't block reading stdin.
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			printCaptureBatchHelp(os.Stdout)
			return nil
		}
	}

	// Same SIGPIPE rationale as cmdCapture — an orchestrator closing its
	// read end of our stdout during error handling MUST NOT kill us
	// before `defer`red writer cleanup runs.
	signal.Ignore(syscall.SIGPIPE)

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var input capturebatch.Input
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Errorf("parse batch input: %w", err)
	}
	if input.Schema != capturebatch.InputSchema {
		return fmt.Errorf("schema MUST be %q, got %q", capturebatch.InputSchema, input.Schema)
	}

	out, err := capturebatch.Run(ctx, input.Captures, capturebatch.Options{
		CapturerVersion: version,
		Writer:          input.Writer,
		URL:             input.URL,
		Defaults:        input.Defaults,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("write batch output: %w", err)
	}
	return nil
}

func printCaptureBatchHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: chrest capture-batch < input.json")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run a batch of web captures per the Web Capture Archive Protocol")
	fmt.Fprintln(w, "(RFC 0001). The single JSON document read from stdin has the shape:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  {")
	fmt.Fprintln(w, `    "schema":   "web-capture-archive/v1",`)
	fmt.Fprintln(w, `    "writer":   {"cmd": ["madder", "--format=json", "write", "--store", "NAME"]},`)
	fmt.Fprintln(w, `    "url":      "https://example.com",`)
	fmt.Fprintln(w, `    "defaults": {"browser": "firefox", "split": false},`)
	fmt.Fprintln(w, `    "captures": [{"name": "text", "format": "text"}, …]`)
	fmt.Fprintln(w, "  }")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "A single JSON document is written to stdout on success. Per-capture")
	fmt.Fprintln(w, "errors are reported inline; batch-level errors exit non-zero with a")
	fmt.Fprintln(w, "diagnostic on stderr.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "See also: chrest capture  (single-capture streaming output)")
}

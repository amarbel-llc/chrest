package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"code.linenisgreat.com/cutting-garden/pkgs/traversal_serve"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/command"

	"code.linenisgreat.com/chrest/go/internal/delta/bookmark_plugin"
)

// registerTraversalServeCommand registers a dewey entry for `traversal-serve`
// so it appears in `chrest --help`; the real dispatch is a bypass in main.go
// for the same reason capture-serve has one (a persistent JSON-RPC session
// over a self-created rendezvous socket does not fit the Result path).
func registerTraversalServeCommand(app *command.Utility) {
	app.AddCommand(&command.Command{
		Name: "traversal-serve",
		Description: command.Description{
			Short: "Serve the read-only bookmark: traversal plugin",
			Long: "traversal-serve is the cutting-garden RFC 0013 traversal-plugin " +
				"transport for the bookmark: scheme: one root per live chrest browser " +
				"socket, then the browser's bookmark folders and bookmarks, with " +
				"folder, domain and date_added facets. It binds a fresh rendezvous " +
				"socket, announces it on stdout, then serves one session until the " +
				"host shuts it down, stdin reaches EOF, or SIGTERM arrives.\n\n" +
				"It is launched by cutting-garden, not invoked directly: the host " +
				"sets TRAVERSAL_PLUGIN_COOKIE in the environment, and without it the " +
				"command exits non-zero without printing to stdout.",
		},
		SeeAlso: []string{"capture-serve"},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			return fmt.Errorf(
				"traversal-serve should be invoked directly (chrest traversal-serve); " +
					"the Result path does not support the RFC 0013 rendezvous-socket contract",
			)
		},
	})
}

// cmdTraversalServe is the plugin side of an RFC 0013 session, shaped like
// cmdCaptureServe: cookie, rendezvous socket, one announce line, accept, serve,
// with stdin-EOF and SIGTERM as lifecycle triggers Serve does not observe.
func cmdTraversalServe(ctx context.Context, version string, args []string) error {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			printTraversalServeHelp(os.Stdout)
			return nil
		}
	}

	cookie, err := traversal_serve.CookieFromEnv()
	if err != nil {
		// Refuse to serve without the launch cookie, and print nothing to
		// stdout while doing so.
		return err
	}

	src, err := bookmark_plugin.NewSocketSource()
	if err != nil {
		return fmt.Errorf("locate chrest state directory: %w", err)
	}

	ln, socketPath, cleanup, err := traversal_serve.ListenRendezvous()
	if err != nil {
		return fmt.Errorf("bind rendezvous socket: %w", err)
	}
	defer cleanup()

	line, err := traversal_serve.AnnounceLine(cookie, traversal_serve.Handshake{
		Version: traversal_serve.SchemaV1,
		Network: traversal_serve.HandshakeNetwork,
		Address: socketPath,
	})
	if err != nil {
		return fmt.Errorf("render announce line: %w", err)
	}

	signal.Ignore(syscall.SIGPIPE)

	sessionCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()

	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()

	// AcceptUnix does not observe the context; closing the listener on
	// cancellation unblocks it if the host never dials in.
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
			return nil
		}

		return fmt.Errorf("accept rendezvous connection: %w", err)
	}

	if err := traversal_serve.Serve(sessionCtx, conn, traversal_serve.ServeConfig{
		Plugin: bookmark_plugin.New(src),
		Info:   traversal_serve.PluginInfo{Name: "chrest", Version: version},
	}); err != nil {
		return fmt.Errorf("traversal-serve session: %w", err)
	}

	return nil
}

func printTraversalServeHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: chrest traversal-serve")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Serve the read-only bookmark: cutting-garden traversal plugin (RFC 0013).")
	fmt.Fprintln(w, "Launched by cutting-garden, which must set TRAVERSAL_PLUGIN_COOKIE in the")
	fmt.Fprintln(w, "environment; invoked without it, this command exits non-zero without")
	fmt.Fprintln(w, "printing to stdout.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "See also: chrest capture-serve")
}

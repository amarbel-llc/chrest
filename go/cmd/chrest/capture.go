package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"code.linenisgreat.com/chrest/go/internal/delta/tools"
)

const defaultCaptureTimeout = 60 * time.Second

// cmdCapture is the CLI-side handler for `chrest capture`. It bypasses
// command.RunCLI so the capture bytes can stream straight to the output
// (os.Stdout or the --output file) without the trailing newline that
// fmt.Println(r.Text) would append in the standard Result path (see
// issue #21). The command is still registered on the command.Utility for
// MCP use — that path buffers into a TextResult.
//
// The underlying dewey gap (no streaming Result variant) is tracked in
// amarbel-llc/purse-first#55. Once that lands, this bypass can be removed
// and the capture command can rely on a single Run handler for both CLI
// and MCP transports.
func cmdCapture(ctx context.Context, args []string) (err error) {
	// Ignore SIGPIPE so that a downstream reader closing the pipe early
	// (e.g. `chrest capture | head -c 4`) turns into an EPIPE on the next
	// write instead of killing the program. Without this, Go's default
	// SIGPIPE handler on fd 1/2 terminates chrest before StreamCapture's
	// deferred session.Close runs — leaving Firefox and its content
	// processes orphaned and blocking the bats shutdown.
	signal.Ignore(syscall.SIGPIPE)

	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: chrest capture --format <kind>[,<kind>...] [flags]")
		fmt.Fprintln(os.Stderr, "  Formats: pdf, screenshot-png, screenshot-jpeg, mhtml, a11y, text, html-monolith,")
		fmt.Fprintln(os.Stderr, "           html-outer, markdown-full, markdown-reader, markdown-selector")
		fmt.Fprintln(os.Stderr, "  Multiple comma-separated formats extract all in a single browser session.")
		fmt.Fprintln(os.Stderr, "  --output is required for multi-format and must be a directory path.")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "See also: chrest capture-batch  (JSON-stdin batch interface per RFC 0001)")
	}

	var params tools.CaptureParams
	var timeout time.Duration
	var output string
	var paperWidth, paperHeight float64
	var marginTop, marginBottom, marginLeft, marginRight float64
	fs.StringVar(&params.Format, "format", "", "Output format (comma-separated for multi): pdf, screenshot-png, screenshot-jpeg, mhtml, a11y, text, html-monolith, html-outer, markdown-full, markdown-reader, markdown-selector")
	fs.StringVar(&params.URL, "url", "", "URL to capture")
	fs.BoolVar(&params.Landscape, "landscape", false, "PDF only: use landscape orientation")
	fs.BoolVar(&params.NoHeaders, "no-headers", false, "PDF only: disable header and footer")
	fs.BoolVar(&params.Background, "background", false, "PDF only: print background graphics")
	fs.Float64Var(&paperWidth, "paper-width", 0, "PDF only: page width in inches (default: browser default, typically 8.5)")
	fs.Float64Var(&paperHeight, "paper-height", 0, "PDF only: page height in inches (default: browser default, typically 11)")
	fs.Float64Var(&marginTop, "margin-top", 0, "PDF only: top margin in inches (0 for borderless)")
	fs.Float64Var(&marginBottom, "margin-bottom", 0, "PDF only: bottom margin in inches (0 for borderless)")
	fs.Float64Var(&marginLeft, "margin-left", 0, "PDF only: left margin in inches (0 for borderless)")
	fs.Float64Var(&marginRight, "margin-right", 0, "PDF only: right margin in inches (0 for borderless)")
	fs.IntVar(&params.Quality, "quality", 0, "screenshot-jpeg only: JPEG quality (0-100)")
	fs.BoolVar(&params.FullPage, "full-page", false, "screenshot-png / screenshot-jpeg: capture the full scrollable page")
	fs.StringVar(&params.Selector, "selector", "", "markdown-selector only: CSS selector for the element to extract (first match wins)")
	fs.StringVar(&params.ReaderEngine, "reader-engine", "", "markdown-reader only: extraction engine (\"readability\" default; \"browser\" reserved/not-yet-implemented)")
	fs.IntVar(&params.ViewportWidth, "viewport-width", 0, "Viewport width in CSS pixels (e.g. 576 for thermal printer). Affects layout of all formats.")
	fs.DurationVar(&timeout, "timeout", defaultCaptureTimeout, "Abort and tear down the browser if the capture takes longer than this (0 disables)")
	fs.StringVar(&output, "output", "", "Write capture to this path. For multi-format, must be a directory (files named <format><ext>).")

	if err = fs.Parse(args); err != nil {
		return err
	}

	// Only set paper/margin when explicitly passed so nil = browser default.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "paper-width":
			params.PaperWidth.Value = &paperWidth
		case "paper-height":
			params.PaperHeight.Value = &paperHeight
		case "margin-top":
			params.MarginTop.Value = &marginTop
		case "margin-bottom":
			params.MarginBottom.Value = &marginBottom
		case "margin-left":
			params.MarginLeft.Value = &marginLeft
		case "margin-right":
			params.MarginRight.Value = &marginRight
		}
	})

	if params.Format == "" {
		fs.Usage()
		return fmt.Errorf("--format is required")
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	formats := strings.Split(params.Format, ",")
	if len(formats) > 1 {
		return cmdCaptureMulti(ctx, params, formats, output)
	}

	if err = params.Validate(); err != nil {
		return err
	}

	var w io.Writer = os.Stdout
	if output != "" {
		// Same-directory tmpfile so the final rename is atomic (same
		// filesystem guarantee). Leading dot keeps the in-flight file
		// out of shell glob expansions.
		f, cerr := os.CreateTemp(filepath.Dir(output), ".chrest-capture-*")
		if cerr != nil {
			return fmt.Errorf("create tmpfile: %w", cerr)
		}
		tmpPath := f.Name()
		w = f
		defer func() {
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
			if err != nil {
				os.Remove(tmpPath)
				return
			}
			if rerr := os.Rename(tmpPath, output); rerr != nil {
				err = rerr
				os.Remove(tmpPath)
			}
		}()
	}

	err = tools.StreamCapture(ctx, params, w)
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("capture timed out after %s", timeout)
	}
	return
}

func cmdCaptureMulti(
	ctx context.Context,
	params tools.CaptureParams,
	formats []string,
	output string,
) error {
	if output == "" {
		return fmt.Errorf("--output is required when capturing multiple formats (it must be a directory path)")
	}

	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Build MultiExtractParams from the user's full CaptureParams so PDF
	// flag passthrough (--landscape, --paper-width, --margin-*) reaches
	// every PDF result in the format set. The single-format helper
	// hard-codes Formats=[p.Format]; multi overrides it with the parsed
	// list.
	mep := params.ToMultiExtractParams()
	mep.Formats = formats

	results, err := tools.MultiExtract(ctx, mep)
	if err != nil {
		return err
	}

	var firstErr error
	for _, r := range results {
		if r.Err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", r.Format, r.Err)
			if firstErr == nil {
				firstErr = fmt.Errorf("format %s failed: %w", r.Format, r.Err)
			}
			continue
		}

		ext := tools.FormatExtension(r.Format)
		dest := filepath.Join(output, r.Format+ext)
		if werr := atomicWrite(dest, r.Data); werr != nil {
			fmt.Fprintf(os.Stderr, "%s: write error: %s\n", r.Format, werr)
			if firstErr == nil {
				firstErr = fmt.Errorf("format %s write failed: %w", r.Format, werr)
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", dest, len(r.Data))
	}
	return firstErr
}

func atomicWrite(dest string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dest), ".chrest-capture-*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

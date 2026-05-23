package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"code.linenisgreat.com/chrest/go/src/delta/proxy"
)

const (
	formatPDF              = "pdf"
	formatScreenshotPNG    = "screenshot-png"
	formatScreenshotJPEG   = "screenshot-jpeg"
	formatMHTML            = "mhtml"
	formatA11y             = "a11y"
	formatText             = "text"
	formatHTMLMonolith     = "html-monolith"
	formatMarkdownFull     = "markdown-full"
	formatMarkdownReader   = "markdown-reader"
	formatMarkdownSelector = "markdown-selector"
	formatHTMLOuter        = "html-outer"

	defaultViewportHeight = 1024
)

var captureFormats = []string{
	formatPDF,
	formatScreenshotPNG,
	formatScreenshotJPEG,
	formatMHTML,
	formatA11y,
	formatText,
	formatHTMLMonolith,
	formatMarkdownFull,
	formatMarkdownReader,
	formatMarkdownSelector,
	formatHTMLOuter,
}

var captureFormatsDesc = strings.Join(captureFormats, ", ")

var formatExtensions = map[string]string{
	formatPDF:              ".pdf",
	formatScreenshotPNG:    ".png",
	formatScreenshotJPEG:   ".jpeg",
	formatMHTML:            ".mhtml",
	formatA11y:             ".json",
	formatText:             ".txt",
	formatHTMLMonolith:     ".html",
	formatHTMLOuter:        ".html",
	formatMarkdownFull:     ".md",
	formatMarkdownReader:   ".md",
	formatMarkdownSelector: ".md",
}

func FormatExtension(format string) string {
	if ext, ok := formatExtensions[format]; ok {
		return ext
	}
	return ""
}

func ValidFormat(s string) bool {
	for _, f := range captureFormats {
		if f == s {
			return true
		}
	}
	return false
}

// readerEngineReadability runs extraction via the embedded Go
// Readability port. Default and only currently-supported engine.
const readerEngineReadability = "readability"

// readerEngineBrowser would use the browser's native reader view
// (Firefox about:reader). Declared here so the spec surface is stable;
// ConvertReader currently rejects this engine with not-yet-implemented.
const readerEngineBrowser = "browser"

// flexFloat accepts both JSON numbers (1.5) and JSON strings ("1.5").
// Needed because dewey lacks a FloatFlag type, so the MCP schema declares
// these params as "string" while the CLI uses flag.Float64Var directly.
type flexFloat struct {
	Value *float64
}

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		f.Value = &n
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expected number or string, got %s", string(b))
	}
	if s == "" {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid number %q: %w", s, err)
	}
	f.Value = &n
	return nil
}

func (f flexFloat) MarshalJSON() ([]byte, error) {
	if f.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*f.Value)
}

// CaptureParams is the shared parameter struct for the `capture` command,
// used by both the MCP tool handler and the CLI bypass in
// go/cmd/chrest/capture.go.
type CaptureParams struct {
	Format        string    `json:"format"`
	URL           string    `json:"url"`
	Browser       string    `json:"browser"`
	Landscape     bool      `json:"landscape"`
	NoHeaders     bool      `json:"no-headers"`
	Background    bool      `json:"background"`
	Quality       int       `json:"quality"`
	FullPage      bool      `json:"full-page"`
	Selector      string    `json:"selector"`
	ReaderEngine  string    `json:"reader-engine"`
	PaperWidth    flexFloat `json:"paper-width"`
	PaperHeight   flexFloat `json:"paper-height"`
	MarginTop     flexFloat `json:"margin-top"`
	MarginBottom  flexFloat `json:"margin-bottom"`
	MarginLeft    flexFloat `json:"margin-left"`
	MarginRight   flexFloat `json:"margin-right"`
	ViewportWidth int       `json:"viewport-width"`
}

// Validate rejects format-specific flags used with an incompatible --format,
// so mistakes surface instead of being silently ignored.
func (p CaptureParams) Validate() error {
	pdfOnly := p.Landscape || p.NoHeaders || p.Background
	pdfOnly = pdfOnly || p.PaperWidth.Value != nil || p.PaperHeight.Value != nil
	pdfOnly = pdfOnly || p.MarginTop.Value != nil || p.MarginBottom.Value != nil
	pdfOnly = pdfOnly || p.MarginLeft.Value != nil || p.MarginRight.Value != nil
	if pdfOnly && p.Format != formatPDF {
		return fmt.Errorf("--landscape, --no-headers, --background, --paper-width, --paper-height, --margin-* are only valid with --format %s", formatPDF)
	}
	if p.Quality != 0 && p.Format != formatScreenshotJPEG {
		return fmt.Errorf("--quality is only valid with --format %s", formatScreenshotJPEG)
	}
	if p.FullPage && p.Format != formatScreenshotPNG && p.Format != formatScreenshotJPEG {
		return fmt.Errorf("--full-page is only valid with --format %s or %s", formatScreenshotPNG, formatScreenshotJPEG)
	}
	if p.Selector != "" && p.Format != formatMarkdownSelector {
		return fmt.Errorf("--selector is only valid with --format %s", formatMarkdownSelector)
	}
	if p.Format == formatMarkdownSelector && p.Selector == "" {
		return fmt.Errorf("--selector is required with --format %s", formatMarkdownSelector)
	}
	if p.ReaderEngine != "" {
		if p.Format != formatMarkdownReader {
			return fmt.Errorf("--reader-engine is only valid with --format %s", formatMarkdownReader)
		}
		switch p.ReaderEngine {
		case readerEngineReadability:
			// ok
		case readerEngineBrowser:
			return fmt.Errorf("--reader-engine=%s is not yet implemented (tracked as a follow-up under chrest#29); use %s", readerEngineBrowser, readerEngineReadability)
		default:
			return fmt.Errorf("--reader-engine must be one of %q, %q (got %q)", readerEngineReadability, readerEngineBrowser, p.ReaderEngine)
		}
	}
	return nil
}

func registerCaptureCommands(app *command.Utility, _ *proxy.BrowserProxy) {
	app.AddCommand(&command.Command{
		Name: "capture",
		Description: command.Description{
			Short: "Capture a web page in a given format",
			Long: "Capture renders a web page and writes its content to stdout (or to a file " +
				"with --output). The --format flag selects the output format:\n\n" +
				"Document formats: pdf, mhtml, html-monolith, html-outer, text, a11y.\n" +
				"Screenshot formats: screenshot-png, screenshot-jpeg.\n" +
				"Semantic formats: markdown-full, markdown-reader, markdown-selector.\n\n" +
				"The browser backend is headless Firefox via WebDriver BiDi.\n\n" +
				"When --output is set, the capture is written atomically via a same-directory " +
				"tmpfile and rename. The tmpfile is cleaned up on failure.\n\n" +
				"PDF captures accept paper dimension and margin overrides (in inches). A " +
				"margin of 0 is valid for borderless output. When unset, the browser default " +
				"is used (typically US Letter 8.5\"x11\" with ~0.4\" margins).",
		},
		SeeAlso: []string{"capture-batch"},
		Examples: []command.Example{
			{
				Description: "Save a page as PDF",
				Command:     "chrest capture --format pdf --url https://example.com --output page.pdf",
			},
			{
				Description: "Thermal printer: 57mm-wide paper, no side margins",
				Command:     "chrest capture --format pdf --paper-width 2.2409 --margin-left 0 --margin-right 0 --url https://example.com",
			},
			{
				Description: "Full-page screenshot",
				Command:     "chrest capture --format screenshot-png --full-page --url https://example.com --output page.png",
			},
			{
				Description: "Extract readable article content as Markdown",
				Command:     "chrest capture --format markdown-reader --url https://example.com/article",
			},
		},
		Params: []command.Param{
			command.StringFlag{
				Name:        "format",
				Description: "Output format: " + captureFormatsDesc,
				Required:    true,
				EnumValues:  captureFormats,
			},
			command.StringFlag{Name: "url", Description: "URL to capture"},
			command.BoolFlag{Name: "landscape", Description: "PDF only: use landscape orientation"},
			command.BoolFlag{Name: "no-headers", Description: "PDF only: disable header and footer"},
			command.BoolFlag{Name: "background", Description: "PDF only: print background graphics"},
			command.StringFlag{Name: "paper-width", Description: "PDF only: page width in inches (default: browser default, typically 8.5)"},
			command.StringFlag{Name: "paper-height", Description: "PDF only: page height in inches (default: browser default, typically 11)"},
			command.StringFlag{Name: "margin-top", Description: "PDF only: top margin in inches (0 for borderless)"},
			command.StringFlag{Name: "margin-bottom", Description: "PDF only: bottom margin in inches (0 for borderless)"},
			command.StringFlag{Name: "margin-left", Description: "PDF only: left margin in inches (0 for borderless)"},
			command.StringFlag{Name: "margin-right", Description: "PDF only: right margin in inches (0 for borderless)"},
			command.IntFlag{Name: "quality", Description: "screenshot-jpeg only: JPEG quality (0-100)"},
			command.BoolFlag{Name: "full-page", Description: "screenshot-png / screenshot-jpeg: capture the full scrollable page"},
			command.StringFlag{Name: "selector", Description: "markdown-selector only: CSS selector for the element to extract (first match wins)"},
			command.StringFlag{Name: "reader-engine", Description: "markdown-reader only: extraction engine (\"readability\" default; \"browser\" reserved/not-yet-implemented)"},
			command.IntFlag{Name: "viewport-width", Description: "Viewport width in CSS pixels (e.g. 576 for thermal printer). Affects layout of all formats."},
		},
		// CLI-only: the real CLI dispatch happens via the bypass in
		// go/cmd/chrest/main.go (cmdCapture), which streams capture bytes
		// directly to stdout/--output without dewey's TextResult buffering.
		// This shim exists so dewey emits manpages, completions, and CLI
		// help for `chrest capture`. With Run unset, the command is
		// excluded from the MCP tool surface (see dewey/.../mcp.go).
		RunCLI: func(_ context.Context, _ json.RawMessage) error {
			return nil
		},
	})
}

// StreamCapture runs a single-format capture and copies the bytes to w.
// Routes through MultiExtract so the CLI single-format path shares one
// engine with the multi-format path and with web-fetch — see
// MultiExtractParams. The browser session is opened, the URL navigated
// to, the format extracted, and the session closed before returning.
func StreamCapture(
	ctx context.Context,
	params CaptureParams,
	w io.Writer,
) error {
	results, err := MultiExtract(ctx, params.ToMultiExtractParams())
	if err != nil {
		return errors.Wrap(err)
	}
	if len(results) != 1 {
		return fmt.Errorf("MultiExtract returned %d results for one format", len(results))
	}
	r := results[0]
	if r.Err != nil {
		return errors.Wrap(r.Err)
	}
	if _, err := w.Write(r.Data); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// ToMultiExtractParams flattens CaptureParams into MultiExtractParams.
// The PDF flexFloat fields collapse to *float64; everything else maps
// 1:1.
func (p CaptureParams) ToMultiExtractParams() MultiExtractParams {
	return MultiExtractParams{
		URL:           p.URL,
		Formats:       []string{p.Format},
		Selector:      p.Selector,
		ReaderEngine:  p.ReaderEngine,
		Quality:       p.Quality,
		FullPage:      p.FullPage,
		ViewportWidth: p.ViewportWidth,

		Landscape:    p.Landscape,
		NoHeaders:    p.NoHeaders,
		Background:   p.Background,
		PaperWidth:   p.PaperWidth.Value,
		PaperHeight:  p.PaperHeight.Value,
		MarginTop:    p.MarginTop.Value,
		MarginBottom: p.MarginBottom.Value,
		MarginLeft:   p.MarginLeft.Value,
		MarginRight:  p.MarginRight.Value,
	}
}

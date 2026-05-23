package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/chrest/go/internal/charlie/firefox"
	"code.linenisgreat.com/chrest/go/internal/charlie/markdown"
	"code.linenisgreat.com/chrest/go/internal/charlie/monolith"
)

type MultiExtractParams struct {
	URL           string
	Formats       []string
	Selector      string
	ReaderEngine  string
	Quality       int
	FullPage      bool
	ViewportWidth int

	// PDF-only flags. Forwarded verbatim to firefox.PDFOptions when
	// formatPDF is in Formats. nil pointers and false bools mean "use
	// the browser default" (typically US Letter, 0.4" margins, headers
	// on, no background, portrait). Web-fetch leaves these zero.
	Landscape    bool
	NoHeaders    bool
	Background   bool
	PaperWidth   *float64
	PaperHeight  *float64
	MarginTop    *float64
	MarginBottom *float64
	MarginLeft   *float64
	MarginRight  *float64
}

type FormatResult struct {
	Format string
	Data   []byte
	Err    error
}

func MultiExtract(
	ctx context.Context,
	params MultiExtractParams,
) ([]FormatResult, error) {
	if params.URL == "" {
		return nil, fmt.Errorf("URL is required")
	}
	if len(params.Formats) == 0 {
		return nil, fmt.Errorf("at least one format is required")
	}
	for _, f := range params.Formats {
		if !ValidFormat(f) {
			return nil, fmt.Errorf("unknown format %q", f)
		}
	}

	session, err := openCaptureSession(ctx, params.URL)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer session.Close()

	if params.ViewportWidth > 0 {
		if err := session.SetViewport(ctx, params.ViewportWidth, defaultViewportHeight); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	if err := session.Navigate(ctx, params.URL); err != nil {
		return nil, errors.Wrap(err)
	}

	return multiExtractFromSession(ctx, session, params), nil
}

// openCaptureSession opens a fresh headless-Firefox session for a
// capture. The url is required so we fail fast (and consistently with
// how the CLI surfaced this error before unification) rather than
// launching Firefox just to discover there's nothing to navigate to.
func openCaptureSession(
	ctx context.Context,
	url string,
) (*firefox.Session, error) {
	if url == "" {
		return nil, fmt.Errorf("--url is required")
	}
	return firefox.NewSession(ctx)
}

// captureSession is the subset of *firefox.Session used by the internal
// extract helpers. Defined here so tests can inject a mock without depending
// on the real Firefox binary.
type captureSession interface {
	Navigate(ctx context.Context, url string) error
	SetViewport(ctx context.Context, width, height int) error
	GetDocumentHTML(ctx context.Context) (io.ReadCloser, error)
	ExtractText(ctx context.Context) (io.ReadCloser, error)
	PrintToPDF(ctx context.Context, opts firefox.PDFOptions) (io.ReadCloser, error)
	CaptureScreenshot(ctx context.Context, opts firefox.ScreenshotOptions) (io.ReadCloser, error)
	CaptureSnapshot(ctx context.Context) (io.ReadCloser, error)
	AccessibilityTree(ctx context.Context) (io.ReadCloser, error)
	BrowserInfo(ctx context.Context) (firefox.BrowserInfo, error)
	LastNavigationHTTP() (firefox.HTTPResponse, bool)
	Close() error
}

func multiExtractFromSession(
	ctx context.Context,
	s captureSession,
	params MultiExtractParams,
) []FormatResult {
	results := make([]FormatResult, len(params.Formats))

	var domBytes []byte
	var domErr error
	if anyFormatNeedsDOM(params.Formats) {
		var rc io.ReadCloser
		rc, domErr = s.GetDocumentHTML(ctx)
		if domErr == nil {
			domBytes, domErr = io.ReadAll(rc)
			rc.Close()
		}
	}

	for i, format := range params.Formats {
		results[i].Format = format
		results[i].Data, results[i].Err = extractOne(ctx, s, format, params, domBytes, domErr)
	}

	return results
}

var domFormats = map[string]bool{
	formatHTMLOuter:        true,
	formatHTMLMonolith:     true,
	formatMarkdownFull:     true,
	formatMarkdownReader:   true,
	formatMarkdownSelector: true,
}

func anyFormatNeedsDOM(formats []string) bool {
	for _, f := range formats {
		if domFormats[f] {
			return true
		}
	}
	return false
}

func extractOne(
	ctx context.Context,
	s captureSession,
	format string,
	params MultiExtractParams,
	domBytes []byte,
	domErr error,
) ([]byte, error) {
	switch format {
	case formatText:
		return readAllCloser(s.ExtractText(ctx))

	case formatHTMLOuter:
		if domErr != nil {
			return nil, domErr
		}
		return domBytes, nil

	case formatHTMLMonolith:
		if domErr != nil {
			return nil, domErr
		}
		return readAllCloser(monolith.Process(ctx, bytes.NewReader(domBytes), params.URL))

	case formatMarkdownFull:
		if domErr != nil {
			return nil, domErr
		}
		return readAllCloser(markdown.ConvertFull(ctx, bytes.NewReader(domBytes)))

	case formatMarkdownReader:
		if domErr != nil {
			return nil, domErr
		}
		return readAllCloser(markdown.ConvertReader(ctx, bytes.NewReader(domBytes), params.URL))

	case formatMarkdownSelector:
		if domErr != nil {
			return nil, domErr
		}
		// Heading-aware: an `#id` selector matching a heading returns
		// the whole section (heading + following siblings up to the
		// next equal-or-higher heading) rather than just the heading
		// element. Matches web-fetch's selector behavior.
		return readAllCloser(markdown.ConvertSelectorSection(bytes.NewReader(domBytes), params.Selector))

	case formatPDF:
		return readAllCloser(s.PrintToPDF(ctx, firefox.PDFOptions{
			Landscape:           params.Landscape,
			DisplayHeaderFooter: !params.NoHeaders,
			PrintBackground:     params.Background,
			PaperWidth:          params.PaperWidth,
			PaperHeight:         params.PaperHeight,
			MarginTop:           params.MarginTop,
			MarginBottom:        params.MarginBottom,
			MarginLeft:          params.MarginLeft,
			MarginRight:         params.MarginRight,
		}))

	case formatScreenshotPNG:
		return readAllCloser(s.CaptureScreenshot(ctx, firefox.ScreenshotOptions{
			Format:   "png",
			FullPage: params.FullPage,
		}))

	case formatScreenshotJPEG:
		return readAllCloser(s.CaptureScreenshot(ctx, firefox.ScreenshotOptions{
			Format:   "jpeg",
			Quality:  params.Quality,
			FullPage: params.FullPage,
		}))

	case formatMHTML:
		return readAllCloser(s.CaptureSnapshot(ctx))

	case formatA11y:
		return readAllCloser(s.AccessibilityTree(ctx))

	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

func readAllCloser(rc io.ReadCloser, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

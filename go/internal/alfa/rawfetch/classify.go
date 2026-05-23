// Package rawfetch classifies HTTP responses for the web-fetch MCP
// tool's content-type-aware dispatch and builds the text/markdown/html
// content slots when the body is already plain text.
//
// See docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md.
package rawfetch

import (
	"mime"
	"net/http"
	"path"
	"strings"
)

// Class is the dispatch decision for a single web-fetch response.
type Class int

const (
	ClassUnknown Class = iota
	ClassHTML
	ClassText
	ClassBinary
	ClassHTTPError
)

// Classify decides which web-fetch path a response should take, based
// on HTTP status, Content-Type, Content-Disposition, and (as a last-
// resort fallback) URL extension.
func Classify(headers http.Header, urlStr string, status int) Class {
	if status < 200 || status >= 300 {
		return ClassHTTPError
	}
	disp, _, _ := mime.ParseMediaType(headers.Get("Content-Disposition"))
	if strings.EqualFold(disp, "attachment") {
		return ClassBinary
	}

	ct := headers.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)

	switch mt {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return ClassHTML
	}

	if isTextMediaType(mt) {
		return ClassText
	}

	if mt == "" {
		if isTextExtension(path.Ext(urlStr)) {
			return ClassText
		}
		return ClassBinary
	}

	return ClassBinary
}

// isTextMediaType returns true for media types whose body should be
// returned as raw text in the web-fetch tool. The list is deliberately
// narrow: text/css, text/javascript, text/csv etc. are not included
// because the tool is intended for prose/documentation/source code,
// not for treating every text/* response as readable content.
func isTextMediaType(mt string) bool {
	switch mt {
	case "text/plain",
		"text/markdown",
		"text/x-markdown",
		"application/json",
		"application/xml",
		"application/x-yaml",
		"application/yaml": // RFC 9512; application/x-yaml is the legacy form
		return true
	}
	if strings.HasPrefix(mt, "text/x-") {
		return true
	}
	return false
}

var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".json": true,
	".toml": true, ".yaml": true, ".yml": true,
	".go": true, ".py": true, ".rs": true,
	".c": true, ".cpp": true, ".h": true,
	".sh": true, ".bash": true,
}

func isTextExtension(ext string) bool {
	return textExtensions[strings.ToLower(ext)]
}

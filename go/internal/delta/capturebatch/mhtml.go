package capturebatch

import (
	"bufio"
	"bytes"
	"fmt"
	"net/textproto"
	"sort"
	"strings"
)

// mhtmlFixedBoundary is the deterministic boundary the normalizer
// substitutes for the per-generation boundary string Chrome emits.
// Per RFC 0001 §mhtml: "Replace the outer MIME boundary with a fixed,
// implementation-defined string." Chosen to be obviously
// chrest-synthesized and long enough that it won't collide with body
// bytes on any realistic page.
const mhtmlFixedBoundary = "----=_chrest_normalized_mhtml_boundary_v1"

// normalizeMHTML implements RFC 0001 §mhtml normalization:
//
//  1. Outer boundary replaced with a fixed string.
//  2. Per-part Date: headers removed.
//  3. Parts ordered lexicographically by Content-Location
//     (Chrome's MHTML uses Content-Location rather than Content-ID).
//
// Original boundary and removed headers are recorded in the returned
// stripped map under the "mhtml" key so consumers can reconstruct
// an approximation of the original bytes if they want.
//
// Part bodies are passed through raw — no Content-Transfer-Encoding
// decoding — so the emitted MHTML stays a valid MIME document.
func normalizeMHTML(raw []byte) ([]byte, map[string]any, error) {
	header, body, err := splitOuterHeader(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("mhtml: %w", err)
	}

	ctype := header.Get("Content-Type")
	boundary, err := extractBoundary(ctype)
	if err != nil {
		return nil, nil, fmt.Errorf("mhtml: %w", err)
	}

	parts, err := splitParts(body, boundary)
	if err != nil {
		return nil, nil, fmt.Errorf("mhtml: %w", err)
	}

	stripped := map[string]any{
		"boundary": boundary,
	}

	// Strip per-part Date: headers, collecting them into stripped.
	type removed struct {
		location string
		date     string
	}
	var removedDates []removed
	for i := range parts {
		if date := parts[i].headers.Get("Date"); date != "" {
			loc := parts[i].headers.Get("Content-Location")
			removedDates = append(removedDates, removed{location: loc, date: date})
			parts[i].headers.Del("Date")
		}
	}
	if len(removedDates) > 0 {
		items := make([]any, 0, len(removedDates))
		for _, r := range removedDates {
			items = append(items, map[string]any{
				"content_location": r.location,
				"date":             r.date,
			})
		}
		stripped["part_dates"] = items
	}

	// Also strip an outer Date: (Chrome's MHTML has one at the top level).
	if date := header.Get("Date"); date != "" {
		stripped["outer_date"] = date
		header.Del("Date")
	}

	// Sort parts by Content-Location. Parts without a Content-Location
	// sort before ones that have it (consistent rather than
	// interleaved).
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].headers.Get("Content-Location") < parts[j].headers.Get("Content-Location")
	})

	// Substitute the outer Content-Type boundary with the fixed one.
	newCtype := replaceBoundary(ctype, mhtmlFixedBoundary)
	header.Set("Content-Type", newCtype)

	var out bytes.Buffer
	writeHeader(&out, header)
	out.WriteString("\r\n")
	for _, p := range parts {
		out.WriteString("--")
		out.WriteString(mhtmlFixedBoundary)
		out.WriteString("\r\n")
		writeHeader(&out, p.headers)
		out.WriteString("\r\n")
		out.Write(p.body)
		if !bytes.HasSuffix(p.body, []byte("\r\n")) {
			out.WriteString("\r\n")
		}
	}
	out.WriteString("--")
	out.WriteString(mhtmlFixedBoundary)
	out.WriteString("--\r\n")

	return out.Bytes(), map[string]any{"mhtml": stripped}, nil
}

// splitOuterHeader parses the leading RFC 5322 / MIME header block
// (the bytes before the first blank line) and returns it as a
// textproto.MIMEHeader plus the remaining body bytes.
func splitOuterHeader(raw []byte) (textproto.MIMEHeader, []byte, error) {
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	bodyStart := 4
	if idx < 0 {
		// Fall back to LF-only separator.
		idx = bytes.Index(raw, []byte("\n\n"))
		bodyStart = 2
		if idx < 0 {
			return nil, nil, fmt.Errorf("no header/body separator found")
		}
	}
	headerBytes := raw[:idx]
	body := raw[idx+bodyStart:]

	tr := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(headerBytes, '\r', '\n', '\r', '\n'))))
	h, err := tr.ReadMIMEHeader()
	if err != nil {
		return nil, nil, fmt.Errorf("parse headers: %w", err)
	}
	return h, body, nil
}

func extractBoundary(contentType string) (string, error) {
	// Content-Type: multipart/related; boundary="abc"; type="..."
	// Do a minimal parse rather than pulling in mime.ParseMediaType —
	// boundary strings can contain characters that Go's parser rejects.
	idx := strings.Index(strings.ToLower(contentType), "boundary=")
	if idx < 0 {
		return "", fmt.Errorf("no boundary= in %q", contentType)
	}
	rest := contentType[idx+len("boundary="):]
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return "", fmt.Errorf("unterminated quoted boundary in %q", contentType)
		}
		return rest[1 : 1+end], nil
	}
	end := strings.IndexAny(rest, "; \t\r\n")
	if end < 0 {
		return rest, nil
	}
	return rest[:end], nil
}

func replaceBoundary(contentType, newBoundary string) string {
	idx := strings.Index(strings.ToLower(contentType), "boundary=")
	if idx < 0 {
		return contentType + `; boundary="` + newBoundary + `"`
	}
	prefix := contentType[:idx+len("boundary=")]
	rest := contentType[idx+len("boundary="):]
	rest = strings.TrimLeft(rest, " \t")
	var tail string
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end >= 0 {
			tail = rest[1+end+1:]
		}
	} else {
		end := strings.IndexAny(rest, "; \t\r\n")
		if end >= 0 {
			tail = rest[end:]
		}
	}
	return prefix + `"` + newBoundary + `"` + tail
}

type mhtmlPart struct {
	headers textproto.MIMEHeader
	body    []byte
}

func splitParts(body []byte, boundary string) ([]mhtmlPart, error) {
	startMarker := []byte("--" + boundary)
	endMarker := []byte("--" + boundary + "--")

	var parts []mhtmlPart
	cursor := 0
	for {
		if cursor >= len(body) {
			break
		}
		idx := bytes.Index(body[cursor:], startMarker)
		if idx < 0 {
			break
		}
		absStart := cursor + idx
		// Skip past the marker + trailing CRLF or LF.
		afterMarker := absStart + len(startMarker)
		if bytes.HasPrefix(body[afterMarker:], []byte("--")) {
			// End marker.
			break
		}
		afterMarker = skipEOL(body, afterMarker)

		// Find next marker.
		nextIdx := bytes.Index(body[afterMarker:], startMarker)
		if nextIdx < 0 {
			// Try end marker.
			nextIdx = bytes.Index(body[afterMarker:], endMarker)
			if nextIdx < 0 {
				return nil, fmt.Errorf("unterminated part starting at %d", absStart)
			}
		}
		partEnd := afterMarker + nextIdx

		partBytes := body[afterMarker:partEnd]
		// Trim the trailing CRLF that sits between the part body and the next
		// boundary marker. Boundary grammar requires that CRLF.
		partBytes = bytes.TrimRight(partBytes, "\r\n")

		hdrs, pbody, err := splitPartHeader(partBytes)
		if err != nil {
			return nil, fmt.Errorf("part at offset %d: %w", absStart, err)
		}
		parts = append(parts, mhtmlPart{headers: hdrs, body: pbody})

		cursor = partEnd
	}
	return parts, nil
}

func skipEOL(b []byte, i int) int {
	if i < len(b) && b[i] == '\r' {
		i++
	}
	if i < len(b) && b[i] == '\n' {
		i++
	}
	return i
}

func splitPartHeader(raw []byte) (textproto.MIMEHeader, []byte, error) {
	// Normalize LF-only to CRLF so textproto is happy.
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		sep = []byte("\n\n")
		idx = bytes.Index(raw, sep)
		if idx < 0 {
			return nil, nil, fmt.Errorf("no header/body separator in part")
		}
	}
	headerBytes := raw[:idx]
	body := raw[idx+len(sep):]
	tr := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(headerBytes, '\r', '\n', '\r', '\n'))))
	h, err := tr.ReadMIMEHeader()
	if err != nil {
		return nil, nil, err
	}
	return h, body, nil
}

// writeHeader writes headers in sorted key order (stable output).
func writeHeader(w *bytes.Buffer, h textproto.MIMEHeader) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			w.WriteString(k)
			w.WriteString(": ")
			w.WriteString(v)
			w.WriteString("\r\n")
		}
	}
}

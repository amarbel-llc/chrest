package capturebatch

import (
	"bytes"
	"strings"
	"testing"
)

// synthMHTML returns a minimal Chrome-shaped MHTML fixture with the
// given boundary and per-part Date headers. Tests use two distinct
// boundaries + swapped part order to verify normalization produces
// identical bytes regardless of input variation.
func synthMHTML(boundary string, swapOrder bool, outerDate string) []byte {
	parts := []string{
		"Content-Location: https://example.com/a.html\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n" +
			"Date: Mon, 15 Apr 2026 12:00:00 GMT\r\n" +
			"\r\n" +
			"<html><body>A</body></html>",
		"Content-Location: https://example.com/b.css\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n" +
			"Content-Type: text/css\r\n" +
			"Date: Mon, 15 Apr 2026 12:00:01 GMT\r\n" +
			"\r\n" +
			"body { color: red; }",
	}
	if swapOrder {
		parts[0], parts[1] = parts[1], parts[0]
	}
	var out bytes.Buffer
	out.WriteString("Content-Type: multipart/related; boundary=\"")
	out.WriteString(boundary)
	out.WriteString("\"; type=\"text/html\"\r\n")
	out.WriteString("MIME-Version: 1.0\r\n")
	out.WriteString("Subject: Test page\r\n")
	if outerDate != "" {
		out.WriteString("Date: ")
		out.WriteString(outerDate)
		out.WriteString("\r\n")
	}
	out.WriteString("\r\n")
	for _, p := range parts {
		out.WriteString("--")
		out.WriteString(boundary)
		out.WriteString("\r\n")
		out.WriteString(p)
		out.WriteString("\r\n")
	}
	out.WriteString("--")
	out.WriteString(boundary)
	out.WriteString("--\r\n")
	return out.Bytes()
}

func TestNormalizeMHTML_ByteStableAcrossBoundariesAndOrder(t *testing.T) {
	a, _, err := normalizeMHTML(synthMHTML("----=_Part_boundary_A_98765", false, "Wed, 17 Apr 2026 09:00:00 GMT"))
	if err != nil {
		t.Fatalf("normalize a: %v", err)
	}
	b, _, err := normalizeMHTML(synthMHTML("----=_Part_boundary_B_12345", true, "Thu, 18 Apr 2026 10:00:00 GMT"))
	if err != nil {
		t.Fatalf("normalize b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("expected byte-identical output across boundary + order variations\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

func TestNormalizeMHTML_FixedBoundary(t *testing.T) {
	out, _, err := normalizeMHTML(synthMHTML("----=_chrome_generated_xyz", false, ""))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !bytes.Contains(out, []byte(mhtmlFixedBoundary)) {
		t.Fatalf("output missing fixed boundary %q", mhtmlFixedBoundary)
	}
	if bytes.Contains(out, []byte("----=_chrome_generated_xyz")) {
		t.Fatalf("output still contains original boundary")
	}
}

func TestNormalizeMHTML_StripsDateHeaders(t *testing.T) {
	out, stripped, err := normalizeMHTML(synthMHTML("----=_b", false, "Wed, 17 Apr 2026 09:00:00 GMT"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// No Date: header should remain anywhere in the output.
	if bytes.Contains(bytes.ToLower(out), []byte("\r\ndate:")) {
		t.Fatalf("output still contains Date: header")
	}
	mhtmlStripped, ok := stripped["mhtml"].(map[string]any)
	if !ok {
		t.Fatalf("stripped.mhtml not present or wrong type: %#v", stripped)
	}
	if mhtmlStripped["boundary"] != "----=_b" {
		t.Fatalf("original boundary not recorded: %#v", mhtmlStripped["boundary"])
	}
	if mhtmlStripped["outer_date"] != "Wed, 17 Apr 2026 09:00:00 GMT" {
		t.Fatalf("outer_date not recorded: %#v", mhtmlStripped["outer_date"])
	}
	items, ok := mhtmlStripped["part_dates"].([]any)
	if !ok {
		t.Fatalf("part_dates missing or wrong type: %#v", mhtmlStripped["part_dates"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 part_dates, got %d", len(items))
	}
}

func TestNormalizeMHTML_SortsPartsByLocation(t *testing.T) {
	out, _, err := normalizeMHTML(synthMHTML("----=_b", true, ""))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	s := string(out)
	a := strings.Index(s, "https://example.com/a.html")
	b := strings.Index(s, "https://example.com/b.css")
	if a < 0 || b < 0 {
		t.Fatalf("both part locations should appear: a=%d b=%d", a, b)
	}
	if a > b {
		t.Fatalf("expected a.html to sort before b.css, but a=%d b=%d", a, b)
	}
}

func TestNormalizeMHTML_MissingBoundary(t *testing.T) {
	bad := []byte("Content-Type: multipart/related\r\nMIME-Version: 1.0\r\n\r\nbody")
	if _, _, err := normalizeMHTML(bad); err == nil {
		t.Fatalf("expected error for missing boundary")
	}
}

func TestExtractBoundary(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`multipart/related; boundary="abc"; type="text/html"`, "abc"},
		{`multipart/related; boundary=abc`, "abc"},
		{`multipart/related; boundary="ab=cd"; type="text/html"`, "ab=cd"},
	}
	for _, c := range cases {
		got, err := extractBoundary(c.in)
		if err != nil {
			t.Errorf("extractBoundary(%q) err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("extractBoundary(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReplaceBoundary(t *testing.T) {
	in := `multipart/related; boundary="abc"; type="text/html"`
	out := replaceBoundary(in, "new_boundary")
	if !strings.Contains(out, `boundary="new_boundary"`) {
		t.Errorf("replaceBoundary result missing new boundary: %q", out)
	}
	if strings.Contains(out, `"abc"`) {
		t.Errorf("replaceBoundary result still contains old boundary: %q", out)
	}
	if !strings.Contains(out, `type="text/html"`) {
		t.Errorf("replaceBoundary dropped trailing params: %q", out)
	}
}

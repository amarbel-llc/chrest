package rawfetch

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		disposition string
		urlExt      string
		status      int
		want        Class
	}{
		{"text/html → HTML", "text/html; charset=utf-8", "", "", 200, ClassHTML},
		{"application/xhtml+xml → HTML", "application/xhtml+xml", "", "", 200, ClassHTML},
		{"image/svg+xml → HTML", "image/svg+xml", "", "", 200, ClassHTML},
		{"text/plain → Text", "text/plain; charset=utf-8", "", "", 200, ClassText},
		{"text/markdown → Text", "text/markdown", "", "", 200, ClassText},
		{"text/x-rst → Text (prefix branch)", "text/x-rst", "", "", 200, ClassText},
		{"application/json → Text", "application/json", "", "", 200, ClassText},
		{"application/xml → Text", "application/xml", "", "", 200, ClassText},
		{".md ext overrides missing ct → Text", "", "", ".md", 200, ClassText},
		{".toml ext → Text", "", "", ".toml", 200, ClassText},
		{".go ext → Text", "", "", ".go", 200, ClassText},
		{"image/png → Binary", "image/png", "", "", 200, ClassBinary},
		{"application/octet-stream → Binary", "application/octet-stream", "", "", 200, ClassBinary},
		{"application/zip → Binary", "application/zip", "", "", 200, ClassBinary},
		{"text/plain + attachment → Binary", "text/plain", "attachment; filename=foo.txt", "", 200, ClassBinary},
		{"404 → HTTPError (regardless of ct)", "text/html", "", "", 404, ClassHTTPError},
		{"500 → HTTPError", "text/plain", "", "", 500, ClassHTTPError},
		{"empty content-type, no ext → Binary", "", "", "", 200, ClassBinary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.contentType != "" {
				h.Set("Content-Type", tc.contentType)
			}
			if tc.disposition != "" {
				h.Set("Content-Disposition", tc.disposition)
			}
			got := Classify(h, "https://example.com/foo"+tc.urlExt, tc.status)
			if got != tc.want {
				t.Errorf("Classify(%+v) = %v; want %v", tc, got, tc.want)
			}
		})
	}
}

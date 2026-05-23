package capturebatch

import "testing"

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single-line", "hello", "hello"},
		{"trailing-lf-preserved", "hello\n", "hello\n"},
		{"crlf-to-lf", "a\r\nb\r\nc", "a\nb\nc"},
		{"cr-to-lf", "a\rb\rc", "a\nb\nc"},
		{"mixed-endings", "a\nb\r\nc\rd", "a\nb\nc\nd"},
		{"trailing-space-per-line", "hello  \nworld\t\t\n", "hello\nworld\n"},
		{"internal-space-kept", "a b  c\nd\te", "a b  c\nd\te"},
		{"all-whitespace-line", "   \n", "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeText([]byte(tt.in))
			if string(got) != tt.want {
				t.Errorf("normalizeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalize_UnknownFormat(t *testing.T) {
	if _, _, err := Normalize("bogus", []byte("x")); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestNormalize_NotImplemented(t *testing.T) {
	for _, fmt := range []string{"pdf", "screenshot", "mhtml", "a11y"} {
		if _, _, err := Normalize(fmt, []byte("x")); err == nil {
			t.Errorf("expected not-implemented error for %s, got nil", fmt)
		}
	}
}

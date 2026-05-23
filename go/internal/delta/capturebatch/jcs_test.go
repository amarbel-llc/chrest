package capturebatch

import "testing"

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"null", nil, `null`},
		{"bool-true", true, `true`},
		{"bool-false", false, `false`},
		{"int", 42, `42`},
		{"int64", int64(-7), `-7`},
		{"string-simple", "hello", `"hello"`},
		{"string-escape", "a\"b\\c\nd", `"a\"b\\c\nd"`},
		{"string-control", "\x01", `"\u0001"`},
		{"string-no-html-escape", "<a>&b", `"<a>&b"`}, // Go default escapes these; JCS must not.
		{"array-empty", []any{}, `[]`},
		{"array", []any{1, "x", true}, `[1,"x",true]`},
		{"object-empty", map[string]any{}, `{}`},
		{"object-sorted", map[string]any{"b": 1, "a": 2}, `{"a":2,"b":1}`},
		{"nested", map[string]any{
			"outer": map[string]any{"k": "v"},
			"list":  []any{map[string]any{"x": 1}},
		}, `{"list":[{"x":1}],"outer":{"k":"v"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Canonicalize(tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Canonicalize(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCanonicalize_UnsupportedType(t *testing.T) {
	if _, err := Canonicalize(3.14); err == nil {
		t.Fatal("expected error for float64; got nil")
	}
	if _, err := Canonicalize(struct{ X int }{1}); err == nil {
		t.Fatal("expected error for struct; got nil")
	}
}

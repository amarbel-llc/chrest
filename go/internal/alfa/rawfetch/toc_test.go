package rawfetch

import (
	"reflect"
	"testing"

	"code.linenisgreat.com/chrest/go/internal/0/markdown"
)

func TestExtractMarkdownTOCFromText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []markdown.Heading
	}{
		{
			"basic levels",
			"# A\n## B\n### C\n",
			[]markdown.Heading{
				{ID: "a", Text: "A", Level: 1},
				{ID: "b", Text: "B", Level: 2},
				{ID: "c", Text: "C", Level: 3},
			},
		},
		{
			"hash inside fenced code is ignored",
			"# Real\n```\n# Not a heading\n```\n## Also Real\n",
			[]markdown.Heading{
				{ID: "real", Text: "Real", Level: 1},
				{ID: "also-real", Text: "Also Real", Level: 2},
			},
		},
		{
			"trailing hashes (closed ATX) trimmed",
			"## Foo ##\n",
			[]markdown.Heading{
				{ID: "foo", Text: "Foo", Level: 2},
			},
		},
		{
			"slug collisions get -2, -3 suffixes",
			"# Foo\n## Foo\n",
			[]markdown.Heading{
				{ID: "foo", Text: "Foo", Level: 1},
				{ID: "foo-2", Text: "Foo", Level: 2},
			},
		},
		{
			"non-markdown text yields no headings",
			"this is just\nplain text\nwith no headings\n",
			nil,
		},
		{
			"info-string fence is recognised",
			"# Real\n```python\n# Not a heading\n```\n## Also Real\n",
			[]markdown.Heading{
				{ID: "real", Text: "Real", Level: 1},
				{ID: "also-real", Text: "Also Real", Level: 2},
			},
		},
		{
			"three-collision sequence yields -2 and -3",
			"# Foo\n## Foo\n### Foo\n",
			[]markdown.Heading{
				{ID: "foo", Text: "Foo", Level: 1},
				{ID: "foo-2", Text: "Foo", Level: 2},
				{ID: "foo-3", Text: "Foo", Level: 3},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractMarkdownTOCFromText([]byte(tc.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

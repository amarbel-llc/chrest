package rawfetch

import (
	"html"
	"mime"
	"path"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/charlie/markdown"
)

// Result contains the text/markdown/html slots and TOC for a raw-text
// response. All fields are owned by the caller.
type Result struct {
	Text     []byte
	Markdown []byte
	HTML     []byte
	TOC      []markdown.Heading
}

// BuildFromText populates the three web-fetch content slots and a TOC
// from a body that was already classified as ClassText.
func BuildFromText(body []byte, contentType, urlStr string) *Result {
	r := &Result{Text: body}

	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	ext := strings.ToLower(path.Ext(urlStr))

	isMarkdown := mt == "text/markdown" || mt == "text/x-markdown" || ext == ".md" || ext == ".markdown"

	if isMarkdown {
		r.Markdown = body
		// TOC is best-effort; we keep the markdown slot regardless of scan errors.
		toc, _ := ExtractMarkdownTOCFromText(body)
		r.TOC = toc
	} else {
		lang := languageHint(mt, ext)
		var b strings.Builder
		b.WriteString("```")
		b.WriteString(lang)
		b.WriteByte('\n')
		b.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
		r.Markdown = []byte(b.String())
	}

	escaped := html.EscapeString(string(body))
	var hb strings.Builder
	hb.Grow(len("<pre></pre>") + len(escaped))
	hb.WriteString("<pre>")
	hb.WriteString(escaped)
	hb.WriteString("</pre>")
	r.HTML = []byte(hb.String())

	return r
}

func languageHint(mt, ext string) string {
	switch ext {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".yaml", ".yml":
		return "yaml"
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".cpp":
		return "cpp"
	case ".h":
		return "c"
	case ".sh", ".bash":
		return "bash"
	}
	switch mt {
	case "application/json":
		return "json"
	case "application/xml":
		return "xml"
	case "application/x-yaml", "application/yaml":
		return "yaml"
	}
	return ""
}

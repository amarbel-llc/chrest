package rawfetch

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/0/markdown"
)

var (
	atxHeadingRE  = regexp.MustCompile(`^(#{1,6})\s+(.+?)(?:\s+#+)?\s*$`)
	slugReplaceRE = regexp.MustCompile(`[^a-z0-9]+`)
)

// ExtractMarkdownTOCFromText scans plain markdown text for ATX
// headings (# through ######), skipping lines inside fenced code
// blocks, and returns synthesized markdown.Heading entries with
// slugified ids. Suffix `-N` is appended on slug collisions.
func ExtractMarkdownTOCFromText(body []byte) ([]markdown.Heading, error) {
	var out []markdown.Heading
	seen := map[string]int{}
	inFence := false
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// Fence detection is intentionally simple: any line beginning with
		// ``` or ~~~ toggles the fence. Strict CommonMark would track the
		// opening fence's exact rune and length so a longer fence can
		// contain a shorter one (nested fences). The TOC is best-effort —
		// getting it wrong on documents that use nested fences yields a
		// noisy table of contents but does not break the dispatcher.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := atxHeadingRE.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		level := len(m[1])
		text := strings.TrimSpace(m[2])
		base := slugify(text)
		if base == "" {
			continue
		}
		id := base
		if n := seen[base]; n > 0 {
			id = fmt.Sprintf("%s-%d", base, n+1)
		}
		seen[base]++
		out = append(out, markdown.Heading{ID: id, Text: text, Level: level})
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("rawfetch: scan markdown body: %w", err)
	}
	return out, nil
}

func slugify(text string) string {
	s := strings.ToLower(text)
	s = slugReplaceRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

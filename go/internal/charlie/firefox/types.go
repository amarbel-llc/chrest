package firefox

// HTTPResponse describes the top-level document response captured
// during a Navigate. Populated via network.responseCompleted events
// from the BiDi backend. Per RFC 0001 §Envelope Artifact this becomes
// the envelope's `http.*` fields.
type HTTPResponse struct {
	URL      string       // final URL after redirects
	Status   int          // HTTP status code of the final response
	Headers  []HTTPHeader // preserves order and duplicates
	TimingMs int64        // responseEnd - fetchStart in ms; 0 if not measured
}

// HTTPHeader is a single response header. Represented as a name/value
// pair rather than a map so the envelope preserves both header order
// and duplicates (Set-Cookie commonly appears multiple times).
type HTTPHeader struct {
	Name  string
	Value string
}

// BrowserInfo is the identity surface of the live browser.
type BrowserInfo struct {
	Name        string
	Version     string
	UserAgent   string
	Platform    string
	JSEngine    string
	CommandLine []string
}

type PDFOptions struct {
	Landscape           bool     `json:"landscape,omitempty"`
	DisplayHeaderFooter bool     `json:"displayHeaderFooter,omitempty"`
	PrintBackground     bool     `json:"printBackground,omitempty"`
	PaperWidth          *float64 `json:"paperWidth,omitempty"`
	PaperHeight         *float64 `json:"paperHeight,omitempty"`
	MarginTop           *float64 `json:"marginTop,omitempty"`
	MarginBottom        *float64 `json:"marginBottom,omitempty"`
	MarginLeft          *float64 `json:"marginLeft,omitempty"`
	MarginRight         *float64 `json:"marginRight,omitempty"`
	PageRanges          string   `json:"pageRanges,omitempty"`
}

type ScreenshotOptions struct {
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
	FullPage bool   `json:"-"`
}

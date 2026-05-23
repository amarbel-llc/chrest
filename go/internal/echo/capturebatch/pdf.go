package capturebatch

import (
	"bytes"
	"fmt"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// pdfStripKeys is the list of /Info dictionary entries the normalizer
// removes when split=true. Per RFC 0001 §pdf:
//
//   - /CreationDate and /ModDate — timestamps of the generation run.
//   - /Producer — the PDF library + version that emitted the file.
//   - /Creator — the browser / UA that requested the PDF.
//
// /Title, /Subject, /Author, /Keywords are left in place: those are
// typically set by the document author, not by the browser or PDF
// library, so they represent real page content.
var pdfStripKeys = []string{
	"CreationDate",
	"ModDate",
	"Producer",
	"Creator",
}

// normalizePDF strips per-run metadata from a PDF per RFC 0001 §pdf.
// Uses pdfcpu to parse + rewrite the PDF, so the output is a valid PDF
// even though it's re-serialized (object stream layout, xref positions,
// and stream compression may differ from the input; content should be
// byte-stable across captures from the same chrest + browser build).
func normalizePDF(raw []byte) ([]byte, map[string]any, error) {
	rs := bytes.NewReader(raw)

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	ctx, err := pdfapi.ReadContext(rs, conf)
	if err != nil {
		return nil, nil, fmt.Errorf("pdf: read context: %w", err)
	}
	if err := pdfapi.ValidateContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("pdf: validate: %w", err)
	}
	if err := pdfapi.OptimizeContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("pdf: optimize: %w", err)
	}

	stripped := map[string]any{}

	// Trailer /ID — always two byte-string elements, the second varies
	// per document generation. Record and clear.
	if len(ctx.XRefTable.ID) > 0 {
		recorded := make([]any, 0, len(ctx.XRefTable.ID))
		for _, v := range ctx.XRefTable.ID {
			recorded = append(recorded, fmt.Sprintf("%v", v))
		}
		stripped["ID"] = recorded
		ctx.XRefTable.ID = nil
	}

	// /Info dict entries — resolve the info dict and remove the keys
	// whose values vary per run.
	if ctx.XRefTable.Info != nil {
		infoDict, err := ctx.XRefTable.DereferenceDict(*ctx.XRefTable.Info)
		if err == nil && infoDict != nil {
			for _, key := range pdfStripKeys {
				if v, ok := infoDict[key]; ok {
					stripped[key] = fmt.Sprintf("%v", v)
					delete(infoDict, key)
				}
			}
		}
	}

	// Also clear the mirror fields on the XRefTable — writeTrailer reads
	// from these in some branches.
	ctx.XRefTable.CreationDate = ""
	ctx.XRefTable.ModDate = ""
	ctx.XRefTable.Producer = ""
	ctx.XRefTable.Creator = ""

	var out bytes.Buffer
	if err := pdfapi.WriteContext(ctx, &out); err != nil {
		return nil, nil, fmt.Errorf("pdf: write: %w", err)
	}

	var strippedMap map[string]any
	if len(stripped) > 0 {
		strippedMap = map[string]any{"pdf": stripped}
	}
	return out.Bytes(), strippedMap, nil
}

// ensure types import is used (for future IDArray construction etc.)
var _ = types.Array{}

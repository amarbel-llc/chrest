package capturebatch

import "github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"

// chrest-binding node type-strings (RFC 0003). The protocol-defined types
// (identity, host, binary, …) are registered by capture_plugin itself;
// here we register the web binding's own types so their reference lines
// carry matching signatures and MediaTypeFor resolves them.
const (
	envType            = "jcs-chrest-capture-environment-v1"
	outcomeType        = "jcs-chrest-capture-outcome-v1"
	outcomeTypePreview = "jcs-chrest-capture-outcome-v1-preview"
	capType            = "jcs-chrest-capture-capabilities-v1"
)

func init() {
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    capture_plugin.ReceiptType(captureKind),
		IANAMediaType: "application/vnd.cutting-garden.capture-receipt-web+hyphence",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    envType,
		IANAMediaType: "application/vnd.chrest.capture-environment+jcs",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    outcomeType,
		IANAMediaType: "application/vnd.chrest.capture-outcome+jcs",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    outcomeTypePreview,
		IANAMediaType: "application/vnd.chrest.capture-outcome+jcs",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    capType,
		IANAMediaType: "application/vnd.chrest.capture-capabilities+jcs",
	})

	for format, mediaType := range payloadMediaTypes {
		capture_plugin.RegisterType(capture_plugin.TypeDef{
			TypeString:         payloadType(format),
			IANAMediaType:      mediaType,
			PayloadCardinality: "single",
		})
	}
}

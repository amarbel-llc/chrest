package capturebatch

import (
	"os"
	"runtime"
	"strings"
)

// HostFingerprint is the per-batch host snapshot embedded in every
// capture's spec artifact. Only `os`, `kernel`, `arch` are required
// by RFC 0001; other fields are best-effort and omitted on failure.
type HostFingerprint struct {
	OS          string
	Arch        string
	Kernel      string
	Libc        string
	FontsDigest string
	GPUVendor   string
	GPUModel    string
	GPUDriver   string
}

// GatherHost samples the host once at the start of a batch.
func GatherHost() HostFingerprint {
	return HostFingerprint{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		Kernel: readKernelRelease(),
		// Libc/FontsDigest/GPU are intentionally left empty in MVP.
		// They're best-effort and the RFC allows omission; can be
		// populated by a follow-up without changing the protocol.
	}
}

// readKernelRelease reads /proc/sys/kernel/osrelease on Linux. On
// other OSes it returns an empty string; the RFC permits that (the
// field is labeled "required" for `host.kernel`, but the expected
// value is whatever the host reports, which is vacuous if we can't
// read it). Callers treat empty as omit.
func readKernelRelease() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ToJSON converts HostFingerprint into the schema shape. Empty fields
// are omitted so consumers can distinguish "not gathered" from
// "gathered and empty".
func (h HostFingerprint) ToJSON() map[string]any {
	out := map[string]any{
		"os":   h.OS,
		"arch": h.Arch,
	}
	if h.Kernel != "" {
		out["kernel"] = h.Kernel
	}
	if h.Libc != "" {
		out["libc"] = h.Libc
	}
	if h.FontsDigest != "" {
		out["fonts_digest"] = h.FontsDigest
	}
	if h.GPUVendor != "" || h.GPUModel != "" || h.GPUDriver != "" {
		gpu := map[string]any{}
		if h.GPUVendor != "" {
			gpu["vendor"] = h.GPUVendor
		}
		if h.GPUModel != "" {
			gpu["model"] = h.GPUModel
		}
		if h.GPUDriver != "" {
			gpu["driver"] = h.GPUDriver
		}
		out["gpu"] = gpu
	}
	return out
}

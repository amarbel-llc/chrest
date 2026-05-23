// chrest-jcs is a minimal CLI that reads a JSON document from stdin
// and writes its JCS (RFC 8785) canonicalization to stdout. Intended
// for byte-stability cross-checks against other implementations of
// RFC 0001 §Capture Spec Artifact / §Envelope Artifact.
//
// Example:
//
//	chrest-jcs < spec-vector.input.json | sha256sum
//
// Uses the same Canonicalize routine the capture-batch runner uses
// to produce spec and envelope bytes, so a byte-identical match
// proves the hash output will match in production too.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/chrest/go/internal/echo/capturebatch"
)

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chrest-jcs: read stdin: %v\n", err)
		os.Exit(1)
	}

	// UseNumber so integer precision is preserved — our schema has no
	// floats, and Canonicalize rejects float64 anyway.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		fmt.Fprintf(os.Stderr, "chrest-jcs: parse json: %v\n", err)
		os.Exit(1)
	}

	out, err := capturebatch.Canonicalize(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chrest-jcs: canonicalize: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "chrest-jcs: write stdout: %v\n", err)
		os.Exit(1)
	}
}

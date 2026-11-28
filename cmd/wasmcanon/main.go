// Command wasmcanon is the thin command-line interface to pkg/wasmcanon (O-1 INT-04): it lets a release producer that
// is not written in Go (the Python release tooling) obtain the executed representation of a wasm module from the SAME
// implementation the relay admits with, instead of carrying a second canonicalisation.
//
//	wasmcanon exec   < raw.wasm > executed.wasm   the executed bytes (wasm-canon/v1)
//	wasmcanon digest < raw.wasm                   sha256:<hex> of the executed bytes (executed_wasm_sha256)
//	wasmcanon version                             transform id and the pinned wabin version
//
// It reads stdin, writes stdout, and exits non-zero with a one-line message on stderr when the module is refused.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"centralrelay/pkg/wasmcanon"
)

// maxInput bounds what is read, the same ceiling as core/cabinet.MaxWASMModuleSize.
const maxInput = 16777216

// run executes one command; it is separate from main so that it can be tested.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: wasmcanon exec|digest|version")
		return 2
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "%s wabin=%s\n", wasmcanon.TransformID, wabinVersion())
		return 0
	}
	if args[0] != "exec" && args[0] != "digest" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, maxInput+1))
	if err != nil {
		fmt.Fprintf(stderr, "read: %v\n", err)
		return 1
	}
	if len(raw) > maxInput {
		fmt.Fprintln(stderr, "the module exceeds the size limit")
		return 1
	}
	exec, err := wasmcanon.Canonicalize(raw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if args[0] == "digest" {
		fmt.Fprintln(stdout, wasmcanon.Digest(exec))
		return 0
	}
	if _, err := stdout.Write(exec); err != nil {
		fmt.Fprintf(stderr, "write: %v\n", err)
		return 1
	}
	return 0
}

// wabinVersion is the wabin version this binary was built with.
func wabinVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/tetratelabs/wabin" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// main runs the command against the process streams.
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

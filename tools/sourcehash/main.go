// Command sourcehash prints the deterministic source tree hash for a directory.
//
// Usage: sourcehash [rootPath]
//
// rootPath defaults to the current working directory.
// Output is a 64-char lowercase SHA-256 hex string with no trailing newline,
// suitable for direct use as an ldflag value:
//
//	go build -ldflags "-X main.SourceTreeHash=$(go run ./tools/sourcehash .)" ...
package main

import (
	"fmt"
	"os"

	"centralrelay/pkg/sourcedigest"
)

var osExit = os.Exit

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	hash, err := sourcedigest.ComputeTreeHash(root, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sourcehash: %v\n", err)
		osExit(1)
		return
	}
	fmt.Print(hash)
}

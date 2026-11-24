// Command cidag builds and verifies the Merkle DAG of a CI process (docs/hu/CI-MERKLE-DAG-CONTRACT.md).
//
//	cidag build  [FILE]            {"template":..., "evidence":...}  ->  the bundle (objects, roots, CI root)
//	cidag verify [FILE]            a bundle  ->  "OK <ci_root>", or the refusal and its class (exit 1)
//	cidag template WORKFLOW [ID]   the template of a GitHub Actions workflow file (jobs and `needs`)
//
// FILE is a path, or "-" or nothing for standard input. Input is read by the strict reader (a duplicate member
// name, a non-integer number or a nesting deeper than 64 is refused). Exit status: 0 success, 1 refusal of the
// content, 2 usage or I/O error. It reads and computes only: it signs nothing, publishes nothing, writes no file.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"centralrelay/pkg/cidag"
)

// RunFunc is the entry point; tests replace it (the pattern of tools/canonicalize).
var RunFunc = Run

// osExit is replaceable in tests so that main can be exercised without ending the process.
var osExit = os.Exit

// main runs RunFunc with the process arguments and exits with its status.
func main() {
	osExit(RunFunc(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// usage is printed for a missing or unknown command.
const usage = "usage: cidag build [FILE] | cidag verify [FILE] | cidag template WORKFLOW [ID]\n"

// Run executes one command and returns the exit status.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:], stdin, stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdin, stdout, stderr)
	case "template":
		return runTemplate(args[1:], stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return 2
}

// readInput reads the named file, or standard input for no argument or "-".
func readInput(args []string, stdin io.Reader) ([]byte, error) {
	if len(args) > 1 {
		return nil, errors.New("too many arguments")
	}
	if len(args) == 0 || args[0] == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(args[0])
}

// refuse prints a refusal of the content (exit 1) or any other error (exit 2).
func refuse(stderr io.Writer, err error) int {
	var e *cidag.Error
	if errors.As(err, &e) {
		fmt.Fprintf(stderr, "REFUSED %s: %s\n", e.Kind, e.Detail)
		return 1
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	return 2
}

// runBuild reads {template, evidence} and prints the bundle with the step roots and the CI root.
func runBuild(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	data, err := readInput(args, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	doc, err := cidag.ParseStrict(data)
	if err != nil {
		return refuse(stderr, err)
	}
	m, ok := doc.(map[string]any)
	if !ok || len(m) != 2 || m["template"] == nil || m["evidence"] == nil {
		return refuse(stderr, &cidag.Error{Kind: cidag.KindBundleShape, Detail: `input must be {"template":..., "evidence":...}`})
	}
	g, err := cidag.Build(m["template"], m["evidence"])
	if err != nil {
		return refuse(stderr, err)
	}
	objs := map[string]any{}
	for id, o := range g.Objects {
		objs[id] = o
	}
	roots := map[string]any{}
	for id, r := range g.StepRoots {
		roots[id] = r
	}
	out, err := cidag.Canonical(map[string]any{"template": m["template"], "objects": objs, "step_roots": roots, "ci_root": g.CIRoot})
	if err != nil {
		return refuse(stderr, err)
	}
	fmt.Fprintf(stdout, "%s\n", out)
	return 0
}

// runVerify recomputes a published bundle and prints its CI root, or the refusal.
func runVerify(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	data, err := readInput(args, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	doc, err := cidag.ParseStrict(data)
	if err != nil {
		return refuse(stderr, err)
	}
	g, err := cidag.Verify(doc)
	if err != nil {
		return refuse(stderr, err)
	}
	fmt.Fprintf(stdout, "OK %s\n", g.CIRoot)
	return 0
}

// runTemplate prints the template of a GitHub Actions workflow file.
func runTemplate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || len(args) > 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	id := filepath.Base(args[0])
	if ext := filepath.Ext(id); ext != "" {
		id = id[:len(id)-len(ext)]
	}
	if len(args) == 2 {
		id = args[1]
	}
	tpl, err := cidag.TemplateFromWorkflow(data, id)
	if err != nil {
		return refuse(stderr, err)
	}
	out, err := cidag.Canonical(tpl)
	if err != nil {
		return refuse(stderr, err)
	}
	fmt.Fprintf(stdout, "%s\n", out)
	return 0
}

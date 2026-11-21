// objinventory lists the top-level functions and methods of Go source files with
// their exact source spans, as JSON. It is the Go side of the object inventory of
// scripts/object_model.py (object-id/1): the Python side decides what an object is
// called and how coverage blocks are attributed, this tool only reports what the
// syntax tree says.
//
// Usage: objinventory <file.go> [<file.go> ...]
//
// Only the standard library is used and nothing is type checked. A function literal
// is not an object of its own: it belongs to the top-level declaration whose span
// contains it (the documented attribution rule), so only FuncDecl spans are listed.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"
)

// Pos is a source position. Line and column are 1-based, as in a Go cover profile.
type Pos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Func is one top-level function or method.
type Func struct {
	Name string `json:"name"`
	Recv string `json:"recv,omitempty"` // receiver base type, without * or type parameters
	Kind string `json:"kind"`           // func | method | init
	// Ord is the 1-based ordinal among the `init` functions of the file (0 otherwise).
	Ord   int `json:"ord,omitempty"`
	Start Pos `json:"start"`
	End   Pos `json:"end"`
}

// File is the result for one input file.
type File struct {
	File    string `json:"file"`
	Package string `json:"package"`
	// Build is the //go:build constraint of the file, "" if it has none.
	Build string `json:"build,omitempty"`
	Funcs []Func `json:"funcs"`
	Error string `json:"error,omitempty"`
}

// Dump is the whole output.
type Dump struct {
	Schema string `json:"schema"`
	Files  []File `json:"files"`
}

// recvName returns the base type name of a receiver expression: T, *T, T[K], *T[K].
func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.ParenExpr:
		return recvName(x.X)
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// buildConstraint returns the //go:build line that precedes the package clause.
func buildConstraint(f *ast.File) string {
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			if rest, ok := strings.CutPrefix(c.Text, "//go:build "); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// dumpFile parses one Go file and lists its function and method declarations with their spans; init functions
// are numbered in source order. A file that does not parse is reported with its error and no functions.
func dumpFile(path string) File {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return File{File: path, Funcs: []Func{}, Error: err.Error()}
	}
	out := File{File: path, Package: f.Name.Name, Build: buildConstraint(f), Funcs: []Func{}}
	inits := 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		s, e := fset.Position(fd.Pos()), fset.Position(fd.End())
		fn := Func{
			Name:  fd.Name.Name,
			Kind:  "func",
			Start: Pos{s.Line, s.Column},
			End:   Pos{e.Line, e.Column},
		}
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			fn.Kind, fn.Recv = "method", recvName(fd.Recv.List[0].Type)
		} else if fd.Name.Name == "init" && fd.Recv == nil {
			inits++
			fn.Kind, fn.Ord = "init", inits
		}
		out.Funcs = append(out.Funcs, fn)
	}
	return out
}

// run is main without the process: it returns the exit status.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: objinventory <file.go> [<file.go> ...]")
		return 2
	}
	d := Dump{Schema: "objinventory/1", Files: []File{}}
	failed := false
	for _, p := range args {
		f := dumpFile(p)
		failed = failed || f.Error != ""
		d.Files = append(d.Files, f)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(d); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if failed { // a file that cannot be parsed is reported in the output AND fails the run
		return 1
	}
	return 0
}

// main runs the tool on the command-line arguments and exits with run's status.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

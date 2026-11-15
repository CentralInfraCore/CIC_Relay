package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const sample = `//go:build linux && !nocgo

package demo

type T struct{}
type G[K comparable] struct{}

var hook = func() int { return 1 } // outside any FuncDecl

func Top(a int) int {
	f := func() int { return a } // a literal belongs to Top
	return f()
}

func (t *T) Ptr() {}
func (t T) Val()  {}
func (g *G[K]) Gen() {}

func init() {}
func init() {}
`

func write(t *testing.T, name, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDumpFile(t *testing.T) {
	f := dumpFile(write(t, "a.go", sample))
	if f.Error != "" || f.Package != "demo" || f.Build != "linux && !nocgo" {
		t.Fatalf("file header wrong: %+v", f)
	}
	type want struct {
		name, recv, kind string
		ord              int
	}
	exp := []want{
		{"Top", "", "func", 0},
		{"Ptr", "T", "method", 0},
		{"Val", "T", "method", 0},
		{"Gen", "G", "method", 0},
		{"init", "", "init", 1},
		{"init", "", "init", 2},
	}
	if len(f.Funcs) != len(exp) {
		t.Fatalf("got %d funcs, want %d: %+v", len(f.Funcs), len(exp), f.Funcs)
	}
	for i, w := range exp {
		g := f.Funcs[i]
		if g.Name != w.name || g.Recv != w.recv || g.Kind != w.kind || g.Ord != w.ord {
			t.Errorf("func %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestSpansAreExact(t *testing.T) {
	f := dumpFile(write(t, "a.go", sample))
	top := f.Funcs[0]
	// `func Top` is on line 10 of the sample and closes on line 13
	if top.Start != (Pos{10, 1}) || top.End != (Pos{13, 2}) {
		t.Errorf("Top span = %+v..%+v", top.Start, top.End)
	}
}

func TestNoBuildConstraint(t *testing.T) {
	f := dumpFile(write(t, "b.go", "package b\n\nfunc F() {}\n"))
	if f.Build != "" || len(f.Funcs) != 1 {
		t.Errorf("unexpected: %+v", f)
	}
}

func TestParseErrorIsReported(t *testing.T) {
	f := dumpFile(write(t, "c.go", "package c\n\nfunc (\n"))
	if f.Error == "" || f.Funcs == nil || len(f.Funcs) != 0 {
		t.Errorf("a parse error must be reported with an empty (non-nil) list: %+v", f)
	}
}

func TestMissingFileIsReported(t *testing.T) {
	if f := dumpFile(filepath.Join(t.TempDir(), "none.go")); f.Error == "" {
		t.Error("a missing file must be an error")
	}
}

func TestRecvName(t *testing.T) {
	f := dumpFile(write(t, "d.go", "package d\n\ntype T[K any] struct{}\n\nfunc (t *T[K]) A() {}\nfunc (T[K]) B() {}\n"))
	for _, fn := range f.Funcs {
		if fn.Recv != "T" {
			t.Errorf("%s: recv = %q, want T", fn.Name, fn.Recv)
		}
	}
}

func TestRunExitStatusAndOutput(t *testing.T) {
	good := write(t, "g.go", "package g\n\nfunc F() {}\n")
	bad := write(t, "b.go", "package b\n\nfunc (\n")

	var out, errw bytes.Buffer
	if rc := run(nil, &out, &errw); rc != 2 || errw.Len() == 0 || out.Len() != 0 {
		t.Errorf("no arguments: rc=%d out=%q err=%q", rc, out.String(), errw.String())
	}

	out.Reset()
	errw.Reset()
	if rc := run([]string{good}, &out, &errw); rc != 0 {
		t.Fatalf("a good file must succeed, rc=%d err=%q", rc, errw.String())
	}
	var d Dump
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.Schema != "objinventory/1" || len(d.Files) != 1 {
		t.Errorf("unexpected output %q (%v)", out.String(), err)
	}

	out.Reset()
	if rc := run([]string{good, bad}, &out, &errw); rc != 1 {
		t.Errorf("a file that cannot be parsed must fail the run, rc=%d", rc)
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || len(d.Files) != 2 || d.Files[1].Error == "" {
		t.Errorf("the failing file must still be reported in the output: %q", out.String())
	}
}

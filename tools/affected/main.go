package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

var (
	// fatalf is a variable that holds the log.Fatalf function.
	// It can be replaced in tests to prevent actual program exit.
	fatalf = log.Fatalf
)

// affectedPackages loads every package in the module rooted at cfg.Dir and
// returns the import paths of the packages directly containing a changed
// file, plus every package that transitively imports one of those packages.
// This is the set of packages whose tests can be affected by changedFiles.
func affectedPackages(cfg *packages.Config, changedFiles []string) ([]string, error) {
	cfg.Mode = packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return nil, fmt.Errorf("failed to load packages: %d error(s)", n)
	}

	dirToPkg := make(map[string]string)
	validPkg := make(map[string]bool)
	for _, pkg := range pkgs {
		validPkg[pkg.PkgPath] = true
		for _, f := range pkg.GoFiles {
			dirToPkg[filepath.Dir(f)] = pkg.PkgPath
		}
	}

	// reverse[X] holds every package that directly imports X.
	reverse := make(map[string][]string)
	for _, pkg := range pkgs {
		for importPath := range pkg.Imports {
			if validPkg[importPath] {
				reverse[importPath] = append(reverse[importPath], pkg.PkgPath)
			}
		}
	}

	changed := make(map[string]bool)
	for _, f := range changedFiles {
		f = strings.TrimSpace(f)
		if f == "" || !strings.HasSuffix(f, ".go") {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(cfg.Dir, f))
		if err != nil {
			continue
		}
		if pkgPath, ok := dirToPkg[filepath.Dir(abs)]; ok {
			changed[pkgPath] = true
		}
	}

	affected := make(map[string]bool, len(changed))
	queue := make([]string, 0, len(changed))
	for p := range changed {
		affected[p] = true
		queue = append(queue, p)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[p] {
			if !affected[dependent] {
				affected[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}

	result := make([]string, 0, len(affected))
	for p := range affected {
		result = append(result, p)
	}
	sort.Strings(result)
	return result, nil
}

func main() {
	var changedFiles []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		changedFiles = append(changedFiles, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		fatalf("failed to read stdin: %v", err)
		return
	}

	result, err := affectedPackages(&packages.Config{}, changedFiles)
	if err != nil {
		fatalf("Error: %v", err)
		return
	}

	for _, p := range result {
		fmt.Println(p)
	}
}

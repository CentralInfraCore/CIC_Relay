package main

import (
	"bytes"
	"context"
	"fmt"
	"go/doc"
	"go/token"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

var (
	allowFile  = "context/SYMBOLS.ALLOWPKGS"
	outputFile = "context/SYMBOLS.md"

	// exit is a variable that holds the os.Exit function.
	// It can be replaced in tests to prevent actual program exit.
	exit = os.Exit

	// fatalf is a variable that holds the log.Fatalf function.
	// It can be replaced in tests to prevent actual program exit.
	fatalf = log.Fatalf
)

// generateSymbols loads and processes Go packages based on the provided patterns
// and returns the generated symbols documentation as a byte slice.
func generateSymbols(pkgPatterns []string) ([]byte, error) {
	// If no package patterns are provided, return nil output and no error.
	if len(pkgPatterns) == 0 {
		return nil, nil
	}

	// Create a context with a timeout for package loading.
	// This prevents packages.Load from hanging indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // 30-second timeout
	defer cancel() // Ensure the context is cancelled to release resources

	// 2. Load the packages, including their syntax trees
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes,
		Fset: token.NewFileSet(),
		Context: ctx, // Assign the context with timeout
	}
	pkgs, err := packages.Load(cfg, pkgPatterns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}

	// Collect all errors from package loading and doc generation
	var allErrs []error

	// Buffer to store generated content for each package
	var packageContentBuffers []bytes.Buffer // Changed to a slice of buffers

	// 3. Process each package
	for _, pkg := range pkgs {
		// Collect errors from packages.Load
		if len(pkg.Errors) > 0 {
			for _, pkgErr := range pkg.Errors {
				allErrs = append(allErrs, fmt.Errorf("package %s: %w", pkg.ID, pkgErr))
			}
			continue // Skip processing this package if there were load errors
		}

		var currentPkgOutput bytes.Buffer // Buffer for the current package's symbols
		// Use the AST provided by the packages.Load call
		p, err := doc.NewFromFiles(pkg.Fset, pkg.Syntax, pkg.PkgPath)
		if err != nil {
			// This error indicates a fundamental issue with doc generation, e.g., no valid Go files.
			allErrs = append(allErrs, fmt.Errorf("failed to create doc package for %s: %w", pkg.PkgPath, err))
			continue
		}

		// Process and write constants
		if len(p.Consts) > 0 {
			currentPkgOutput.WriteString(fmt.Sprintf("## Package: `%s`\n\n", pkg.PkgPath))
			currentPkgOutput.WriteString("### Constants\n\n")
			sort.Slice(p.Consts, func(i, j int) bool { return p.Consts[i].Names[0] < p.Consts[j].Names[0] })
			for _, c := range p.Consts {
				for _, name := range c.Names {
					currentPkgOutput.WriteString(fmt.Sprintf("- `const %s`\n", name))
				}
			}
			currentPkgOutput.WriteString("\n")
		}

		// Process and write variables
		if len(p.Vars) > 0 {
			if len(p.Consts) == 0 { // Only write package header if not already written by constants
				currentPkgOutput.WriteString(fmt.Sprintf("## Package: `%s`\n\n", pkg.PkgPath))
			}
			currentPkgOutput.WriteString("### Variables\n\n")
			sort.Slice(p.Vars, func(i, j int) bool { return p.Vars[i].Names[0] < p.Vars[j].Names[0] })
			for _, v := range p.Vars {
				for _, name := range v.Names {
					currentPkgOutput.WriteString(fmt.Sprintf("- `var %s`\n", name))
				}
			}
			currentPkgOutput.WriteString("\n")
		}

		// Process and write functions
		if len(p.Funcs) > 0 {
			if len(p.Consts) == 0 && len(p.Vars) == 0 { // Only write package header if not already written
				currentPkgOutput.WriteString(fmt.Sprintf("## Package: `%s`\n\n", pkg.PkgPath))
			}
			currentPkgOutput.WriteString("### Functions\n\n")
			sort.Slice(p.Funcs, func(i, j int) bool { return p.Funcs[i].Name < p.Funcs[j].Name })
			for _, f := range p.Funcs {
				currentPkgOutput.WriteString(fmt.Sprintf("- `func %s`\n", f.Name)) // Simplified for now
			}
			currentPkgOutput.WriteString("\n")
		}

		// Process and write types
		if len(p.Types) > 0 {
			if len(p.Consts) == 0 && len(p.Vars) == 0 && len(p.Funcs) == 0 { // Only write package header if not already written
				currentPkgOutput.WriteString(fmt.Sprintf("## Package: `%s`\n\n", pkg.PkgPath))
			}
			currentPkgOutput.WriteString("### Types\n\n")
			sort.Slice(p.Types, func(i, j int) bool { return p.Types[i].Name < p.Types[j].Name })
			for _, t := range p.Types {
				currentPkgOutput.WriteString(fmt.Sprintf("- `type %s`\n", t.Name))
			}
			currentPkgOutput.WriteString("\n")
		}
		packageContentBuffers = append(packageContentBuffers, currentPkgOutput)
	}

	// If any errors were collected, return them.
	if len(allErrs) > 0 {
		return nil, fmt.Errorf("multiple errors encountered during symbol generation: %v", allErrs)
	}

	// If no symbols were generated across all packages, return nil output.
	if len(packageContentBuffers) == 0 {
		return nil, nil
	}

	// Assemble final output with main header
	var finalOutput bytes.Buffer
	finalOutput.WriteString("# Public Symbols\n\n")
	finalOutput.WriteString("_*This file is automatically generated. Do not edit manually.*_\n\n")
	for _, contentBuffer := range packageContentBuffers {
		finalOutput.Write(contentBuffer.Bytes())
	}

	return finalOutput.Bytes(), nil
}

// run contains the main logic of the symbolsgen tool, allowing it to be tested.
func run(allowFilePath, outputFilePath string) error {
	// 1. Read the allowlist of packages
	allowlistContent, err := os.ReadFile(allowFilePath)
	if err != nil {
		return fmt.Errorf("failed to read allowlist file %s: %w", allowFilePath, err)
	}

	pkgPatterns := strings.Split(strings.TrimSpace(string(allowlistContent)), "\n")

	// Generate the symbol documentation
	output, err := generateSymbols(pkgPatterns)
	
	if err != nil {
		return fmt.Errorf("failed to generate symbols: %w", err)
	}

	// If generateSymbols returned nil output (e.g., no symbols found), don't write file.
	if output == nil {
		return nil // Treat as success if no symbols to generate
	}

	// 4. Write the output to the file
	if err := os.WriteFile(outputFilePath, output, 0644); err != nil {
		return fmt.Errorf("failed to write to %s: %w", outputFilePath, err)
	}

	fmt.Printf("Successfully generated %s\n", outputFilePath) // Changed to fmt.Printf for stdout
	return nil
}

func main() {
	// Handle command-line arguments
	if len(os.Args) != 3 {
		log.Printf("Usage: %s <allowlist_file> <output_file>\n", os.Args[0]) // Changed to log.Printf for stderr
		exit(1)
	}

	allowFilePath := os.Args[1]
	outputFilePath := os.Args[2]

	// The run function will handle the actual file operations.
	if err := run(allowFilePath, outputFilePath); err != nil {
		fatalf("Error: %v", err)
	}
}

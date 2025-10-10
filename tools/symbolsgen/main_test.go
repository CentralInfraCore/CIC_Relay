package main

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper function to create a temporary file with content
func createTempFile(t *testing.T, content string) string {
	tmpfile, err := ioutil.TempFile("", "testfile")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := tmpfile.WriteString(content); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}
	return tmpfile.Name()
}

// Helper function to read file content
func readFileContent(t *testing.T, path string) string {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	return string(content)
}

func TestRun_Success(t *testing.T) {
	// Mock os.Exit and log.Fatalf to prevent actual program exit
	oldExit := exit
	oldFatalf := fatalf
	defer func() {
		exit = oldExit
		fatalf = oldFatalf
	}()

	exitCalled := false
	exit = func(code int) {
		exitCalled = true
	}
	fatalfCalled := false
	fatalf = func(format string, v ...interface{}) {
		fatalfCalled = true
		log.Printf(format, v...) // Log the error for visibility in test output
	}

	// Create a temporary allowlist file
	allowlistContent := "fmt" // Use a standard library package for testing
	allowFilePath := createTempFile(t, allowlistContent)
	defer os.Remove(allowFilePath)

	// Create a temporary output file path
	outputFilePath := filepath.Join(os.TempDir(), "SYMBOLS.md")
	defer os.Remove(outputFilePath)

	err := run(allowFilePath, outputFilePath)
	if err != nil {
		t.Errorf("run() failed with error: %v", err)
	}

	if exitCalled {
		t.Error("os.Exit was called unexpectedly")
	}
	if fatalfCalled {
		t.Error("log.Fatalf was called unexpectedly")
	}

	// Verify output file content
	outputContent := readFileContent(t, outputFilePath)
	if !strings.Contains(outputContent, "# Public Symbols") {
		t.Errorf("output file content missing expected header")
	}
	if !strings.Contains(outputContent, "## Package: `fmt`") {
		t.Errorf("output file content missing expected package header for 'fmt'")
	}
	if !strings.Contains(outputContent, "- `func Printf`") {
		t.Errorf("output file content missing expected function for 'fmt.Printf'")
	}
}

func TestRun_AllowlistNotFound(t *testing.T) {
	// Mock os.Exit and log.Fatalf
	oldExit := exit
	oldFatalf := fatalf
	defer func() {
		exit = oldExit
		fatalf = oldFatalf
	}()

	exitCalled := false
	exit = func(code int) {
		exitCalled = true
	}
	fatalfCalled := false
	fatalf = func(format string, v ...interface{}) {
		fatalfCalled = true
		log.Printf(format, v...)
	}

	nonExistentAllowFilePath := filepath.Join(os.TempDir(), "nonexistent_allowlist.txt")
	outputFilePath := filepath.Join(os.TempDir(), "SYMBOLS.md")
	defer os.Remove(outputFilePath) // Ensure no file is left behind if created

	err := run(nonExistentAllowFilePath, outputFilePath)
	if err == nil {
		t.Error("run() did not return an error for non-existent allowlist file")
	}
	if !strings.Contains(err.Error(), "failed to read allowlist file") {
		t.Errorf("expected error message to contain 'failed to read allowlist file', got: %v", err)
	}

	if exitCalled {
		t.Error("os.Exit was called unexpectedly")
	}
	if fatalfCalled {
		t.Error("log.Fatalf was called unexpectedly")
	}
}

func TestRun_GenerateSymbolsError(t *testing.T) {
	// Mock os.Exit and log.Fatalf
	oldExit := exit
	oldFatalf := fatalf
	defer func() {
		exit = oldExit
		fatalf = oldFatalf
	}()

	exitCalled := false
	exit = func(code int) {
		exitCalled = true
	}
	fatalfCalled := false
	fatalf = func(format string, v ...interface{}) {
		fatalfCalled = true
		log.Printf(format, v...)
	}

	// Create an allowlist file with an invalid package pattern to force generateSymbols to error
	allowlistContent := "nonexistent/package/xyz"
	allowFilePath := createTempFile(t, allowlistContent)
	defer os.Remove(allowFilePath)

	outputFilePath := filepath.Join(os.TempDir(), "SYMBOLS.md")
	defer os.Remove(outputFilePath)

	err := run(allowFilePath, outputFilePath)
	if err == nil {
		t.Error("run() did not return an error for generateSymbols failure")
	}
	if !strings.Contains(err.Error(), "failed to generate symbols") {
		t.Errorf("expected error message to contain 'failed to generate symbols', got: %v", err)
	}

	if exitCalled {
		t.Error("os.Exit was called unexpectedly")
	}
	if fatalfCalled {
		t.Error("log.Fatalf was called unexpectedly")
	}
}

func TestGenerateSymbols_Success(t *testing.T) {
	pkgPatterns := []string{"fmt"} // Use a standard library package

	output, err := generateSymbols(pkgPatterns)
	if err != nil {
		t.Fatalf("generateSymbols() failed with error: %v", err)
	}
	if output == nil {
		t.Fatal("generateSymbols() returned nil output for valid package")
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "# Public Symbols") {
		t.Errorf("output missing expected header")
	}
	if !strings.Contains(outputStr, "## Package: `fmt`") {
		t.Errorf("output missing expected package header for 'fmt'")
	}
	if !strings.Contains(outputStr, "- `func Printf`") {
		t.Errorf("output missing expected function for 'fmt.Printf'")
	}
	// Add more assertions for other symbol types if desired
}

func TestGenerateSymbols_NoPackages(t *testing.T) {
	pkgPatterns := []string{} // No packages

	output, err := generateSymbols(pkgPatterns)
	if err != nil {
		t.Fatalf("generateSymbols() failed with error: %v", err)
	}
	if output != nil {
		t.Errorf("generateSymbols() returned non-nil output for no packages: %s", string(output))
	}
}

func TestGenerateSymbols_PackageLoadError(t *testing.T) {
	pkgPatterns := []string{"nonexistent/package/xyz"} // Invalid package pattern

	output, err := generateSymbols(pkgPatterns)
	if err == nil {
		t.Error("generateSymbols() did not return an error for invalid package pattern")
	}
	if output != nil {
		t.Errorf("generateSymbols() returned non-nil output for invalid package pattern: %s", string(output))
	}
	if !strings.Contains(err.Error(), "multiple errors encountered during symbol generation") || !strings.Contains(err.Error(), "package nonexistent/package/xyz") {
		t.Errorf("expected error message to contain 'multiple errors encountered during symbol generation' and 'package nonexistent/package/xyz', got: %v", err)
	}
}

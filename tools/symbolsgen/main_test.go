package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/tools/go/packages"
)

// Helper function to create a temporary file with content
func createTempFile(t *testing.T, content string) string {
	tmpfile, err := os.CreateTemp("", "testfile")
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

// Helper function to create a temporary Go package directory and file
func createTempGoPackage(t *testing.T, pkgName, fileName, content string) (string, string) {
	tmpDir, err := os.MkdirTemp("", "testpkg")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create go.mod file
	modulePath := "example.com/testmodule_" + pkgName
	goModContent := "module " + modulePath + "\n\ngo 1.18\n"
	goModPath := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(goModContent), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	pkgPath := filepath.Join(tmpDir, pkgName)
	if err := os.Mkdir(pkgPath, 0755); err != nil {
		t.Fatalf("failed to create package dir: %v", err)
	}

	filePath := filepath.Join(pkgPath, fileName)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write package file: %v", err)
	}
	return tmpDir, modulePath + "/" + pkgName // Return the base temp dir and the module path to the package
}

// Helper function to read file content
func readFileContent(t *testing.T, path string) string {
	content, err := os.ReadFile(path)
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
		log.Printf(format, v...)
	}

	nonExistentAllowFilePath := filepath.Join(os.TempDir(), "nonexistent_allowlist.txt")
	outputFilePath := filepath.Join(os.TempDir(), "SYMBOLS.md")
	defer os.Remove(outputFilePath)

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
	cfg := &packages.Config{}      // Default config
	pkgPatterns := []string{"fmt"} // Use a standard library package

	output, err := generateSymbols(cfg, pkgPatterns)
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
	cfg := &packages.Config{} // Default config
	pkgPatterns := []string{} // No packages

	output, err := generateSymbols(cfg, pkgPatterns)
	if err != nil {
		t.Fatalf("generateSymbols() failed with error: %v", err)
	}
	if output != nil { // FIX: Should be output == nil
		t.Errorf("generateSymbols() returned non-nil output for no packages: %s", string(output))
	}
}

func TestGenerateSymbols_PackageLoadError(t *testing.T) {
	cfg := &packages.Config{}                          // Default config
	pkgPatterns := []string{"nonexistent/package/xyz"} // Invalid package pattern

	output, err := generateSymbols(cfg, pkgPatterns)
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

func TestGenerateSymbols_PackageWithErrors(t *testing.T) {
	pkgContent := `package mypackage

const MyConst = "hello"

var MyVar = 123

func MyFunc() string {
	return "world"
}

func MyBadFunc() string {
	return "bad" // Missing closing brace
` // Syntax error here

	baseDir, modulePath := createTempGoPackage(t, "mypackage_with_errors", "mypackage_with_errors.go", pkgContent)
	defer os.RemoveAll(baseDir) // Clean up the temporary directory

	cfg := &packages.Config{Dir: baseDir} // Set the working directory for packages.Load

	// Call generateSymbols with the custom package
	output, err := generateSymbols(cfg, []string{modulePath})
	if err == nil {
		t.Error("generateSymbols() did not return an error for package with errors")
	}
	if output != nil {
		t.Errorf("generateSymbols() returned non-nil output for package with errors: %s", string(output))
	}

	// Check for expected error message parts
	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "multiple errors encountered during symbol generation") {
		t.Errorf("expected error message to contain 'multiple errors encountered during symbol generation', got: %v", errorMsg)
	}
	if !strings.Contains(errorMsg, "mypackage_with_errors.go") || !strings.Contains(errorMsg, "expected '}'") {
		t.Errorf("expected error message to contain syntax error details, got: %v", errorMsg)
	}
}

func TestGenerateSymbols_CustomPackage(t *testing.T) {
	pkgContent := `package mypackage

const MyConst = "hello"

var MyVar = 123

func MyFunc() string {
	return "world"
}

type MyStruct struct {
	Field1 string
	Field2 int
}
`

	baseDir, modulePath := createTempGoPackage(t, "mypackage", "mypackage.go", pkgContent)
	defer os.RemoveAll(baseDir) // Clean up the temporary directory

	cfg := &packages.Config{Dir: baseDir} // Set the working directory for packages.Load

	// Call generateSymbols with the custom package
	output, err := generateSymbols(cfg, []string{modulePath})
	if err != nil {
		t.Fatalf("generateSymbols() failed with error: %v", err)
	}
	if output == nil {
		t.Fatal("generateSymbols() returned nil output for custom package")
	}

	outputStr := string(output)

	// Assertions for expected content
	if !strings.Contains(outputStr, "# Public Symbols") {
		t.Errorf("output missing expected header")
	}
	if !strings.Contains(outputStr, fmt.Sprintf("## Package: `%s`", modulePath)) {
		t.Errorf("output missing expected package header for '%s'", modulePath)
	}
	if !strings.Contains(outputStr, "### Constants") {
		t.Errorf("output missing expected Constants header")
	}
	if !strings.Contains(outputStr, "- `const MyConst`") {
		t.Errorf("output missing expected constant 'MyConst'")
	}
	if !strings.Contains(outputStr, "### Variables") {
		t.Errorf("output missing expected Variables header")
	}
	if !strings.Contains(outputStr, "- `var MyVar`") {
		t.Errorf("output missing expected variable 'MyVar'")
	}
	if !strings.Contains(outputStr, "### Functions") {
		t.Errorf("output missing expected Functions header")
	}
	if !strings.Contains(outputStr, "- `func MyFunc`") {
		t.Errorf("output missing expected function 'MyFunc'")
	}
	if !strings.Contains(outputStr, "### Types") {
		t.Errorf("output missing expected Types header")
	}
	if !strings.Contains(outputStr, "- `type MyStruct`") {
		t.Errorf("output missing expected type 'MyStruct'")
	}
}

func TestGenerateSymbols_DocNewFromFilesError(t *testing.T) {
	// Syntax error here: missing closing brace
	pkgContent := `package mypackage

func MyFunc() string {
	return "world"
`

	baseDir, modulePath := createTempGoPackage(t, "mypackage_empty", "empty.go", pkgContent)
	defer os.RemoveAll(baseDir) // Clean up the temporary directory

	cfg := &packages.Config{Dir: baseDir} // Set the working directory for packages.Load

	// Call generateSymbols with the custom package
	output, err := generateSymbols(cfg, []string{modulePath})
	if err == nil {
		t.Fatal("generateSymbols() did not return an error for empty package") // Use t.Fatal to stop on unexpected nil error
	}
	if output != nil {
		t.Errorf("generateSymbols() returned non-nil output for empty package: %s", string(output))
	}

	// Check for expected error message parts
	errorMsg := err.Error()
	// Normalize temporary paths for robust comparison
	re := regexp.MustCompile(`/tmp/testpkg\d+`)
	errorMsg = re.ReplaceAllString(errorMsg, `<TMP>`)

	if !strings.Contains(errorMsg, "multiple errors encountered during symbol generation") {
		t.Errorf("expected error message to contain 'multiple errors encountered during symbol generation', got: %v", errorMsg)
	}
	if !strings.Contains(errorMsg, "syntax error") || !strings.Contains(errorMsg, "unexpected EOF") || !strings.Contains(errorMsg, "expected '}'") {
		t.Errorf("expected error message to contain syntax error details (syntax error, unexpected EOF, expected '}}'), got: %v", errorMsg)
	}
	if !strings.Contains(errorMsg, "mypackage_empty/empty.go") {
		t.Errorf("expected error message to contain file name 'mypackage_empty/empty.go', got: %v", errorMsg)
	}
}

func TestMain_WrongArgCount(t *testing.T) {
	oldExit := exit
	t.Cleanup(func() { exit = oldExit })

	exitCode := 0
	exit = func(code int) { exitCode = code }

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"symbolsgen"} // only 1 arg, need 3

	main()
	assert.Equal(t, 1, exitCode)
}

func TestMain_RunError(t *testing.T) {
	oldFatalf := fatalf
	t.Cleanup(func() { fatalf = oldFatalf })

	called := false
	fatalf = func(format string, args ...interface{}) { called = true }

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"symbolsgen", "/nonexistent-allowlist.txt", "/tmp/out.txt"}

	main()
	assert.True(t, called, "fatalf must be called when run() returns an error")
}

func TestRun_EmptyAllowlist(t *testing.T) {
	allowlist := createTempFile(t, "")
	defer os.Remove(allowlist)
	output := createTempFile(t, "")
	defer os.Remove(output)

	// Empty allowlist → generateSymbols returns nil → no file write
	err := run(allowlist, output)
	assert.NoError(t, err)
}

func TestRun_WriteError(t *testing.T) {
	allowlist := createTempFile(t, "centralrelay/pkg/obs\n")
	defer os.Remove(allowlist)

	err := run(allowlist, "/proc/invalid/readonly/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write")
}

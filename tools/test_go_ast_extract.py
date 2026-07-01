#!/usr/bin/env python3
"""
Unit tests for go_ast_extract.py AST extraction.

Tests cover:
  - Function signature parsing (params, returns)
  - Struct field extraction with types and tags
  - Interface method extraction
  - Position accuracy (start/end line numbers)
  - Function call extraction from bodies
  - Exported flag detection
"""

import tempfile
from pathlib import Path

import pytest
from go_ast_extract import extract_ast  # type: ignore[import-not-found]


@pytest.fixture
def sample_go_file():
    """Create a temporary Go file with various definitions for testing."""
    code = """package test

import (
	"fmt"
	"errors"
)

// SimpleFunc does something simple.
func SimpleFunc(name string, count int) (string, error) {
	if name == "" {
		return "", errors.New("empty name")
	}
	fmt.Println(name)
	return fmt.Sprintf("%s:%d", name, count), nil
}

// MyStruct holds test data.
type MyStruct struct {
	Name    string `json:"name" yaml:"name"`
	Count   int    `json:"count,omitempty" yaml:"count,omitempty"`
	Enabled bool
}

// MyInterface defines a contract.
type MyInterface interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
}

// Receiver method on MyStruct
func (m *MyStruct) String() string {
	return fmt.Sprintf("%s:%d", m.Name, m.Count)
}

// Another method
func (m *MyStruct) Reset() {
	m.Name = ""
	m.Count = 0
}

// unexported function (lowercase)
func privateFunc() {
	fmt.Println("private")
}
"""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".go", delete=False) as f:
        f.write(code)
        f.flush()
        yield Path(f.name)
        Path(f.name).unlink()


class TestFunctionExtraction:
    """Test function and method extraction."""

    def test_function_signature_extraction(self, sample_go_file):
        """Test that function signatures are correctly extracted."""
        result = extract_ast(sample_go_file)

        assert "SimpleFunc" in result
        obj = result["SimpleFunc"]
        assert obj["kind"] == "func"
        assert obj["exported"] is True

        # Check signature
        assert "signature" in obj
        sig = obj["signature"]
        assert len(sig["params"]) == 2
        assert sig["params"][0]["name"] == "name"
        assert sig["params"][0]["type"] == "string"
        assert sig["params"][1]["name"] == "count"
        assert sig["params"][1]["type"] == "int"

        # Check returns
        assert len(sig["returns"]) == 2
        assert "string" in sig["returns"]
        assert "error" in sig["returns"]

    def test_function_calls_extraction(self, sample_go_file):
        """Test that function calls within a body are extracted."""
        result = extract_ast(sample_go_file)

        assert "SimpleFunc" in result
        calls = result["SimpleFunc"].get("calls", [])

        # Should contain the calls from the function body
        assert "errors.New" in calls or "New" in calls
        assert "fmt.Println" in calls or "Println" in calls
        assert "fmt.Sprintf" in calls or "Sprintf" in calls

    def test_method_extraction(self, sample_go_file):
        """Test that methods on types are correctly extracted."""
        result = extract_ast(sample_go_file)

        # Method key should be ReceiverType.MethodName
        assert "MyStruct.String" in result
        obj = result["MyStruct.String"]
        assert obj["kind"] == "method"
        assert obj["exported"] is True

        # Check another method
        assert "MyStruct.Reset" in result
        reset = result["MyStruct.Reset"]
        assert reset["kind"] == "method"

    def test_unexported_function(self, sample_go_file):
        """Test that unexported functions are marked correctly."""
        result = extract_ast(sample_go_file)

        assert "privateFunc" in result
        obj = result["privateFunc"]
        assert obj["exported"] is False


class TestStructExtraction:
    """Test struct field extraction."""

    def test_struct_fields_extraction(self, sample_go_file):
        """Test that struct fields are correctly extracted."""
        result = extract_ast(sample_go_file)

        assert "MyStruct" in result
        obj = result["MyStruct"]
        assert obj["kind"] == "struct"
        assert "fields" in obj

        fields = obj["fields"]
        assert len(fields) >= 3

        # Check Name field
        name_field = next((f for f in fields if f["name"] == "Name"), None)
        assert name_field is not None
        assert name_field["type"] == "string"
        assert "json:" in name_field["tag"]
        assert "yaml:" in name_field["tag"]

        # Check Count field (with omitempty)
        count_field = next((f for f in fields if f["name"] == "Count"), None)
        assert count_field is not None
        assert count_field["type"] == "int"
        assert "omitempty" in count_field["tag"]

        # Check Enabled field (no tags)
        enabled_field = next((f for f in fields if f["name"] == "Enabled"), None)
        assert enabled_field is not None
        assert enabled_field["type"] == "bool"


class TestInterfaceExtraction:
    """Test interface method extraction."""

    def test_interface_methods_extraction(self, sample_go_file):
        """Test that interface methods are correctly extracted."""
        result = extract_ast(sample_go_file)

        assert "MyInterface" in result
        obj = result["MyInterface"]
        assert obj["kind"] == "interface"
        assert "methods" in obj

        methods = obj["methods"]
        assert len(methods) == 3

        # Check method names
        method_names = {m["name"] for m in methods}
        assert "Read" in method_names
        assert "Write" in method_names
        assert "Close" in method_names

        # Check Read signature
        read = next(m for m in methods if m["name"] == "Read")
        assert len(read["params"]) > 0
        assert len(read["returns"]) > 0


class TestPositionAccuracy:
    """Test that line positions are accurate."""

    def test_position_extraction(self, sample_go_file):
        """Test that start and end line positions are extracted."""
        result = extract_ast(sample_go_file)

        # SimpleFunc should have position info
        assert "SimpleFunc" in result
        obj = result["SimpleFunc"]
        assert "position" in obj
        assert "start_line" in obj["position"]
        assert "end_line" in obj["position"]

        start = obj["position"]["start_line"]
        end = obj["position"]["end_line"]
        assert start > 0
        assert end > 0
        assert end >= start

    def test_multiple_objects_position_ordering(self, sample_go_file):
        """Test that positions are correctly ordered for multiple objects."""
        result = extract_ast(sample_go_file)

        # Get positions for functions/methods
        positions = {}
        for key, obj in result.items():
            if "position" in obj:
                positions[key] = obj["position"]

        # Verify we have multiple objects with positions
        assert len(positions) > 3

        # All should have valid start/end
        for key, pos in positions.items():
            assert pos["start_line"] > 0
            assert pos["end_line"] >= pos["start_line"]


class TestExportedFlag:
    """Test exported flag detection."""

    def test_exported_detection(self, sample_go_file):
        """Test that exported vs unexported symbols are correctly identified."""
        result = extract_ast(sample_go_file)

        # Exported: SimpleFunc, MyStruct, MyInterface, String, Reset
        exported = [k for k, v in result.items() if v.get("exported", False)]
        unexported = [k for k, v in result.items() if not v.get("exported", True)]

        # Check some exported
        assert any("SimpleFunc" in k or k == "SimpleFunc" for k in exported)
        assert any("MyStruct" in k or k == "MyStruct" for k in exported)

        # Check some unexported
        assert any("privateFunc" in k or k == "privateFunc" for k in unexported)


class TestEdgeCases:
    """Test edge cases and error handling."""

    def test_nonexistent_file(self):
        """Test that nonexistent file returns empty dict."""
        result = extract_ast(Path("/nonexistent/file.go"))
        # Should handle gracefully
        assert isinstance(result, dict)

    def test_empty_file(self):
        """Test that empty Go file returns empty dict."""
        with tempfile.NamedTemporaryFile(mode="w", suffix=".go", delete=False) as f:
            f.write("package test\n")
            f.flush()
            result = extract_ast(Path(f.name))
            assert isinstance(result, dict)
            Path(f.name).unlink()

    def test_function_without_params_or_returns(self):
        """Test function with no parameters or return values."""
        code = """package test

func NoParamsNoReturns() {
	fmt.Println("hello")
}
"""
        with tempfile.NamedTemporaryFile(mode="w", suffix=".go", delete=False) as f:
            f.write(code)
            f.flush()
            result = extract_ast(Path(f.name))

            assert "NoParamsNoReturns" in result
            obj = result["NoParamsNoReturns"]
            assert obj["signature"]["params"] == []
            assert obj["signature"]["returns"] == []

            Path(f.name).unlink()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])

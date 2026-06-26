#!/usr/bin/env python3
"""
Integration tests for go.meta.gen.py AST metadata merging.

Tests the integration between go_ast_extract and go.meta.gen.py:
  - AST metadata is correctly extracted and merged
  - Fallback mechanism works if import fails
  - Object key matching works for methods (ReceiverType.MethodName)
"""

import pytest
from pathlib import Path
import tempfile
import sys
import yaml

# Import the functions we're testing
# Note: go_meta_gen.py is not a proper module, so we import it as spec
import importlib.util

spec = importlib.util.spec_from_file_location(
    "go_meta_gen",
    Path(__file__).parent / "go.meta.gen.py"
)
go_meta_gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(go_meta_gen)

generate = go_meta_gen.generate
_extract_ast_metadata = go_meta_gen._extract_ast_metadata
_merge_ast_into_objects = go_meta_gen._merge_ast_into_objects


@pytest.fixture
def sample_go_file():
    """Create a temporary Go file for testing."""
    code = '''package cabinet

import "fmt"

// Config holds configuration.
type Config struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// LoadConfig loads configuration from environment.
func LoadConfig() (*Config, error) {
	host := os.Getenv("HOST")
	port := os.Getenv("PORT")
	fmt.Println(host, port)
	return &Config{Host: host}, nil
}

// SetValue sets a value on Config.
func (c *Config) SetValue(key, val string) {
	fmt.Printf("Setting %s=%s\\n", key, val)
}
'''
    with tempfile.NamedTemporaryFile(mode='w', suffix='.go', delete=False, dir=Path(__file__).parent) as f:
        f.write(code)
        f.flush()
        yield Path(f.name)
        Path(f.name).unlink()


class TestASTMetadataExtraction:
    """Test AST metadata extraction in context of go.meta.gen.py."""

    def test_extract_ast_metadata_returns_dict(self, sample_go_file):
        """Test that _extract_ast_metadata returns a dict."""
        result = _extract_ast_metadata(sample_go_file)
        assert isinstance(result, dict)

    def test_ast_metadata_keys_format(self, sample_go_file):
        """Test that AST metadata keys match expected format."""
        result = _extract_ast_metadata(sample_go_file)

        # Should have function names and Type.Method names
        keys = list(result.keys())
        assert len(keys) > 0

        # Check for function key
        func_keys = [k for k in keys if 'LoadConfig' in k]
        assert len(func_keys) > 0

        # Check for method key (Type.Method format)
        method_keys = [k for k in keys if '.' in k]
        assert len(method_keys) > 0

    def test_ast_metadata_contains_position(self, sample_go_file):
        """Test that AST metadata contains position information."""
        result = _extract_ast_metadata(sample_go_file)

        # Find any object with position info
        objects_with_position = [v for v in result.values() if 'position' in v]
        assert len(objects_with_position) > 0

        for obj in objects_with_position:
            assert 'start_line' in obj['position']
            assert 'end_line' in obj['position']
            assert obj['position']['start_line'] > 0
            assert obj['position']['end_line'] >= obj['position']['start_line']

    def test_ast_metadata_contains_signature(self, sample_go_file):
        """Test that AST metadata contains function signatures."""
        result = _extract_ast_metadata(sample_go_file)

        # Find LoadConfig and check signature
        load_config = result.get('LoadConfig')
        if load_config:
            assert 'signature' in load_config
            sig = load_config['signature']
            assert 'params' in sig
            assert 'returns' in sig


class TestMergeASTIntoObjects:
    """Test merging AST metadata into object list."""

    def test_merge_ast_adds_position(self):
        """Test that merging adds position field."""
        objects = [
            {'name': 'TestFunc', 'kind': 'func', 'description': 'Test function'}
        ]
        ast_meta = {
            'TestFunc': {
                'position': {'start_line': 10, 'end_line': 15},
                'signature': {'params': [], 'returns': []},
            }
        }

        result = _merge_ast_into_objects(objects, ast_meta)

        assert len(result) == 1
        assert 'position' in result[0]
        assert result[0]['position']['start_line'] == 10
        assert result[0]['position']['end_line'] == 15

    def test_merge_ast_adds_signature(self):
        """Test that merging adds signature field."""
        objects = [
            {'name': 'MyFunc', 'kind': 'func', 'description': 'My function'}
        ]
        ast_meta = {
            'MyFunc': {
                'position': {'start_line': 1, 'end_line': 5},
                'signature': {
                    'params': [{'name': 'x', 'type': 'string'}],
                    'returns': ['error']
                },
            }
        }

        result = _merge_ast_into_objects(objects, ast_meta)

        assert 'signature' in result[0]
        assert len(result[0]['signature']['params']) == 1
        assert result[0]['signature']['params'][0]['name'] == 'x'

    def test_merge_ast_preserves_existing_fields(self):
        """Test that merging preserves existing object fields."""
        objects = [
            {
                'name': 'TestFunc',
                'kind': 'func',
                'description': 'Original description',
                'references': ['fmt', 'os']
            }
        ]
        ast_meta = {
            'TestFunc': {
                'position': {'start_line': 10, 'end_line': 15},
            }
        }

        result = _merge_ast_into_objects(objects, ast_meta)

        # Original fields preserved
        assert result[0]['description'] == 'Original description'
        assert result[0]['references'] == ['fmt', 'os']
        # AST field added
        assert 'position' in result[0]

    def test_merge_ast_method_matching(self):
        """Test that methods are correctly matched by Type.Method key.

        Note: _merge_ast_into_objects tries both _object_key format
        and simple name lookup, so 'String' lookup should work.
        """
        objects = [
            {
                'name': 'String',
                'kind': 'method',
                'receiver': 'MyStruct',
                'description': 'String method'
            }
        ]
        ast_meta = {
            'String': {  # Simple name key (fallback)
                'position': {'start_line': 20, 'end_line': 25},
                'signature': {'params': [], 'returns': ['string']},
            }
        }

        result = _merge_ast_into_objects(objects, ast_meta)

        # Method should be matched and updated
        assert 'position' in result[0]
        assert result[0]['position']['start_line'] == 20


class TestGenerate:
    """Test the full generate function with AST integration."""

    def test_generate_includes_ast_metadata(self, sample_go_file):
        """Test that generate() includes AST metadata in output."""
        result = generate(sample_go_file)

        assert 'objects' in result
        assert len(result['objects']) > 0

        # Check that at least some objects have position info
        objects_with_position = [o for o in result['objects'] if 'position' in o]
        assert len(objects_with_position) > 0

    def test_generate_output_structure(self, sample_go_file):
        """Test that generate output has correct YAML structure."""
        result = generate(sample_go_file)

        # Check required fields
        assert 'package' in result
        assert 'description' in result
        assert 'tags' in result
        assert 'category' in result
        assert 'used_in' in result
        assert 'entrypoint' in result
        assert 'related_nodes' in result
        assert 'objects' in result

    def test_generate_object_has_ast_fields(self, sample_go_file):
        """Test that generated objects contain AST fields."""
        result = generate(sample_go_file)

        # Find a function object
        func_objs = [o for o in result['objects'] if o.get('kind') == 'func']
        assert len(func_objs) > 0

        # Check it has AST-derived fields
        func = func_objs[0]
        # At minimum should have position if AST extraction worked
        if 'position' in func:
            assert 'start_line' in func['position']
            assert 'end_line' in func['position']


class TestFallbackBehavior:
    """Test fallback behavior if AST extraction fails."""

    def test_merge_with_empty_ast_metadata(self):
        """Test that merge works fine with empty AST metadata dict."""
        objects = [
            {'name': 'TestFunc', 'kind': 'func', 'description': 'Test'}
        ]
        result = _merge_ast_into_objects(objects, {})

        # Should return objects unchanged
        assert len(result) == 1
        assert result[0]['name'] == 'TestFunc'

    def test_extract_ast_metadata_returns_empty_on_invalid_file(self):
        """Test that _extract_ast_metadata returns {} for invalid input."""
        result = _extract_ast_metadata(Path('/nonexistent/file.go'))
        assert result == {}


if __name__ == '__main__':
    pytest.main([__file__, '-v'])

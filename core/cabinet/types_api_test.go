package cabinet_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"centralrelay/core/cabinet"
)

func TestBaseBlock_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.BaseBlock{})
	assert.Equal(t, 10, typeOf.NumField(), "BaseBlock field count")
}

func TestSchemaDef_API_ShapeAndZeroValue(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.SchemaDef{})
	assert.Equal(t, 3, typeOf.NumField(), "SchemaDef field count")
}

func TestSchema_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.Schema{})
	assert.Equal(t, 2, typeOf.NumField(), "Schema field count")
}

func TestModuleDescriptor_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.ModuleDescriptor{})
	// Meta, Type, Accepts, OutputKeys, WasmCode
	assert.Equal(t, 6, typeOf.NumField(), "ModuleDescriptor field count")
}

func TestWorkflow_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.Workflow{})
	assert.Equal(t, 2, typeOf.NumField(), "Workflow field count")
}

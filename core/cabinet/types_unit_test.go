package cabinet

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaseBlock_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(BaseBlock{})
	assert.Equal(t, 10, typeOf.NumField(), "BaseBlock field count")
}

func TestSchemaDef_ShapeAndZeroValue(t *testing.T) {
	typeOf := reflect.TypeOf(SchemaDef{})
	assert.Equal(t, 3, typeOf.NumField(), "SchemaDef field count")
}

func TestSchema_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(Schema{})
	assert.Equal(t, 2, typeOf.NumField(), "Schema field count")
}

func TestModuleDescriptor_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(ModuleDescriptor{})
	// Meta, Type, Accepts, OutputKeys, WasmCode
	assert.Equal(t, 7, typeOf.NumField(), "ModuleDescriptor field count")
}

func TestWorkflow_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(Workflow{})
	assert.Equal(t, 2, typeOf.NumField(), "Workflow field count")
}

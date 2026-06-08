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

func TestSchemaIdentity_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.SchemaIdentity{})
	assert.Equal(t, 3, typeOf.NumField(), "SchemaIdentity field count")
}

func TestSchemaConfig_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.SchemaConfig{})
	assert.Equal(t, 7, typeOf.NumField(), "SchemaConfig field count")
}

func TestSchemaSigning_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.SchemaSigning{})
	assert.Equal(t, 4, typeOf.NumField(), "SchemaSigning field count")
}

func TestSchema_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.Schema{})
	assert.Equal(t, 4, typeOf.NumField(), "Schema field count")
}

func TestModuleDescriptor_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.ModuleDescriptor{})
	// Meta, Type, Accepts, OutputKeys, WasmCode
	assert.Equal(t, 6, typeOf.NumField(), "ModuleDescriptor field count")
}

func TestWorkflowIdentity_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.WorkflowIdentity{})
	assert.Equal(t, 3, typeOf.NumField(), "WorkflowIdentity field count")
}

func TestWorkflowConfig_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.WorkflowConfig{})
	assert.Equal(t, 5, typeOf.NumField(), "WorkflowConfig field count")
}

func TestWorkflowSigning_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.WorkflowSigning{})
	assert.Equal(t, 4, typeOf.NumField(), "WorkflowSigning field count")
}

func TestWorkflow_API_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(cabinet.Workflow{})
	assert.Equal(t, 4, typeOf.NumField(), "Workflow field count")
}

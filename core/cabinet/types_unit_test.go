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

func TestSchemaIdentity_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(SchemaIdentity{})
	assert.Equal(t, 3, typeOf.NumField(), "SchemaIdentity field count")
}

func TestSchemaConfig_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(SchemaConfig{})
	assert.Equal(t, 7, typeOf.NumField(), "SchemaConfig field count")
}

func TestSchemaSigning_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(SchemaSigning{})
	assert.Equal(t, 4, typeOf.NumField(), "SchemaSigning field count")
}

func TestSchema_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(Schema{})
	assert.Equal(t, 4, typeOf.NumField(), "Schema field count")
}

func TestModuleDescriptor_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(ModuleDescriptor{})
	// Meta, Type, Accepts, OutputKeys, WasmCode, wazeroInstance, NativeImpl
	assert.Equal(t, 6, typeOf.NumField(), "ModuleDescriptor field count")
}

func TestWorkflowIdentity_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(WorkflowIdentity{})
	assert.Equal(t, 3, typeOf.NumField(), "WorkflowIdentity field count")
}

func TestWorkflowConfig_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(WorkflowConfig{})
	assert.Equal(t, 5, typeOf.NumField(), "WorkflowConfig field count")
}

func TestWorkflowSigning_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(WorkflowSigning{})
	assert.Equal(t, 4, typeOf.NumField(), "WorkflowSigning field count")
}

func TestWorkflow_Shape(t *testing.T) {
	typeOf := reflect.TypeOf(Workflow{})
	assert.Equal(t, 4, typeOf.NumField(), "Workflow field count")
}

package iac

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
)

// stubCabinet implements cabinet.Cabinet with only GetSchema functional.
// All other methods are no-ops — only schema resolution is tested here.
type stubCabinet struct {
	schemas map[string]struct{}
}

func (s *stubCabinet) GetSchema(id string) (*cabinet.Schema, error) {
	if _, ok := s.schemas[id]; ok {
		return &cabinet.Schema{}, nil
	}
	return nil, errors.New("not found: " + id)
}

func (s *stubCabinet) PutSchema(_ *cabinet.Schema) error                     { return nil }
func (s *stubCabinet) PutModule(_ *cabinet.ModuleDescriptor) error           { return nil }
func (s *stubCabinet) GetModule(_ string) (*cabinet.ModuleDescriptor, error) { return nil, nil }
func (s *stubCabinet) PutWorkflow(_ *cabinet.Workflow) error                 { return nil }
func (s *stubCabinet) GetWorkflow(_ string) (*cabinet.Workflow, error)       { return nil, nil }
func (s *stubCabinet) Setx(_ context.Context, _ *cabinet.SetPayload) (*cabinet.SetResponse, error) {
	return nil, nil
}
func (s *stubCabinet) Close(_ context.Context) error { return nil }

// newStubCabinet returns a cabinet stub with the given schema IDs pre-populated.
func newStubCabinet(ids ...string) *stubCabinet {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return &stubCabinet{schemas: m}
}

func makeTestResource(schemaName, version string) types.ResourceSpec {
	return types.ResourceSpec{
		Meta:      types.ResourceMeta{Name: "res-test"},
		SchemaRef: types.SchemaRef{Name: schemaName, Version: version},
		State:     types.ResourceState{Desired: "present"},
	}
}

func TestIaCValidator_ValidateResource_SchemaFound(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("cert.selfsigned@1.0"))
	err := v.ValidateResource(context.Background(), makeTestResource("cert.selfsigned", "1.0"))
	require.NoError(t, err)
}

func TestIaCValidator_ValidateResource_SchemaNotFound(t *testing.T) {
	v := NewIaCValidator(newStubCabinet())
	err := v.ValidateResource(context.Background(), makeTestResource("cert.selfsigned", "1.0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cert.selfsigned@1.0")
}

func TestIaCValidator_Validate_Graph_AllValid(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("cert.selfsigned@1.0", "core.test@1.0"))

	graph := &types.Graph{
		Services: map[string]*types.Service{
			"svc-a": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{
				makeTestResource("cert.selfsigned", "1.0"),
			}}},
			"svc-b": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{
				makeTestResource("core.test", "1.0"),
			}}},
		},
	}

	require.NoError(t, v.Validate(context.Background(), graph))
}

func TestIaCValidator_Validate_Graph_PartialFailure(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("cert.selfsigned@1.0")) // core.test missing

	graph := &types.Graph{
		Services: map[string]*types.Service{
			"svc-a": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{
				makeTestResource("cert.selfsigned", "1.0"),
			}}},
			"svc-b": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{
				makeTestResource("core.test", "1.0"),
			}}},
		},
	}

	err := v.Validate(context.Background(), graph)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "core.test@1.0")
	assert.NotContains(t, err.Error(), "cert.selfsigned@1.0")
}

func TestIaCValidator_Validate_EmptyGraph(t *testing.T) {
	v := NewIaCValidator(newStubCabinet())
	graph := &types.Graph{Services: map[string]*types.Service{}}
	require.NoError(t, v.Validate(context.Background(), graph))
}

// A bare (write-defaulted) spec passes; an explicitly non-writable field in an
// intent spec is a structural error.
func TestIaCValidator_SpecStructure(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("core.test@1.0"))

	ok := makeTestResource("core.test", "1.0")
	ok.Spec = map[string]interface{}{"display_name": "x"} // bare → write:true
	require.NoError(t, v.ValidateResource(context.Background(), ok))

	bad := makeTestResource("core.test", "1.0")
	bad.Spec = map[string]interface{}{
		"provider_id": map[string]interface{}{"value": "ocid1", "mode": map[string]interface{}{"write": false}},
	}
	err := v.ValidateResource(context.Background(), bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-writable")
	assert.Contains(t, err.Error(), "provider_id")
}

// A dependency resolves iff a resource with that name exists in the graph.
func TestIaCValidator_DependencyIntegrity(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("core.test@1.0"))

	subnet := makeTestResource("core.test", "1.0")
	subnet.Meta = types.ResourceMeta{Name: "subnet-a"}

	vm := makeTestResource("core.test", "1.0")
	vm.Meta = types.ResourceMeta{Name: "vm-01"}
	vm.Dependencies = []string{"subnet-a"} // resolves

	graph := &types.Graph{Services: map[string]*types.Service{
		"svc": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{subnet, vm}}},
	}}
	require.NoError(t, v.Validate(context.Background(), graph))

	// dangling dependency → error
	vm.Dependencies = []string{"subnet-a", "does-not-exist"}
	graph.Services["svc"].Spec.Resources = []types.ResourceSpec{subnet, vm}
	err := v.Validate(context.Background(), graph)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
	assert.Contains(t, err.Error(), "does not resolve")
}

// A field-level object reference resolves iff the target exists and its kind
// (when named) matches.
func TestIaCValidator_FieldReference(t *testing.T) {
	v := NewIaCValidator(newStubCabinet("core.test@1.0"))

	rt := makeTestResource("core.test", "1.0")
	rt.Meta = types.ResourceMeta{Name: "production-private", Kind: "RouteTable"}

	subnet := makeTestResource("core.test", "1.0")
	subnet.Meta = types.ResourceMeta{Name: "subnet-a", Kind: "Subnet"}
	subnet.Spec = map[string]interface{}{
		"route_table": map[string]interface{}{"ref": "production-private", "kind": "RouteTable"},
	}
	graph := &types.Graph{Services: map[string]*types.Service{
		"svc": {Spec: types.ServiceSpec{Resources: []types.ResourceSpec{rt, subnet}}},
	}}
	require.NoError(t, v.Validate(context.Background(), graph))

	// dangling reference → error
	subnet.Spec = map[string]interface{}{"route_table": map[string]interface{}{"ref": "ghost"}}
	graph.Services["svc"].Spec.Resources = []types.ResourceSpec{rt, subnet}
	err := v.Validate(context.Background(), graph)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost")
	assert.Contains(t, err.Error(), "does not exist")

	// wrong kind → error
	subnet.Spec = map[string]interface{}{"route_table": map[string]interface{}{"ref": "production-private", "kind": "Subnet"}}
	graph.Services["svc"].Spec.Resources = []types.ResourceSpec{rt, subnet}
	err = v.Validate(context.Background(), graph)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "as kind")
}

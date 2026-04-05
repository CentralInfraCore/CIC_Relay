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

func (s *stubCabinet) PutSchema(_ *cabinet.Schema) error                              { return nil }
func (s *stubCabinet) PutModule(_ *cabinet.ModuleDescriptor) error                    { return nil }
func (s *stubCabinet) GetModule(_ string) (*cabinet.ModuleDescriptor, error)          { return nil, nil }
func (s *stubCabinet) PutWorkflow(_ *cabinet.Workflow) error                          { return nil }
func (s *stubCabinet) GetWorkflow(_ string) (*cabinet.Workflow, error)                { return nil, nil }
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

package main

import (
	"context"

	"centralrelay/core/cabinet"
	"github.com/stretchr/testify/mock"
)

// mockCabinetService is a mock implementation of the cabinet.Cabinet interface.
// It is defined in a helper file to be accessible by all tests in the package.
type mockCabinetService struct {
	mock.Mock
}

// PutSchema records the call and returns the stubbed error.
func (m *mockCabinetService) PutSchema(s *cabinet.Schema) error {
	args := m.Called(s)
	return args.Error(0)
}

// GetSchema returns the stubbed schema (nil when stubbed with nil) and error.
func (m *mockCabinetService) GetSchema(id string) (*cabinet.Schema, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cabinet.Schema), args.Error(1)
}

// PutModule records the call and returns the stubbed error.
func (m *mockCabinetService) PutModule(md *cabinet.ModuleDescriptor) error {
	args := m.Called(md)
	return args.Error(0)
}

// GetModule returns the stubbed module descriptor (nil when stubbed with nil) and error.
func (m *mockCabinetService) GetModule(id string) (*cabinet.ModuleDescriptor, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cabinet.ModuleDescriptor), args.Error(1)
}

// PutWorkflow records the call and returns the stubbed error.
func (m *mockCabinetService) PutWorkflow(w *cabinet.Workflow) error {
	args := m.Called(w)
	return args.Error(0)
}

// GetWorkflow returns the stubbed workflow (nil when stubbed with nil) and error.
func (m *mockCabinetService) GetWorkflow(id string) (*cabinet.Workflow, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cabinet.Workflow), args.Error(1)
}

// Setx returns the stubbed response (nil when stubbed with nil) and error.
func (m *mockCabinetService) Setx(ctx context.Context, in *cabinet.SetPayload) (*cabinet.SetResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cabinet.SetResponse), args.Error(1)
}

// Close records the call and returns the stubbed error.
func (m *mockCabinetService) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

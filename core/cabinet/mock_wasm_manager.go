package cabinet

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockWasmManager is a mock implementation of the wasmManager interface for unit testing.
type MockWasmManager struct {
	mock.Mock
}

// NewHostInstance mocks the creation of a new host instance.
// It allows tests to specify what cicWasmHost instance (or error) should be returned.
func (m *MockWasmManager) NewHostInstance(ctx context.Context, moduleID string) (cicWasmHost, error) {
	args := m.Called(ctx, moduleID)
	// The first return argument can be nil if we are testing an error case.
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	// We expect a cicWasmHost interface, so we assert to that interface.
	return args.Get(0).(cicWasmHost), args.Error(1)
}

// Close mocks the closing of the WasmManager.
func (m *MockWasmManager) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockCicWasmHost is a mock for the cicWasmHost interface.
type MockCicWasmHost struct {
	mock.Mock
}

// Process returns the stubbed (result, error) strings.
func (m *MockCicWasmHost) Process(ctx context.Context, authContextJson, inputJson string) (string, string) {
	args := m.Called(ctx, authContextJson, inputJson)
	return args.String(0), args.String(1)
}

// Get returns the stubbed (result, error) strings.
func (m *MockCicWasmHost) Get(ctx context.Context, authContextJson, queryJson string) (string, string) {
	args := m.Called(ctx, authContextJson, queryJson)
	return args.String(0), args.String(1)
}

// Init returns the stubbed (result, error) strings.
func (m *MockCicWasmHost) Init(ctx context.Context, authContextJson, configJson string) (string, string) {
	args := m.Called(ctx, authContextJson, configJson)
	return args.String(0), args.String(1)
}

// Notify returns the stubbed (result, error) strings.
func (m *MockCicWasmHost) Notify(ctx context.Context, authContextJson, eventJson string) (string, string) {
	args := m.Called(ctx, authContextJson, eventJson)
	return args.String(0), args.String(1)
}

// Call returns the stubbed (result, error) strings for the named op.
func (m *MockCicWasmHost) Call(ctx context.Context, op, authContextJson, inputJson string) (string, string) {
	args := m.Called(ctx, op, authContextJson, inputJson)
	return args.String(0), args.String(1)
}

// Close records the call and returns the stubbed error.
func (m *MockCicWasmHost) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Ensure MockWasmManager implements the wasmManager interface.
var _ wasmManager = (*MockWasmManager)(nil)

// Ensure MockCicWasmHost implements the cicWasmHost interface.
var _ cicWasmHost = (*MockCicWasmHost)(nil)

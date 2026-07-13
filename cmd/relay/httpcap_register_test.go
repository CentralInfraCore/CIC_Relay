package main

import (
	"context"
	"errors"
	"testing"

	"centralrelay/core/cabinet"
	"centralrelay/core/modules/httpcap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A no-op actuator stands in for the FFI bridge in the default build, so the
// registration path is fully exercised without cgo.
var mockActuator = httpcap.ActuatorFunc(func(_ []byte) ([]byte, error) { return []byte(`{"ok":true}`), nil })

func TestRegisterHTTPCapability_NilActuatorIsNoOp(t *testing.T) {
	mockCabinet := new(mockCabinetService)
	// No PutModule expectation: a nil actuator must register nothing.
	err := registerHTTPCapability(context.Background(), mockCabinet, nil)
	require.NoError(t, err)
	mockCabinet.AssertNotCalled(t, "PutModule", mock.Anything)
}

func TestRegisterHTTPCapability_RegistersHTTPModule(t *testing.T) {
	mockCabinet := new(mockCabinetService)
	var got *cabinet.ModuleDescriptor
	mockCabinet.On("PutModule", mock.Anything).Run(func(args mock.Arguments) {
		got = args.Get(0).(*cabinet.ModuleDescriptor)
	}).Return(nil).Once()

	err := registerHTTPCapability(context.Background(), mockCabinet, mockActuator)
	require.NoError(t, err)
	mockCabinet.AssertExpectations(t)

	require.NotNil(t, got)
	assert.Equal(t, "http@1.0", got.ID())
	require.NotNil(t, got.NativeImpl)
	_, ok := got.NativeImpl["request"]
	assert.True(t, ok, "http@1.0 must expose the 'request' function")
}

func TestRegisterHTTPCapability_PutModuleErrorPropagates(t *testing.T) {
	mockCabinet := new(mockCabinetService)
	mockCabinet.On("PutModule", mock.Anything).Return(errors.New("store full")).Once()

	err := registerHTTPCapability(context.Background(), mockCabinet, mockActuator)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http module")
}

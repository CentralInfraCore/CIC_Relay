//go:build ffi_cgo

package main

import (
	"context"
	"testing"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/merkle"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestActuatorPresentUnderCgo proves the deploy span: in a relay built with the
// ffi_cgo bridge, the actuator is wired and registerNativeComponents registers
// the live http.request capability (module http@1.0 with a "request" function).
func TestActuatorPresentUnderCgo(t *testing.T) {
	require.NotNil(t, newActuator(), "ffi_cgo build must provide an FFI-backed actuator")

	mockCabinet := new(mockCabinetService)
	var registered []*cabinet.ModuleDescriptor
	mockCabinet.On("PutModule", mock.Anything).Run(func(args mock.Arguments) {
		registered = append(registered, args.Get(0).(*cabinet.ModuleDescriptor))
	}).Return(nil)
	mockCabinet.On("PutWorkflow", mock.Anything).Return(nil)

	err := registerNativeComponents(context.Background(), mockCabinet, &PKIAssets{}, merkle.BuildContext{}, nil, &config{})
	require.NoError(t, err)

	var httpMod *cabinet.ModuleDescriptor
	for _, m := range registered {
		if m.ID() == "http@1.0" {
			httpMod = m
			break
		}
	}
	require.NotNil(t, httpMod, "http@1.0 capability must be registered in the ffi_cgo build")
	require.NotNil(t, httpMod.NativeImpl)
	_, ok := httpMod.NativeImpl["request"]
	assert.True(t, ok, "http@1.0 must expose the 'request' function")
}

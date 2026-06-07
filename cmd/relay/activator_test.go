package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
)

func silentActivatorLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(&bytes.Buffer{})
	return l
}

func makeResource(schemaName, schemaVersion string, spec map[string]any) types.ResourceSpec {
	return types.ResourceSpec{
		Meta:      types.ResourceMeta{Name: "res-test"},
		SchemaRef: types.SchemaRef{Name: schemaName, Version: schemaVersion},
		Spec:      spec,
		State:     types.ResourceState{Desired: "present"},
	}
}

func TestCabinetActivator_Activate_Success(t *testing.T) {
	mc := new(mockCabinetService)
	mc.On("Setx", mock.Anything, mock.MatchedBy(func(p *cabinet.SetPayload) bool {
		return p.WorkflowID == "core.test@1.0" &&
			len(p.Modules) == 1 &&
			string(p.Modules[0]) == "core.test@1.0"
	})).Return(&cabinet.SetResponse{
		Results: map[string]any{"status": "ok"},
		Trace:   &cabinet.ProofTrace{ChainHash: "abc123"},
	}, nil)

	act := NewCabinetActivator(mc, silentActivatorLogger())
	res := makeResource("core.test", "1.0", map[string]any{"key": "value"})

	err := act.Activate(context.Background(), "svc-a", res)
	require.NoError(t, err)
	mc.AssertExpectations(t)
}

func TestCabinetActivator_Activate_SetxError(t *testing.T) {
	mc := new(mockCabinetService)
	mc.On("Setx", mock.Anything, mock.Anything).Return(nil, errors.New("workflow not found"))

	act := NewCabinetActivator(mc, silentActivatorLogger())
	res := makeResource("core.test", "1.0", nil)

	err := act.Activate(context.Background(), "svc-a", res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow not found")
	assert.Contains(t, err.Error(), "core.test@1.0")
}

func TestCabinetActivator_Activate_NilTrace(t *testing.T) {
	mc := new(mockCabinetService)
	mc.On("Setx", mock.Anything, mock.Anything).Return(&cabinet.SetResponse{
		Results: map[string]any{},
		Trace:   nil,
	}, nil)

	act := NewCabinetActivator(mc, silentActivatorLogger())
	res := makeResource("core.test", "2.0", nil)

	err := act.Activate(context.Background(), "svc-b", res)
	require.NoError(t, err)
}

func TestCabinetActivator_Activate_PayloadMapping(t *testing.T) {
	var capturedPayload *cabinet.SetPayload
	mc := new(mockCabinetService)
	mc.On("Setx", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedPayload = args.Get(1).(*cabinet.SetPayload)
	}).Return(&cabinet.SetResponse{}, nil)

	act := NewCabinetActivator(mc, silentActivatorLogger())
	spec := map[string]any{"host": "db.local", "port": 5432}
	res := makeResource("postgres.instance", "v2", spec)

	require.NoError(t, act.Activate(context.Background(), "svc-db", res))

	require.NotNil(t, capturedPayload)
	assert.Equal(t, "postgres.instance@v2", capturedPayload.WorkflowID)
	assert.Equal(t, spec, capturedPayload.Payload)
}

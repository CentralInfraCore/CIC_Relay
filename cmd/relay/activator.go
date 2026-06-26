package main

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/types"
)

// CabinetActivator implements operator.ResourceActivator by bridging IaC resource
// declarations to Cabinet.Setx() workflow execution.
//
// WorkflowID convention: "<schemaRef.Name>@<schemaRef.Version>"
// This matches the schemaRef already validated by IaCValidator — if the schema exists,
// the same ID is used to look up the workflow and module.
type CabinetActivator struct {
	cabinet cabinet.Cabinet
	logger  *logrus.Logger
}

// NewCabinetActivator creates an activator backed by the given Cabinet.
func NewCabinetActivator(c cabinet.Cabinet, logger *logrus.Logger) *CabinetActivator {
	return &CabinetActivator{cabinet: c, logger: logger}
}

// Activate executes the workflow for a validated, desired-present IaC resource.
// It maps res.SchemaRef to WorkflowID, passes res.Spec directly as the payload, and calls Cabinet.Setx().
func (a *CabinetActivator) Activate(ctx context.Context, svcName string, res types.ResourceSpec) error {
	schemaID := res.SchemaRef.Name + "@" + res.SchemaRef.Version

	payload := &cabinet.SetPayload{
		WorkflowID: schemaID,
		Modules:    []cabinet.ModuleID{cabinet.ModuleID(schemaID)},
		Payload:    res.Spec,
	}
	cabinet.CanonicalizeSetPayload(payload)

	resp, err := a.cabinet.Setx(ctx, payload)
	if err != nil {
		return fmt.Errorf("activate %s/%s (workflow %s): %w", svcName, res.Meta.Name, schemaID, err)
	}

	log := a.logger.WithFields(logrus.Fields{
		"service":  svcName,
		"resource": res.Meta.Name,
		"workflow": schemaID,
	})
	if resp.Trace != nil {
		log = log.WithField("chain_hash", resp.Trace.ChainHash)
	}
	log.Info("resource activated")
	return nil
}

package types

import (
	"time"
)

// Metadata defines the common metadata block for all CIC components.
// Based on the structure seen in postgresql-v0.18.0_2025.yaml.
type Metadata struct {
	Name           string             `json:"name" yaml:"name"`
	Version        string             `json:"version,omitempty" yaml:"version,omitempty"`
	Description    string             `json:"description,omitempty" yaml:"description,omitempty"`
	Owner          string             `json:"owner,omitempty" yaml:"owner,omitempty"`
	Tags           []string           `json:"tags,omitempty" yaml:"tags,omitempty"`
	Validity       *Validity          `json:"validity,omitempty" yaml:"validity,omitempty"`
	Checksum       string             `json:"checksum,omitempty" yaml:"checksum,omitempty"`
	Sign           string             `json:"sign,omitempty" yaml:"sign,omitempty"`
	BuildTimestamp *time.Time         `json:"build_timestamp,omitempty" yaml:"build_timestamp,omitempty"`
	CreatedBy      *EntityIdentity    `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	ValidatedBy    *ValidationReceipt `json:"validatedBy,omitempty" yaml:"validatedBy,omitempty"`
}

// Validity defines the time range for which a component is valid.
type Validity struct {
	From  time.Time `json:"from" yaml:"from"`
	Until time.Time `json:"until" yaml:"until"`
}

// EntityIdentity represents the creator of a component (signed).
type EntityIdentity struct {
	Name              string `json:"name" yaml:"name"`
	Email             string `json:"email" yaml:"email"`
	Certificate       string `json:"certificate" yaml:"certificate"`
	IssuerCertificate string `json:"issuer_certificate" yaml:"issuer_certificate"`
}

// ValidationReceipt proves that the component was validated by a specific meta-schema.
type ValidationReceipt struct {
	Name     string `json:"name" yaml:"name"`
	Version  string `json:"version" yaml:"version"`
	Checksum string `json:"checksum" yaml:"checksum"`
}

// BaseComponent is the common interface for Relay, Host, and Service definitions.
type BaseComponent struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
}

// Relay represents a Relay node in the infrastructure graph.
type Relay struct {
	BaseComponent `yaml:",inline"`
	Spec          RelaySpec `json:"spec" yaml:"spec"`
}

type RelaySpec struct {
	// Hosts is a list of references to Host definition files or embedded objects.
	Hosts []string `json:"hosts" yaml:"hosts"`
}

// Host represents a Host node (execution environment).
type Host struct {
	BaseComponent `yaml:",inline"`
	Spec          HostSpec `json:"spec" yaml:"spec"`
}

type HostSpec struct {
	OS       string   `json:"os" yaml:"os"`
	Services []string `json:"services" yaml:"services"` // References to Service definitions
}

// Service represents a functional unit (e.g., PostgreSQL).
type Service struct {
	BaseComponent `yaml:",inline"`
	Spec          ServiceSpec `json:"spec" yaml:"spec"`
}

type ServiceSpec struct {
	Module string                 `json:"module" yaml:"module"` // Reference to the schema/module name (e.g., "postgresql")
	Config map[string]interface{} `json:"config" yaml:"config"` // The actual configuration payload
}

// Graph represents the in-memory execution graph.
type Graph struct {
	Relays   map[string]*Relay
	Hosts    map[string]*Host
	Services map[string]*Service
}

// ExpectedState represents the "Desired State" snapshot for the recorder.
type ExpectedState struct {
	Graph     *Graph    `json:"graph"`
	Timestamp time.Time `json:"timestamp"`
	RunID     string    `json:"run_id"`
}

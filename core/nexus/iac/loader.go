package iac

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"centralrelay/core/nexus/types"
	"gopkg.in/yaml.v3"
)

// IaCSource defines the interface for retrieving raw configuration data.
type IaCSource interface {
	// FetchConfig returns a map of filenames to their content.
	// The keys should be relative paths (e.g., "relay.yaml", "hosts/host-01.yaml").
	FetchConfig(ctx context.Context) (map[string][]byte, error)
}

// Loader is responsible for parsing IaC data and building the graph.
type Loader struct {
	Source IaCSource
}

// NewLoader creates a new Loader instance.
func NewLoader(source IaCSource) *Loader {
	return &Loader{
		Source: source,
	}
}

// Load reads the configuration and builds the infrastructure graph.
func (l *Loader) Load(ctx context.Context) (*types.Graph, error) {
	rawConfig, err := l.Source.FetchConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}

	graph := &types.Graph{
		Relays:   make(map[string]*types.Relay),
		Hosts:    make(map[string]*types.Host),
		Services: make(map[string]*types.Service),
	}

	// Temporary maps to store objects by their file path for linking
	relaysByPath := make(map[string]*types.Relay)
	hostsByPath := make(map[string]*types.Host)
	servicesByPath := make(map[string]*types.Service)

	// 1. Parse all files
	for path, content := range rawConfig {
		// Skip non-yaml files if any slipped through
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		// Decode base component to determine Kind
		var base types.BaseComponent
		if err := yaml.Unmarshal(content, &base); err != nil {
			return nil, fmt.Errorf("failed to parse base component in %s: %w", path, err)
		}

		switch base.Kind {
		case "Relay":
			var relay types.Relay
			if err := yaml.Unmarshal(content, &relay); err != nil {
				return nil, fmt.Errorf("failed to parse Relay in %s: %w", path, err)
			}
			graph.Relays[relay.Metadata.Name] = &relay
			relaysByPath[path] = &relay

		case "Host":
			var host types.Host
			if err := yaml.Unmarshal(content, &host); err != nil {
				return nil, fmt.Errorf("failed to parse Host in %s: %w", path, err)
			}
			graph.Hosts[host.Metadata.Name] = &host
			hostsByPath[path] = &host

		case "Service":
			var service types.Service
			if err := yaml.Unmarshal(content, &service); err != nil {
				return nil, fmt.Errorf("failed to parse Service in %s: %w", path, err)
			}
			graph.Services[service.Metadata.Name] = &service
			servicesByPath[path] = &service

		default:
			// Ignore unknown kinds or meta-schemas (like project.yaml)
			continue
		}
	}

	// 2. Link objects (Graph Building)
	// Note: In a real implementation, we would replace string references with pointers
	// or validate that the referenced names exist. For now, we validate existence.

	// Validate Relay -> Host links
	for path, relay := range relaysByPath {
		for _, hostRef := range relay.Spec.Hosts {
			// hostRef is expected to be a relative path from the relay file, or from root.
			// For simplicity, let's assume it's relative to the root or matches a known file path.
			// A robust implementation would handle path resolution (filepath.Join(dir(path), hostRef)).

			// Try to find exact match first
			if _, exists := hostsByPath[hostRef]; !exists {
				// Try resolving relative path
				dir := filepath.Dir(path)
				resolvedPath := filepath.Join(dir, hostRef)
				if _, exists := hostsByPath[resolvedPath]; !exists {
					return nil, fmt.Errorf("relay %s references unknown host file: %s (resolved: %s)", relay.Metadata.Name, hostRef, resolvedPath)
				}
			}
		}
	}

	// Validate Host -> Service links
	for path, host := range hostsByPath {
		for _, serviceRef := range host.Spec.Services {
			// Try to find exact match first
			if _, exists := servicesByPath[serviceRef]; !exists {
				// Try resolving relative path
				dir := filepath.Dir(path)
				resolvedPath := filepath.Join(dir, serviceRef)
				if _, exists := servicesByPath[resolvedPath]; !exists {
					return nil, fmt.Errorf("host %s references unknown service file: %s (resolved: %s)", host.Metadata.Name, serviceRef, resolvedPath)
				}
			}
		}
	}

	return graph, nil
}

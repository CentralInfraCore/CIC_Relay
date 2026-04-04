package iac

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoader_Load_Success(t *testing.T) {
	// Use the testdata directory
	source := NewFileSource("./testdata")
	loader := NewLoader(source)

	graph, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.NotNil(t, graph)

	// Verify graph content
	assert.Len(t, graph.Relays, 1, "should have one relay")
	assert.Len(t, graph.Hosts, 1, "should have one host")
	assert.Len(t, graph.Services, 1, "should have one service")

	relay, ok := graph.Relays["test-relay"]
	require.True(t, ok, "relay 'test-relay' should exist")
	assert.Equal(t, "test-relay", relay.Metadata.Name)
	assert.Len(t, relay.Spec.Hosts, 1)

	host, ok := graph.Hosts["host-01"]
	require.True(t, ok, "host 'host-01' should exist")
	assert.Equal(t, "linux", host.Spec.OS)
	assert.Len(t, host.Spec.Services, 1)

	service, ok := graph.Services["service-a"]
	require.True(t, ok, "service 'service-a' should exist")
	assert.Equal(t, "test-module", service.Spec.Module)
	require.Len(t, service.Spec.Resources, 1, "service should have one resource")
	res := service.Spec.Resources[0]
	assert.Equal(t, "resource-01", res.Meta.Name)
	assert.Equal(t, "core.test", res.SchemaRef.Name)
	assert.Equal(t, "1.0", res.SchemaRef.Version)
	assert.Equal(t, "present", res.State.Desired)
}

func TestLoader_Load_RelativePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "hosts"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "services"), 0755))

	// relay.yaml references host via relative path
	relayContent := `
apiVersion: cic/v1
kind: Relay
metadata:
  name: rel-relay
spec:
  hosts:
    - ./hosts/host.yaml
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "relay.yaml"), []byte(relayContent), 0644))

	// host.yaml references service via relative path (going up one level)
	hostContent := `
apiVersion: cic/v1
kind: Host
metadata:
  name: rel-host
spec:
  os: linux
  services:
    - ../services/svc.yaml
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hosts/host.yaml"), []byte(hostContent), 0644))

	// svc.yaml
	svcContent := `
apiVersion: cic/v1
kind: Service
metadata:
  name: rel-svc
spec:
  module: mod
  config: {}
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "services/svc.yaml"), []byte(svcContent), 0644))

	source := NewFileSource(tmpDir)
	loader := NewLoader(source)

	graph, err := loader.Load(context.Background())
	require.NoError(t, err)

	assert.Contains(t, graph.Relays, "rel-relay")
	assert.Contains(t, graph.Hosts, "rel-host")
	assert.Contains(t, graph.Services, "rel-svc")
}

func TestLoader_Load_Errors(t *testing.T) {
	t.Run("source fetch error", func(t *testing.T) {
		source := &mockErrorSource{}
		loader := NewLoader(source)
		_, err := loader.Load(context.Background())
		assert.Error(t, err)
	})

	t.Run("bad yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		badFile := filepath.Join(tmpDir, "bad.yaml")
		_ = os.WriteFile(badFile, []byte("key: value: another"), 0644)

		source := NewFileSource(tmpDir)
		loader := NewLoader(source)
		_, err := loader.Load(context.Background())
		assert.Error(t, err)
	})

	t.Run("unknown host reference", func(t *testing.T) {
		tmpDir := t.TempDir()
		relayFile := filepath.Join(tmpDir, "relay.yaml")
		_ = os.WriteFile(relayFile, []byte(`
apiVersion: cic/v1
kind: Relay
metadata:
  name: test-relay
spec:
  hosts:
    - non-existent-host.yaml
`), 0644)

		source := NewFileSource(tmpDir)
		loader := NewLoader(source)
		_, err := loader.Load(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown host file")
	})

	t.Run("unknown service reference", func(t *testing.T) {
		tmpDir := t.TempDir()
		hostFile := filepath.Join(tmpDir, "host.yaml")
		_ = os.WriteFile(hostFile, []byte(`
apiVersion: cic/v1
kind: Host
metadata:
  name: test-host
spec:
  services:
    - non-existent-service.yaml
`), 0644)

		source := NewFileSource(tmpDir)
		loader := NewLoader(source)
		_, err := loader.Load(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown service file")
	})
}

// mockErrorSource always returns an error.
type mockErrorSource struct{}

func (m *mockErrorSource) FetchConfig(ctx context.Context) (map[string][]byte, error) {
	return nil, assert.AnError
}

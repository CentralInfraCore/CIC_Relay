package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	// Disable log output for all tests in this package
	lf := logrus.StandardLogger().Out
	logrus.SetOutput(&bytes.Buffer{})
	code := m.Run()
	logrus.SetOutput(lf)
	os.Exit(code)
}

func TestRun_ServerStart(t *testing.T) {
	// Override the listenAndServe function to immediately return, preventing the server from actually starting.
	originalListenAndServe := listenAndServe
	listenAndServe = func(addr string, handler http.Handler) error {
		return nil
	}
	defer func() { listenAndServe = originalListenAndServe }()

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	// Use a mock API that satisfies the cabinet.Cabinet interface
	mockAPI := new(mockCabinetService)

	server := &relayServer{
		api:    mockAPI,
		config: &config{},
		logger: logger,
	}

	err := run(":8081", logger, server)
	assert.NoError(t, err, "run() should not return an error when server starts successfully")
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Ensure no env vars are set
	os.Clearenv()

	cfg, err := loadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, int64(4*1024*1024), cfg.MaxJSONInputSize)
	assert.Equal(t, int64(16*1024*1024), cfg.MaxWASMModuleSize)
}

func TestNewMux(t *testing.T) {
	server := &relayServer{}
	mux := newMux(server)

	tests := []string{"/", "/set", "/healthz", "/readyz"}

	for _, path := range tests {
		_, pattern := mux.Handler(httptest.NewRequest("GET", path, nil))
		assert.Equal(t, path, pattern, "Expected path %s to be registered", path)
	}
}

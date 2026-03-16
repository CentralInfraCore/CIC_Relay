package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"centralrelay/core/cabinet"
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
	assert.Equal(t, 30, cfg.WasmExecTimeoutSeconds)
}
func TestLoadConfig_AllEnvSet(t *testing.T) {
	t.Setenv("CIC_MAX_JSON_INPUT_SIZE", "8192")
	t.Setenv("CIC_MAX_WASM_MODULE_SIZE", "32768")
	t.Setenv("CIC_HASHING_TIMEOUT", "10s")

	cfg, err := loadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, int64(8192), cfg.MaxJSONInputSize)
	assert.Equal(t, int64(32768), cfg.MaxWASMModuleSize)
	assert.Equal(t, 10*time.Second, cfg.HashingTimeout)
}

func TestLoadConfig_InvalidJSONSize(t *testing.T) {
	t.Setenv("CIC_MAX_JSON_INPUT_SIZE", "notanumber")

	cfg, err := loadConfig()
	assert.Nil(t, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for CIC_MAX_JSON_INPUT_SIZE")
}

func TestLoadConfig_InvalidWASMSize(t *testing.T) {
	t.Setenv("CIC_MAX_WASM_MODULE_SIZE", "oops")

	cfg, err := loadConfig()
	assert.Nil(t, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for CIC_MAX_WASM_MODULE_SIZE")
}

func TestLoadConfig_InvalidHashingTimeout(t *testing.T) {
	t.Setenv("CIC_HASHING_TIMEOUT", "notaduration")

	cfg, err := loadConfig()
	assert.Nil(t, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for CIC_HASHING_TIMEOUT")
}

func TestLoadConfig_WasmExecTimeout_Custom(t *testing.T) {
	t.Setenv("CIC_WASM_EXEC_TIMEOUT_SECONDS", "60")

	cfg, err := loadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, 60, cfg.WasmExecTimeoutSeconds)
}

func TestLoadConfig_WasmExecTimeout_InvalidNotNumber(t *testing.T) {
	t.Setenv("CIC_WASM_EXEC_TIMEOUT_SECONDS", "notanumber")

	cfg, err := loadConfig()
	assert.Nil(t, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for CIC_WASM_EXEC_TIMEOUT_SECONDS")
}

func TestLoadConfig_WasmExecTimeout_InvalidZero(t *testing.T) {
	t.Setenv("CIC_WASM_EXEC_TIMEOUT_SECONDS", "0")

	cfg, err := loadConfig()
	assert.Nil(t, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CIC_WASM_EXEC_TIMEOUT_SECONDS")
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

func TestValidateConfig_Valid(t *testing.T) {
	cfg := &config{
		MaxJSONInputSize:       4 * 1024 * 1024,
		MaxWASMModuleSize:      16 * 1024 * 1024,
		HashingTimeout:         5 * time.Second,
		WasmExecTimeoutSeconds: 30,
	}
	assert.NoError(t, validateConfig(cfg))
}

func TestValidateConfig_RangeViolation_JSONSize(t *testing.T) {
	cfg := &config{
		MaxJSONInputSize:       0,
		MaxWASMModuleSize:      16 * 1024 * 1024,
		HashingTimeout:         5 * time.Second,
		WasmExecTimeoutSeconds: 30,
	}
	assert.Error(t, validateConfig(cfg))
}

func TestValidateConfig_RangeViolation_WASMSize(t *testing.T) {
	cfg := &config{
		MaxJSONInputSize:       4 * 1024 * 1024,
		MaxWASMModuleSize:      0,
		HashingTimeout:         5 * time.Second,
		WasmExecTimeoutSeconds: 30,
	}
	assert.Error(t, validateConfig(cfg))
}

func TestValidateConfig_RangeViolation_HashingTimeout(t *testing.T) {
	cfg := &config{
		MaxJSONInputSize:       4 * 1024 * 1024,
		MaxWASMModuleSize:      16 * 1024 * 1024,
		HashingTimeout:         500 * time.Millisecond,
		WasmExecTimeoutSeconds: 30,
	}
	assert.Error(t, validateConfig(cfg))
}

func TestValidateConfig_RangeViolation_WasmExecTimeout(t *testing.T) {
	cfg := &config{
		MaxJSONInputSize:       4 * 1024 * 1024,
		MaxWASMModuleSize:      16 * 1024 * 1024,
		HashingTimeout:         5 * time.Second,
		WasmExecTimeoutSeconds: 0,
	}
	assert.Error(t, validateConfig(cfg))
}

func TestLoadSecretsFromVault_NoToken(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	secrets := loadSecretsFromVault(logger)
	assert.Nil(t, secrets, "secrets must be nil when VAULT_TOKEN is not set")
}

func TestLoadSecretsFromVault_WithToken_Defaults(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "dev-root-token")
	t.Setenv("CIC_VAULT_MOUNT", "")
	t.Setenv("CIC_VAULT_KEY", "")
	t.Setenv("CIC_AUDIT_REPO_PATH", "")

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	secrets := loadSecretsFromVault(logger)
	assert.NotNil(t, secrets)
	assert.Equal(t, "dev-root-token", secrets.Token)
	assert.Equal(t, "transit", secrets.MountPath)
	assert.Equal(t, "cic-dev-sign-key", secrets.KeyName)
	assert.Equal(t, "/var/lib/cic-relay/audit", secrets.AuditRepoPath)
}

func TestLoadSecretsFromVault_WithToken_CustomValues(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "my-token")
	t.Setenv("CIC_VAULT_MOUNT", "custom-mount")
	t.Setenv("CIC_VAULT_KEY", "custom-key")
	t.Setenv("CIC_AUDIT_REPO_PATH", "/tmp/audit")

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	secrets := loadSecretsFromVault(logger)
	assert.NotNil(t, secrets)
	assert.Equal(t, "my-token", secrets.Token)
	assert.Equal(t, "custom-mount", secrets.MountPath)
	assert.Equal(t, "custom-key", secrets.KeyName)
	assert.Equal(t, "/tmp/audit", secrets.AuditRepoPath)
}

func TestBuildRecorderOption_NoVaultToken(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	opt := buildRecorderOption(logger, nil)
	assert.NotNil(t, opt, "option must not be nil even when Vault is unconfigured")

	// Verify the option can be applied without panic.
	wasmConfig := cabinet.WasmManagerConfig{LRUCacheSize: 1, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(),
		cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		wasmConfig, opt)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
	_ = svc.Close(context.Background())
}

func TestBuildRecorderOption_WithVaultToken_ValidPath(t *testing.T) {
	secrets := &VaultSecrets{
		Token:         "dev-root-token",
		MountPath:     "transit",
		KeyName:       "cic-dev-sign-key",
		AuditRepoPath: t.TempDir(),
	}

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	opt := buildRecorderOption(logger, secrets)
	assert.NotNil(t, opt)

	wasmConfig := cabinet.WasmManagerConfig{LRUCacheSize: 1, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(),
		cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		wasmConfig, opt)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
	_ = svc.Close(context.Background())
}

func TestBuildRecorderOption_WithVaultToken_InvalidPath(t *testing.T) {
	secrets := &VaultSecrets{
		Token:         "dev-root-token",
		MountPath:     "transit",
		KeyName:       "cic-dev-sign-key",
		AuditRepoPath: "/dev/null/nope/nope",
	}

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	opt := buildRecorderOption(logger, secrets)
	assert.NotNil(t, opt, "option must not be nil even when git init fails")

	wasmConfig := cabinet.WasmManagerConfig{LRUCacheSize: 1, DefaultTimeoutSeconds: 1}
	svc, err := cabinet.NewCabinetService(context.Background(),
		cabinet.NewMemSchemaStore(), cabinet.NewMemModuleStore(), cabinet.NewMemWorkflowStore(),
		wasmConfig, opt)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
	_ = svc.Close(context.Background())
}

func TestMockCabinetService_PutSchema(t *testing.T) {
	mockService := new(mockCabinetService)
	schema := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "test-schema", Version: "1.0"}}

	mockService.On("PutSchema", schema).Return(nil).Once()

	err := mockService.PutSchema(schema)
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_PutSchema_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	schema := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: "test-schema", Version: "1.0"}}
	expectedErr := errors.New("failed to put schema")

	mockService.On("PutSchema", schema).Return(expectedErr).Once()

	err := mockService.PutSchema(schema)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetSchema(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-schema-id"
	expectedSchema := &cabinet.Schema{Meta: cabinet.BaseBlock{Name: id, Version: "1.0"}}

	mockService.On("GetSchema", id).Return(expectedSchema, nil).Once()

	schema, err := mockService.GetSchema(id)
	assert.NoError(t, err)
	assert.Equal(t, expectedSchema, schema)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetSchema_NotFound(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "non-existent-schema"

	mockService.On("GetSchema", id).Return(nil, nil).Once()

	schema, err := mockService.GetSchema(id)
	assert.NoError(t, err)
	assert.Nil(t, schema)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetSchema_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-schema-id"
	expectedErr := errors.New("failed to get schema")

	mockService.On("GetSchema", id).Return(nil, expectedErr).Once()

	schema, err := mockService.GetSchema(id)
	assert.Error(t, err)
	assert.Nil(t, schema)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_PutModule(t *testing.T) {
	mockService := new(mockCabinetService)
	module := &cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: "test-module", Version: "1.0"}}

	mockService.On("PutModule", module).Return(nil).Once()

	err := mockService.PutModule(module)
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_PutModule_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	module := &cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: "test-module", Version: "1.0"}}
	expectedErr := errors.New("failed to put module")

	mockService.On("PutModule", module).Return(expectedErr).Once()

	err := mockService.PutModule(module)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetModule(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-module-id"
	expectedModule := &cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: id, Version: "1.0"}}

	mockService.On("GetModule", id).Return(expectedModule, nil).Once()

	module, err := mockService.GetModule(id)
	assert.NoError(t, err)
	assert.Equal(t, expectedModule, module)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetModule_NotFound(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "non-existent-module"

	mockService.On("GetModule", id).Return(nil, nil).Once()

	module, err := mockService.GetModule(id)
	assert.NoError(t, err)
	assert.Nil(t, module)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetModule_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-module-id"
	expectedErr := errors.New("failed to get module")

	mockService.On("GetModule", id).Return(nil, expectedErr).Once()

	module, err := mockService.GetModule(id)
	assert.Error(t, err)
	assert.Nil(t, module)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_PutWorkflow(t *testing.T) {
	mockService := new(mockCabinetService)
	workflow := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: "test-workflow", Version: "1.0"}}

	mockService.On("PutWorkflow", workflow).Return(nil).Once()

	err := mockService.PutWorkflow(workflow)
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_PutWorkflow_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	workflow := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: "test-workflow", Version: "1.0"}}
	expectedErr := errors.New("failed to put workflow")

	mockService.On("PutWorkflow", workflow).Return(expectedErr).Once()

	err := mockService.PutWorkflow(workflow)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetWorkflow(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-workflow-id"
	expectedWorkflow := &cabinet.Workflow{Meta: cabinet.BaseBlock{Name: id, Version: "1.0"}}

	mockService.On("GetWorkflow", id).Return(expectedWorkflow, nil).Once()

	workflow, err := mockService.GetWorkflow(id)
	assert.NoError(t, err)
	assert.Equal(t, expectedWorkflow, workflow)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetWorkflow_NotFound(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "non-existent-workflow"

	mockService.On("GetWorkflow", id).Return(nil, nil).Once()

	workflow, err := mockService.GetWorkflow(id)
	assert.NoError(t, err)
	assert.Nil(t, workflow)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_GetWorkflow_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	id := "test-workflow-id"
	expectedErr := errors.New("failed to get workflow")

	mockService.On("GetWorkflow", id).Return(nil, expectedErr).Once()

	workflow, err := mockService.GetWorkflow(id)
	assert.Error(t, err)
	assert.Nil(t, workflow)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_Set(t *testing.T) {
	mockService := new(mockCabinetService)
	ctx := context.Background()
	payload := &cabinet.SetPayload{}
	expectedResponse := &cabinet.SetResponse{} // Removed Success field

	mockService.On("Setx", ctx, payload).Return(expectedResponse, nil).Once()

	response, err := mockService.Setx(ctx, payload)
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_Set_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	ctx := context.Background()
	payload := &cabinet.SetPayload{}
	expectedErr := errors.New("failed to set payload")

	mockService.On("Setx", ctx, payload).Return(nil, expectedErr).Once()

	response, err := mockService.Setx(ctx, payload)
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_Close(t *testing.T) {
	mockService := new(mockCabinetService)
	ctx := context.Background()

	mockService.On("Close", ctx).Return(nil).Once()

	err := mockService.Close(ctx)
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestMockCabinetService_Close_Error(t *testing.T) {
	mockService := new(mockCabinetService)
	ctx := context.Background()
	expectedErr := errors.New("failed to close")

	mockService.On("Close", ctx).Return(expectedErr).Once()

	err := mockService.Close(ctx)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	mockService.AssertExpectations(t)
}

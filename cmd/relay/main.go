package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus" // Import logrus

	"centralrelay/core/cabinet"
	"centralrelay/core/nexus/crypto"
	"centralrelay/core/nexus/recorder"
)

// Build metadata — injected by Makefile ldflags:
//
//	-X main.BuildID=$(VERSION)-$(COMMIT)
//	-X main.CommitHash=$(COMMIT)
//	-X main.Timestamp=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
var (
	BuildID    = "dev"
	CommitHash = "unknown"
	Timestamp  = "unknown"
)

// test seam: unit tesztben felülírható
var listenAndServe = http.ListenAndServe

// config holds the application's configuration.
type config struct {
	MaxJSONInputSize  int64
	MaxWASMModuleSize int64
	HashingTimeout    time.Duration // Added for M4
}

// loadConfig loads configuration from environment variables.
func loadConfig() (*config, error) {
	cfg := &config{
		MaxJSONInputSize:  4 * 1024 * 1024,           // Default to 4 MB
		MaxWASMModuleSize: cabinet.MaxWASMModuleSize,  // Single source: cabinet package constant
		HashingTimeout:    5 * time.Second,            // Default to 5 seconds
	}

	if val := os.Getenv("CIC_MAX_JSON_INPUT_SIZE"); val != "" {
		size, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_MAX_JSON_INPUT_SIZE: %w", err)
		}
		cfg.MaxJSONInputSize = size
	}

	if val := os.Getenv("CIC_MAX_WASM_MODULE_SIZE"); val != "" {
		size, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_MAX_WASM_MODULE_SIZE: %w", err)
		}
		cfg.MaxWASMModuleSize = size
	}

	if val := os.Getenv("CIC_HASHING_TIMEOUT"); val != "" {
		timeout, err := time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_HASHING_TIMEOUT: %w", err)
		}
		cfg.HashingTimeout = timeout
	}

	return cfg, nil
}

// relayServer holds the dependencies for our HTTP handlers.
type relayServer struct {
	api    cabinet.Cabinet // Use the interface type
	config *config
	logger *logrus.Logger // Use logrus logger
}

// writeError writes a uniform JSON error response: {"error":"<msg>","code":"<code>"}.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

// newMux creates a new HTTP multiplexer with all routes.
func newMux(s *relayServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.rootHandler)
	mux.HandleFunc("/set", s.setHandler)
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/readyz", s.readyzHandler)
	return mux
}

// rootHandler serves the root path.
func (s *relayServer) rootHandler(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling root request")

	w.Header().Set("Content-Type", "application/json")
	response := `{"status": "ok", "message": "central relay responding"}`
	fmt.Fprintln(w, response)
}

// healthzHandler serves the health check endpoint.
func (s *relayServer) healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// readyzHandler serves the readiness check endpoint.
func (s *relayServer) readyzHandler(w http.ResponseWriter, r *http.Request) {
	// For now, readiness is the same as liveness.
	// This can be expanded to check dependencies like the cabinet service.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// setHandler processes incoming SetPayload requests.
func (s *relayServer) setHandler(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()
	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling set request")

	if r.Method != http.MethodPost {
		logEntry.WithField("outcome", "method_not_allowed").Warn("Only POST method is allowed")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST method is allowed")
		return
	}

	// Limit the size of the request body to prevent abuse.
	// We add 1 to the limit to detect if the body is *exactly* at the limit or exceeds it.
	limitedReader := io.LimitReader(r.Body, s.config.MaxJSONInputSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		logEntry.WithField("outcome", "read_error").WithError(err).Error("Failed to read request body")
		writeError(w, http.StatusInternalServerError, "read_error", "Failed to read request")
		return
	}
	defer r.Body.Close() // Ensure the original body is closed

	// If the body length is greater than the allowed size, return 413.
	if int64(len(body)) > s.config.MaxJSONInputSize {
		logEntry.WithFields(logrus.Fields{
			"outcome": "body_too_large",
			"size":    len(body),
			"limit":   s.config.MaxJSONInputSize,
		}).Warn("Request body too large")
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("Request body too large, exceeds %d bytes", s.config.MaxJSONInputSize))
		return
	}

	var payload cabinet.SetPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logEntry.WithField("outcome", "invalid_json").WithError(err).Warn("Invalid JSON payload")
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error())
		return
	}

	resp, err := s.api.Setx(r.Context(), &payload)
	if err != nil {
		if cabinet.IsTimeout(err) {
			logEntry.WithField("outcome", "timeout").WithError(err).Warn("Workflow execution timed out")
			writeError(w, http.StatusGatewayTimeout, "timeout", "Workflow execution timed out: "+err.Error())
			return
		}
		if cabinet.IsInvalid(err) {
			logEntry.WithField("outcome", "invalid_request").WithError(err).Warn("Invalid request")
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request: "+err.Error())
			return
		}
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Internal server error during Set operation")
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error: "+err.Error())
		return
	}

	logEntry.WithField("outcome", "success").Info("Set operation successful")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// validateConfig validates the loaded configuration using the cabinet schema engine.
// This is "dogfooding": the relay validates its own config with the same mechanism
// it uses to validate workflow inputs (cabinet.ValidateSchema / $schema key check).
func validateConfig(cfg *config) error {
	configMap := map[string]interface{}{
		"$schema":               "relay.config@1.0",
		"max_json_input_size":   cfg.MaxJSONInputSize,
		"max_wasm_module_size":  cfg.MaxWASMModuleSize,
		"hashing_timeout":       cfg.HashingTimeout.String(),
	}
	return cabinet.ValidateSchema([]string{"relay.config@1.0"}, configMap)
}

// VaultSecrets holds Vault connection parameters loaded from the environment.
type VaultSecrets struct {
	Token         string
	MountPath     string
	KeyName       string
	AuditRepoPath string
}

// loadSecretsFromVault reads Vault-related parameters from environment variables.
// Returns nil if VAULT_TOKEN is not set — Vault is not configured, recording disabled (non-fatal).
// Required env: VAULT_TOKEN (also used by the Vault SDK), VAULT_ADDR (Vault SDK).
// Optional env: CIC_VAULT_MOUNT (default "transit"), CIC_VAULT_KEY (default "cic-dev-sign-key"),
//
//	CIC_AUDIT_REPO_PATH (default "/var/lib/cic-relay/audit").
func loadSecretsFromVault(logger *logrus.Logger) *VaultSecrets {
	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		logger.Warn("VAULT_TOKEN not set — ProofTrace recording disabled")
		return nil
	}

	mountPath := os.Getenv("CIC_VAULT_MOUNT")
	if mountPath == "" {
		mountPath = "transit"
	}
	keyName := os.Getenv("CIC_VAULT_KEY")
	if keyName == "" {
		keyName = "cic-dev-sign-key"
	}
	auditRepoPath := os.Getenv("CIC_AUDIT_REPO_PATH")
	if auditRepoPath == "" {
		auditRepoPath = "/var/lib/cic-relay/audit"
	}

	return &VaultSecrets{
		Token:         token,
		MountPath:     mountPath,
		KeyName:       keyName,
		AuditRepoPath: auditRepoPath,
	}
}

// buildRecorderOption constructs a cabinet.WithRecorder option from the provided VaultSecrets.
// If secrets is nil (Vault not configured), returns a no-op option.
func buildRecorderOption(logger *logrus.Logger, secrets *VaultSecrets) cabinet.CabinetOption {
	if secrets == nil {
		return cabinet.WithRecorder(nil)
	}

	cryptoSvc, err := crypto.NewVaultCryptoService(secrets.MountPath, secrets.KeyName)
	if err != nil {
		logger.WithError(err).Warn("Failed to init Vault crypto service — ProofTrace recording disabled")
		return cabinet.WithRecorder(nil)
	}

	gitRecorder, err := recorder.NewGitStateRecorder(secrets.AuditRepoPath, cryptoSvc)
	if err != nil {
		logger.WithError(err).Warn("Failed to init audit git recorder — ProofTrace recording disabled")
		return cabinet.WithRecorder(nil)
	}

	wr := recorder.NewWorkflowRecorder(gitRecorder)
	logger.WithFields(logrus.Fields{
		"vault_mount":     secrets.MountPath,
		"vault_key":       secrets.KeyName,
		"audit_repo_path": secrets.AuditRepoPath,
	}).Info("ProofTrace recording enabled")
	return cabinet.WithRecorder(wr)
}

// run: külön függvény, hogy unit tesztben ne main()-t kelljen hívni
func run(port string, logger *logrus.Logger, s *relayServer) error {
	logger.Printf("✅ Central relay API started on %s", port) // This will use logrus's Printf
	return listenAndServe(port, newMux(s))
}

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{}) // Structured logging in JSON format

	logger.WithFields(logrus.Fields{
		"build_id":   BuildID,
		"commit":     CommitHash,
		"build_time": Timestamp,
	}).Info("Starting cic-relay")

	cfg, err := loadConfig()
	if err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load configuration")
	}
	logger.WithFields(logrus.Fields{
		"MaxJSONInputSize":  cfg.MaxJSONInputSize,
		"MaxWASMModuleSize": cfg.MaxWASMModuleSize,
		"HashingTimeout":    cfg.HashingTimeout,
	}).Info("Loaded configuration")

	if err := validateConfig(cfg); err != nil {
		logger.WithError(err).Fatal("FATAL: Configuration validation failed")
	}
	logger.Info("Configuration validated (relay.config@1.0)")

	// --- Cabinet Service Initialization ---
	instanceID := uuid.New().String()
	logger.WithField("instance_id", instanceID).Info("Instance ID")

	logger.Info("Initializing cabinet stores...")
	schemaStore := cabinet.NewMemSchemaStore()
	workflowStore := cabinet.NewMemWorkflowStore()
	moduleStore := cabinet.NewMemModuleStore()

	wasmConfig := cabinet.WasmManagerConfig{
		LRUCacheSize:          100, // Default cache size
		DefaultTimeoutSeconds: 5,   // Default timeout
	}
	secrets := loadSecretsFromVault(logger)
	recorderOpt := buildRecorderOption(logger, secrets)

	cabinetSvc, err := cabinet.NewCabinetService(context.Background(), schemaStore, moduleStore, workflowStore, wasmConfig, recorderOpt)
	if err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to create cabinet service")
	}
	defer cabinetSvc.Close(context.Background()) // Ensure WASM runtime is closed
	logger.Info("Cabinet service initialized.")

	// --- Component Loading ---
	// Create a context with a timeout for all loading/hashing operations.
	loadCtx, cancel := context.WithTimeout(context.Background(), cfg.HashingTimeout)
	defer cancel()

	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	// For now, it's hardcoded to /etc/cic-relay/components.d
	if err := loadInstalledComponents(loadCtx, cabinetSvc, "/etc/cic-relay/components.d", "", cfg.MaxWASMModuleSize, logger); err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load installed components")
	}

	// --- HTTP Server Initialization ---
	server := &relayServer{api: cabinetSvc, config: cfg, logger: logger} // Pass the service which implements the interface

	port := ":8080"
	logger.Fatal(run(port, logger, server)) // Pass logrus logger to run function
}

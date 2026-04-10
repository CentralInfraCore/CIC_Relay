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
	"centralrelay/core/nexus/iac"
	"centralrelay/core/nexus/isolation"
	"centralrelay/core/nexus/operator"
	"centralrelay/core/nexus/recorder"
	"centralrelay/pkg/obs"
)

// Build metadata — injected by Makefile ldflags:
//
//	-X main.BuildID=$(VERSION)-$(COMMIT)
//	-X main.CommitHash=$(COMMIT)
//	-X main.Timestamp=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
//	-X main.SourceTreeHash=$(shell go run ./tools/sourcehash .)  [release/build targets]
var (
	BuildID        = "dev"
	CommitHash     = "unknown"
	Timestamp      = "unknown"
	SourceTreeHash = "unknown"
)

// test seam: unit tesztben felülírható
var listenAndServe = func(srv *http.Server) error {
	return srv.ListenAndServe()
}

// config holds the application's configuration.
type config struct {
	MaxJSONInputSize       int64
	MaxWASMModuleSize      int64
	HashingTimeout         time.Duration
	WasmExecTimeoutSeconds int
	RateLimitRPS           int
	MaxConcurrentRequests  int
}

// loadConfig loads configuration from environment variables.
func loadConfig() (*config, error) {
	cfg := &config{
		MaxJSONInputSize:       4 * 1024 * 1024,          // Default to 4 MB
		MaxWASMModuleSize:      cabinet.MaxWASMModuleSize, // Single source: cabinet package constant
		HashingTimeout:         5 * time.Second,           // Default to 5 seconds
		WasmExecTimeoutSeconds: 30,                        // Default WASM execution timeout
		RateLimitRPS:           100,                       // Default rate limit
		MaxConcurrentRequests:  50,                        // Default concurrent cap
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

	if val := os.Getenv("CIC_WASM_EXEC_TIMEOUT_SECONDS"); val != "" {
		secs, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_WASM_EXEC_TIMEOUT_SECONDS: %w", err)
		}
		if secs < 1 {
			return nil, fmt.Errorf("invalid value for CIC_WASM_EXEC_TIMEOUT_SECONDS: must be >= 1, got %d", secs)
		}
		cfg.WasmExecTimeoutSeconds = secs
	}

	if val := os.Getenv("CIC_RATE_LIMIT_RPS"); val != "" {
		rps, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_RATE_LIMIT_RPS: %w", err)
		}
		if rps < 1 {
			return nil, fmt.Errorf("invalid value for CIC_RATE_LIMIT_RPS: must be >= 1, got %d", rps)
		}
		cfg.RateLimitRPS = rps
	}

	if val := os.Getenv("CIC_MAX_CONCURRENT_REQUESTS"); val != "" {
		max, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid value for CIC_MAX_CONCURRENT_REQUESTS: %w", err)
		}
		if max < 1 {
			return nil, fmt.Errorf("invalid value for CIC_MAX_CONCURRENT_REQUESTS: must be >= 1, got %d", max)
		}
		cfg.MaxConcurrentRequests = max
	}

	return cfg, nil
}

// relayServer holds the dependencies for our HTTP handlers.
type relayServer struct {
	api    cabinet.Cabinet // Use the interface type
	config *config
	logger *logrus.Logger // Use logrus logger
	// obs sinks — nil-safe: handlers use emitTrace/emitLog/emitCounter helpers.
	// Inject obs.NoopSink{} in production; obs.CaptureSink in tests.
	traceSink   obs.TraceSink
	logSink     obs.LogSink
	metricSink  obs.MetricSink
	obsResource obs.Resource // self-identification for obs records
}

// emitTrace emits a TraceFrame if a TraceSink is configured.
func (s *relayServer) emitTrace(f obs.TraceFrame) {
	if s.traceSink != nil {
		s.traceSink.RecordTrace(f)
	}
}

// emitLog emits a LogRecord if a LogSink is configured.
func (s *relayServer) emitLog(r obs.LogRecord) {
	if s.logSink != nil {
		s.logSink.RecordLog(r)
	}
}

// emitCounter emits a CounterPoint if a MetricSink is configured.
func (s *relayServer) emitCounter(p obs.CounterPoint) {
	if s.metricSink != nil {
		s.metricSink.RecordCounter(p)
	}
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
	mux.HandleFunc("/v1/schema/compile", s.compileHandler)
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
	start := time.Now()
	traceStatus := "ok"
	var workflowID string

	defer func() {
		endTS := time.Now().UTC().Format(time.RFC3339Nano)
		s.emitTrace(obs.TraceFrame{
			TraceID:   reqID,
			SpanID:    reqID,
			Name:      "relay.set",
			StartTime: start.UTC().Format(time.RFC3339Nano),
			EndTime:   endTS,
			Resource:  s.obsResource,
			Attrs:     map[string]string{"status": traceStatus, "workflow_id": workflowID},
		})
		s.emitCounter(obs.CounterPoint{
			Name:      "relay.requests.total",
			Value:     1,
			Timestamp: endTS,
			Resource:  s.obsResource,
			Attrs:     map[string]string{"handler": "set", "status": traceStatus},
		})
	}()

	logEntry := s.logger.WithFields(logrus.Fields{
		"req-id": reqID,
		"path":   r.URL.Path,
		"method": r.Method,
	})
	logEntry.Info("Handling set request")

	if r.Method != http.MethodPost {
		traceStatus = "rejected"
		logEntry.WithField("outcome", "method_not_allowed").Warn("Only POST method is allowed")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST method is allowed")
		return
	}

	// Limit the size of the request body to prevent abuse.
	// We add 1 to the limit to detect if the body is *exactly* at the limit or exceeds it.
	limitedReader := io.LimitReader(r.Body, s.config.MaxJSONInputSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		traceStatus = "error"
		logEntry.WithField("outcome", "read_error").WithError(err).Error("Failed to read request body")
		writeError(w, http.StatusInternalServerError, "read_error", "Failed to read request")
		return
	}
	defer r.Body.Close() // Ensure the original body is closed

	// If the body length is greater than the allowed size, return 413.
	if int64(len(body)) > s.config.MaxJSONInputSize {
		traceStatus = "rejected"
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
		traceStatus = "rejected"
		logEntry.WithField("outcome", "invalid_json").WithError(err).Warn("Invalid JSON payload")
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error())
		return
	}
	workflowID = payload.WorkflowID

	// Pin the chain to the build-time source tree hash so the ProofTrace is
	// anchored to the full build-input boundary, not just the commit pointer.
	// SourceTreeHash covers all tracked source files (pkg/sourcedigest) and is
	// injected at build time via -X main.SourceTreeHash.  Falls back to
	// CommitHash in dev builds where the ldflag was not applied.
	if payload.SourceDigest == "" {
		if SourceTreeHash != "unknown" {
			payload.SourceDigest = SourceTreeHash
		} else {
			payload.SourceDigest = CommitHash
		}
	}

	// Route through API.Set() to enforce ValidateSet (workflow exists, modules resolvable,
	// options.priority valid) before delegating to Cabinet.Setx().
	resp, err := cabinet.NewAPI(s.api).Set(r.Context(), &payload)
	if err != nil {
		if cabinet.IsTimeout(err) {
			traceStatus = "timeout"
			logEntry.WithField("outcome", "timeout").WithError(err).Warn("Workflow execution timed out")
			writeError(w, http.StatusGatewayTimeout, "timeout", "Workflow execution timed out: "+err.Error())
			return
		}
		if cabinet.IsInvalid(err) {
			traceStatus = "rejected"
			logEntry.WithField("outcome", "invalid_request").WithError(err).Warn("Invalid request")
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request: "+err.Error())
			return
		}
		traceStatus = "error"
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Internal server error during Set operation")
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error: "+err.Error())
		return
	}

	s.emitLog(obs.LogRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     "info",
		Message:   "relay.set.completed",
		Resource:  s.obsResource,
		ExecCtx:   &obs.ExecutionContext{WorkflowID: workflowID, SourceDigest: payload.SourceDigest},
		Attrs:     map[string]string{"req_id": reqID},
	})

	logEntry.WithField("outcome", "success").Info("Set operation successful")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// validateConfig validates the loaded configuration using the cabinet schema engine.
// This is "dogfooding": the relay validates its own config with the same mechanism
// it uses to validate workflow inputs (cabinet.ValidateSchema / $schema key check).
// Range constraints are also enforced directly before the schema ID check.
func validateConfig(cfg *config) error {
	if cfg.MaxJSONInputSize < 1 {
		return fmt.Errorf("max_json_input_size must be >= 1, got %d", cfg.MaxJSONInputSize)
	}
	if cfg.MaxWASMModuleSize < 1 {
		return fmt.Errorf("max_wasm_module_size must be >= 1, got %d", cfg.MaxWASMModuleSize)
	}
	if cfg.HashingTimeout < time.Second {
		return fmt.Errorf("hashing_timeout must be >= 1s, got %s", cfg.HashingTimeout)
	}
	if cfg.WasmExecTimeoutSeconds < 1 {
		return fmt.Errorf("wasm_exec_timeout_seconds must be >= 1, got %d", cfg.WasmExecTimeoutSeconds)
	}
	if cfg.RateLimitRPS < 1 {
		return fmt.Errorf("rate_limit_rps must be >= 1, got %d", cfg.RateLimitRPS)
	}
	if cfg.MaxConcurrentRequests < 1 {
		return fmt.Errorf("max_concurrent_requests must be >= 1, got %d", cfg.MaxConcurrentRequests)
	}
	configMap := map[string]interface{}{
		"$schema":                   "relay.config@1.0",
		"max_json_input_size":       cfg.MaxJSONInputSize,
		"max_wasm_module_size":      cfg.MaxWASMModuleSize,
		"hashing_timeout":           cfg.HashingTimeout.String(),
		"wasm_exec_timeout_seconds": cfg.WasmExecTimeoutSeconds,
		"rate_limit_rps":            cfg.RateLimitRPS,
		"max_concurrent_requests":   cfg.MaxConcurrentRequests,
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

// buildOperatorIfConfigured constructs and returns an Operator if CIC_INFRA_REPO_PATH is set.
// Returns nil if CIC_INFRA_REPO_PATH is not set (operator is optional).
// Calls logger.Fatal if CIC_INFRA_REPO_PATH is set but CIC_OPERATOR_SECRET is missing.
//
// The operator is fully wired:
//   - IaCValidator → Cabinet.GetSchema (schema-bound resource validation)
//   - CabinetActivator → Cabinet.Setx (execution bridge)
//   - IaC graph loaded upfront for dependency gating (WithGraph)
func buildOperatorIfConfigured(ctx context.Context, cabinetSvc cabinet.Cabinet, logger *logrus.Logger) *operator.Operator {
	infraPath := os.Getenv("CIC_INFRA_REPO_PATH")
	if infraPath == "" {
		logger.Warn("CIC_INFRA_REPO_PATH not set — Operator not started")
		return nil
	}

	secret := os.Getenv("CIC_OPERATOR_SECRET")
	if secret == "" {
		logger.Fatal("FATAL: CIC_INFRA_REPO_PATH is set but CIC_OPERATOR_SECRET is missing — cannot generate bootstrap tokens")
	}

	// Select IaC source: GitSource if CIC_GIT_SOURCE_URL is set, else FileSource.
	var iacSource iac.IaCSource
	if gitURL := os.Getenv("CIC_GIT_SOURCE_URL"); gitURL != "" {
		localPath := os.Getenv("CIC_GIT_SOURCE_LOCAL_PATH")
		if localPath == "" {
			logger.Fatal("FATAL: CIC_GIT_SOURCE_URL is set but CIC_GIT_SOURCE_LOCAL_PATH is missing")
		}
		branch := os.Getenv("CIC_GIT_SOURCE_BRANCH")
		if branch == "" {
			branch = "main"
		}
		iacSource = iac.NewGitSource(iac.GitSourceConfig{
			URL:       gitURL,
			Branch:    branch,
			LocalPath: localPath,
		})
	} else {
		iacSource = iac.NewFileSource(infraPath)
	}

	// Load the full IaC graph for dependency gating.
	graph, err := iac.NewLoader(iacSource).Load(ctx)
	if err != nil {
		logger.WithError(err).Warn("IaC graph load failed — operator will run without dependency gating")
		graph = nil
	}

	watcher := operator.NewPollingWatcher(infraPath, 30*time.Second)
	b := operator.NewBootstrapper([]byte(secret), nil, logger)
	reg := operator.NewHostRegistry()
	lm := operator.NewLifecycleManager(b, reg, nil, logger)

	op := operator.NewOperatorWithLifecycle(watcher, b, lm, logger).
		WithValidator(iac.NewIaCValidator(cabinetSvc)).
		WithActivator(NewCabinetActivator(cabinetSvc, logger))

	if graph != nil {
		op = op.WithGraph(graph)
	}

	logger.WithFields(logrus.Fields{
		"infra_repo_path": infraPath,
		"graph_loaded":    graph != nil,
	}).Info("Operator configured")
	return op
}

// run: külön függvény, hogy unit tesztben ne main()-t kelljen hívni
func run(port string, logger *logrus.Logger, s *relayServer) error {
	logger.Printf("✅ Central relay API started on %s", port)
	srv := &http.Server{
		Addr:              port,
		Handler:           buildHandler(s),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return listenAndServe(srv)
}

func main() {
	// Worker mode: subprocess IPC loop (CIC_ISOLATION_MODE=subprocess).
	// When the relay is spawned as a worker by SubprocessWorker it receives
	// --mode=module-worker and runs the IPC loop instead of the HTTP server.
	for _, arg := range os.Args[1:] {
		if arg == "--mode=module-worker" {
			isolation.RunWorkerLoop(context.Background(), os.Stdin, os.Stdout, nil)
			return
		}
	}

	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{}) // Structured logging in JSON format

	logger.WithFields(logrus.Fields{
		"build_id":         BuildID,
		"commit":           CommitHash,
		"build_time":       Timestamp,
		"source_tree_hash": SourceTreeHash,
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
		LRUCacheSize:          100,
		DefaultTimeoutSeconds: uint32(cfg.WasmExecTimeoutSeconds),
	}
	secrets := loadSecretsFromVault(logger)
	recorderOpt := buildRecorderOption(logger, secrets)

	logrusSink := obs.NewLogrusSink(logger)
	obsOpt := cabinet.WithObsSinks(logrusSink, logrusSink)
	cabinetSvc, err := cabinet.NewCabinetService(context.Background(), schemaStore, moduleStore, workflowStore, wasmConfig, recorderOpt, obsOpt)
	if err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to create cabinet service")
	}
	defer cabinetSvc.Close(context.Background()) // Ensure WASM runtime is closed
	logger.Info("Cabinet service initialized.")

	// --- Component Loading ---
	// Create a context with a timeout for all loading/hashing operations.
	loadCtx, cancel := context.WithTimeout(context.Background(), cfg.HashingTimeout)
	defer cancel()

	// --- PKI Trust Bootstrap ---
	trustMode := resolveTrustMode(logger)
	pkiAssets, err := bootstrapPKI(trustMode, embeddedFS, logger)
	if err != nil {
		logger.WithError(err).Fatal("FATAL: PKI bootstrap failed")
	}
	logger.WithFields(logrus.Fields{
		"trust_mode":         trustMode,
		"trust_store_loaded": pkiAssets.TrustStoreLoaded,
	}).Info("PKI trust store ready")

	// Register built-in native modules first (core.test, ci.build, …).
	if err := registerNativeComponents(loadCtx, cabinetSvc, pkiAssets); err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to register native components")
	}
	logger.Info("Native components registered.")

	// Load embedded schemas and workflows.
	if err := loadEmbeddedComponents(loadCtx, cabinetSvc, embeddedFS, embeddedFS, embeddedFS, "embedded/pki", cfg.MaxWASMModuleSize, logger); err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load embedded components")
	}

	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	// For now, it's hardcoded to /etc/cic-relay/components.d
	if err := loadInstalledComponents(loadCtx, cabinetSvc, "/etc/cic-relay/components.d", pkiAssets, cfg.MaxWASMModuleSize, logger); err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load installed components")
	}

	// --- Operator Initialization (optional) ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if op := buildOperatorIfConfigured(ctx, cabinetSvc, logger); op != nil {
		go func() {
			if err := op.Run(ctx); err != nil {
				logger.WithError(err).Warn("operator exited with error")
			}
		}()
	}

	// --- HTTP Server Initialization ---
	// logrusSink is already declared above (wired into cabinetSvc via WithObsSinks).
	// Reuse the same sink for the relayServer obs fields.
	server := &relayServer{
		api:        cabinetSvc,
		config:     cfg,
		logger:     logger,
		traceSink:  logrusSink,
		logSink:    logrusSink,
		metricSink: logrusSink,
		obsResource: obs.Resource{
			RelayID:   instanceID,
			Component: "cmd/relay",
		},
	}

	port := ":8080"
	logger.Fatal(run(port, logger, server)) // Pass logrus logger to run function
}

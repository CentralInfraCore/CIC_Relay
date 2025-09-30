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
		MaxJSONInputSize:  4 * 1024 * 1024,  // Default to 4 MB
		MaxWASMModuleSize: 16 * 1024 * 1024, // Default to 16 MB
		HashingTimeout:    5 * time.Second,      // Default to 5 seconds
	}

	if val := os.Getenv("MAX_JSON_INPUT_SIZE"); val != "" {
		size, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for MAX_JSON_INPUT_SIZE: %w", err)
		}
		cfg.MaxJSONInputSize = size
	}

	if val := os.Getenv("MAX_WASM_MODULE_SIZE"); val != "" {
		size, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for MAX_WASM_MODULE_SIZE: %w", err)
		}
		cfg.MaxWASMModuleSize = size
	}

	if val := os.Getenv("HASHING_TIMEOUT"); val != "" {
		timeout, err := time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid value for HASHING_TIMEOUT: %w", err)
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
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit the size of the request body to prevent abuse.
	// We add 1 to the limit to detect if the body is *exactly* at the limit or exceeds it.
	limitedReader := io.LimitReader(r.Body, s.config.MaxJSONInputSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		logEntry.WithField("outcome", "read_error").WithError(err).Error("Failed to read request body")
		http.Error(w, "Failed to read request", http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("Request body too large, exceeds %d bytes", s.config.MaxJSONInputSize), http.StatusRequestEntityTooLarge)
		return
	}

	var payload cabinet.SetPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logEntry.WithField("outcome", "invalid_json").WithError(err).Warn("Invalid JSON payload")
		http.Error(w, "Invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := s.api.Set(r.Context(), &payload)
	if err != nil {
		// Handle specific cabinet errors
		if cabinet.IsInvalid(err) {
			logEntry.WithField("outcome", "invalid_request").WithError(err).Warn("Invalid request")
			http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		logEntry.WithField("outcome", "internal_error").WithError(err).Error("Internal server error during Set operation")
		http.Error(w, "Internal server error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	logEntry.WithField("outcome", "success").Info("Set operation successful")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// run: külön függvény, hogy unit tesztben ne main()-t kelljen hívni
func run(port string, logger *logrus.Logger, s *relayServer) error {
	logger.Printf("✅ Central relay API started on %s", port) // This will use logrus's Printf
	return listenAndServe(port, newMux(s))
}

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{}) // Structured logging in JSON format

	cfg, err := loadConfig()
	if err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load configuration")
	}
	logger.WithFields(logrus.Fields{
		"MaxJSONInputSize":  cfg.MaxJSONInputSize,
		"MaxWASMModuleSize": cfg.MaxWASMModuleSize,
		"HashingTimeout":    cfg.HashingTimeout,
	}).Info("Loaded configuration")

	// --- Cabinet Service Initialization ---
	instanceID := uuid.New().String()
	logger.WithField("instance_id", instanceID).Info("Instance ID")

	logger.Info("Initializing cabinet stores...")
	schemaStore := cabinet.NewMemSchemaStore()
	workflowStore := cabinet.NewMemWorkflowStore()
	moduleStore := cabinet.NewMemModuleStore()

	cabinetSvc := cabinet.NewCabinetService(schemaStore, moduleStore, workflowStore)
	defer cabinetSvc.Close(context.Background()) // Ensure WASM runtime is closed
	logger.Info("Cabinet service initialized.")

	// --- Component Loading ---
	// Create a context with a timeout for all loading/hashing operations.
	loadCtx, cancel := context.WithTimeout(context.Background(), cfg.HashingTimeout)
	defer cancel()

	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	// For now, it's hardcoded to /etc/cic-relay/components.d
	if err := loadInstalledComponents(loadCtx, cabinetSvc, "/etc/cic-relay/components.d", cfg.MaxWASMModuleSize, logger); err != nil {
		logger.WithField("error", err).Fatal("FATAL: Failed to load installed components")
	}

	// --- HTTP Server Initialization ---
	server := &relayServer{api: cabinetSvc, config: cfg, logger: logger} // Pass the service which implements the interface

	port := ":8080"
	logger.Fatal(run(port, logger, server)) // Pass logrus logger to run function
}

package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"centralrelay/core/cabinet"
)

//go:embed embedded/schemas/*.yaml
var embeddedSchemas embed.FS

//go:embed embedded/workflows/*.yaml
var embeddedWorkflows embed.FS

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
	api    *cabinet.API
	config *config
}

// newMux creates a new HTTP multiplexer with all routes.
func newMux(s *relayServer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.rootHandler)
	mux.HandleFunc("/set", s.setHandler)
	return mux
}

// rootHandler serves the root path.
func (s *relayServer) rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := `{"status": "ok", "message": "central relay responding"}`
	fmt.Fprintln(w, response)
}

// setHandler processes incoming SetPayload requests.
func (s *relayServer) setHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit the size of the request body to prevent abuse.
	// We add 1 to the limit to detect if the body is *exactly* at the limit or exceeds it.
	limitedReader := io.LimitReader(r.Body, s.config.MaxJSONInputSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		http.Error(w, "Failed to read request", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close() // Ensure the original body is closed

	// If the body length is greater than the allowed size, return 413.
	if int64(len(body)) > s.config.MaxJSONInputSize {
		http.Error(w, fmt.Sprintf("Request body too large, exceeds %d bytes", s.config.MaxJSONInputSize), http.StatusRequestEntityTooLarge)
		return
	}

	var payload cabinet.SetPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := s.api.Set(r.Context(), &payload)
	if err != nil {
		// Handle specific cabinet errors
		if cabinet.IsInvalid(err) {
			http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "Internal server error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// run: külön függvény, hogy unit tesztben ne main()-t kelljen hívni
func run(port string, logger *log.Logger, s *relayServer) error {
	logger.Printf("✅ Central relay API started on %s", port)
	return listenAndServe(port, newMux(s))
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("FATAL: Failed to load configuration: %v", err)
	}
	log.Printf("Loaded configuration: MaxJSONInputSize=%d bytes, MaxWASMModuleSize=%d bytes, HashingTimeout=%s", cfg.MaxJSONInputSize, cfg.MaxWASMModuleSize, cfg.HashingTimeout)

	// --- Cabinet Service Initialization ---
	instanceID := uuid.New().String()
	log.Printf("Instance ID: %s", instanceID)

	log.Println("Initializing cabinet stores...")
	schemaStore := cabinet.NewMemSchemaStore()
	workflowStore := cabinet.NewMemWorkflowStore()
	moduleStore := cabinet.NewMemModuleStore()

	cabinetSvc := cabinet.NewCabinetService(schemaStore, moduleStore, workflowStore)
	defer cabinetSvc.Close(context.Background()) // Ensure WASM runtime is closed
	log.Println("Cabinet service initialized.")

	// --- Component Loading ---
	// Create a context with a timeout for all loading/hashing operations.
	loadCtx, cancel := context.WithTimeout(context.Background(), cfg.HashingTimeout)
	defer cancel()

	if err := loadEmbeddedComponents(loadCtx, cabinetSvc, embeddedSchemas, embeddedWorkflows, cfg.MaxWASMModuleSize); err != nil {
		log.Fatalf("FATAL: Failed to load embedded components: %v", err)
	}
	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	// For now, it's hardcoded to /etc/cic-relay/components.d
	if err := loadInstalledComponents(loadCtx, cabinetSvc, "/etc/cic-relay/components.d", cfg.MaxWASMModuleSize); err != nil {
		log.Fatalf("FATAL: Failed to load installed components: %v", err)
	}

	// --- HTTP Server Initialization ---
	api := cabinet.NewAPI(cabinetSvc)
	server := &relayServer{api: api, config: cfg}

	port := ":8080"
	log.Fatal(run(port, log.Default(), server))
}

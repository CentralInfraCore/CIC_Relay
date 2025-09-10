package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/google/uuid"

	"centralrelay/core/cabinet"
)

//go:embed embedded/schemas/*.yaml
var embeddedSchemas embed.FS

//go:embed embedded/workflows/*.yaml
var embeddedWorkflows embed.FS

// test seam: unit tesztben felülírható
var listenAndServe = http.ListenAndServe

// relayServer holds the dependencies for our HTTP handlers.
type relayServer struct {
	api *cabinet.API
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

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
	if err := loadEmbeddedComponents(cabinetSvc, embeddedSchemas, embeddedWorkflows); err != nil {
		log.Fatalf("FATAL: Failed to load embedded components: %v", err)
	}
	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	// For now, it's hardcoded to /etc/cic-relay/components.d
	if err := loadInstalledComponents(cabinetSvc, "/etc/cic-relay/components.d"); err != nil {
		log.Fatalf("FATAL: Failed to load installed components: %v", err)
	}

	// --- HTTP Server Initialization ---
	api := cabinet.NewAPI(cabinetSvc)
	server := &relayServer{api: api}

	port := ":8080"
	log.Fatal(run(port, log.Default(), server))
}

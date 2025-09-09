package main

import (
	"embed"
	"fmt"
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

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := `{"status": "ok", "message": "central relay responding"}`
	fmt.Fprintln(w, response)
}

// newMux külön a könnyebb tesztelhetőségért (routing ellenőrzés)
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", rootHandler)
	return mux
}

// run: külön függvény, hogy unit tesztben ne main()-t kelljen hívni
func run(port string, logger *log.Logger) error {
	logger.Printf("✅ Central relay API started on %s", port)
	return listenAndServe(port, newMux())
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
	log.Println("Cabinet service initialized.")

	// --- Component Loading ---
	if err := loadEmbeddedComponents(cabinetSvc, embeddedSchemas, embeddedWorkflows); err != nil {
		log.Fatalf("FATAL: Failed to load embedded components: %v", err)
	}
	// The path for installed components can be made configurable later (e.g., via flags or env vars)
	if err := loadInstalledComponents(cabinetSvc, "/etc/cic-relay/components.d"); err != nil {
		log.Fatalf("FATAL: Failed to load installed components: %v", err)
	}

	// --- HTTP Server Initialization ---
	port := ":8080"
	log.Fatal(run(port, log.Default()))
}

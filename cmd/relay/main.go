package main

import (
	"fmt"
	"log"
	"net/http"
)

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
	port := ":8080"
	log.Fatal(run(port, log.Default()))
}

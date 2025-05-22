package main

import (
	"fmt"
	"log"
	"net/http"
)

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := `{"status": "ok", "message": "central relay responding"}`
	fmt.Fprintln(w, response)
}

func main() {
	http.HandleFunc("/", rootHandler)

	port := ":8080"
	log.Printf("✅ Central relay API started on %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}


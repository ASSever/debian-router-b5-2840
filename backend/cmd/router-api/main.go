package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
)

const listenAddress = "127.0.0.1:8081"

func main() {
	dataDir := os.Getenv("STATE_DIRECTORY")
	if dataDir == "" {
		dataDir = "."
	}
	db, err := database.Open(filepath.Join(dataDir, "router.db"))
	if err != nil {
		log.Fatalf("open state database: %v", err)
	}
	defer db.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		if err := server.Close(); err != nil {
			log.Printf("close listener: %v", err)
		}
	}()

	log.Printf("router-api listening on %s", listenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("router-api failed: %v", err)
	}
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}

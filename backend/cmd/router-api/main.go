package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/admin"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/database"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/metrics"
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
	if len(os.Args) > 1 {
		if err := adminCommand(db, os.Args[1:]); err != nil {
			log.Fatalf("administrator command failed: %v", err)
		}
		return
	}
	initialized, err := admin.Exists(context.Background(), db)
	if err != nil {
		log.Fatalf("read administrator state: %v", err)
	}
	if !initialized {
		log.Fatal("administrator is not initialized")
	}

	certPath, keyPath, _, err := loadTLSCertificate(dataDir)
	if err != nil {
		log.Fatalf("prepare HTTPS: %v", err)
	}

	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	initCtx, finishInit := context.WithTimeout(stop, 5*time.Second)
	history, err := metrics.Open(initCtx, filepath.Join(dataDir, "metrics.db"))
	finishInit()
	if err != nil {
		log.Printf("open metrics history: %v", err)
	} else {
		collectorDone := make(chan struct{})
		go func() {
			defer close(collectorDone)
			metrics.Collect(stop, history, agentSocketPath(), dataDir)
		}()
		defer func() {
			cancel()
			<-collectorDone
			history.Close()
		}()
	}
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           withDashboard(newAPI(db), db, history),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	if os.Getenv("ROUTER_LAN_HTTPS") == "yes" {
		if err := serveLAN(stop, dataDir, server.Handler); err != nil {
			log.Printf("LAN HTTPS unavailable: %v", err)
		}
	}
	go func() {
		<-stop.Done()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("shutdown HTTPS server: %v", err)
		}
	}()

	log.Printf("router-api listening on https://%s", listenAddress)
	if err := server.ListenAndServeTLS(certPath, keyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("router-api failed: %v", err)
	}
}

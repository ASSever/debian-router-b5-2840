package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/ddns"
)

func main() {
	defaultStateDir := os.Getenv("STATE_DIRECTORY")
	if defaultStateDir == "" {
		defaultStateDir = ddns.DefaultStateDir
	}
	configPath := flag.String("config", ddns.DefaultConfigPath, "DDNS configuration file")
	stateDir := flag.String("state-dir", defaultStateDir, "DDNS status directory")
	flag.Parse()
	if flag.NArg() > 1 || flag.NArg() == 1 && flag.Arg(0) != "check-config" && flag.Arg(0) != "status" {
		log.Print("Usage: router-ddns [-config path] [-state-dir path] [check-config|status]")
		os.Exit(2)
	}
	if flag.Arg(0) == "status" {
		status, err := ddns.ReadStatus(*stateDir)
		if err != nil {
			log.Fatalf("DDNS status unavailable: %v", err)
		}
		status.ConfigID = ""
		if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
			log.Fatal(err)
		}
		return
	}
	config, err := ddns.LoadConfig(*configPath)
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	if flag.Arg(0) == "check-config" {
		return
	}
	creds, err := ddns.LoadCredentials(os.Getenv("CREDENTIALS_DIRECTORY"))
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ddns.Run(ctx, config, creds, *stateDir)
}

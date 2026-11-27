package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/mqtt"
)

func main() {
	configPath := flag.String("config", mqtt.DefaultConfigPath, "MQTT configuration file")
	flag.Parse()
	if flag.NArg() > 1 || flag.NArg() == 1 && flag.Arg(0) != "check-config" {
		log.Print("Usage: router-mqtt [-config path] [check-config]")
		os.Exit(2)
	}
	config, err := mqtt.LoadConfig(*configPath)
	if err == nil && flag.NArg() == 1 {
		return
	}
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	credentials, err := mqtt.LoadCredentials(os.Getenv("CREDENTIALS_DIRECTORY"))
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mqtt.Run(ctx, config, credentials)
}

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/network"
)

func loadLANTLSCertificate(stateDir string) (string, string, tls.Certificate, error) {
	dir := filepath.Join(stateDir, "tls-lan")
	cert, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := ensureLocalCertificate(stateDir, cert, key, network.LANHostname); err != nil {
		return "", "", tls.Certificate{}, err
	}
	if err := checkPrivateKeyPermissions(key); err != nil {
		return "", "", tls.Certificate{}, err
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	return cert, key, pair, err
}

func serveLAN(ctx context.Context, dataDir string, handler http.Handler) error {
	_, _, certificate, err := loadLANTLSCertificate(dataDir)
	if err != nil {
		return err
	}
	listenerConfig := net.ListenConfig{Control: bindLANDevice}
	listener, err := listenerConfig.Listen(ctx, "tcp4", ":"+network.LANPort)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           lanIngress(handler),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.ServeTLS(listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve LAN HTTPS: %v", err)
		}
	}()
	return nil
}

func lanIngress(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config, err := network.ManagedLAN(network.LANProfilePath)
		if err != nil || config == nil {
			http.NotFound(w, r)
			return
		}
		prefix := netip.MustParsePrefix(config.Address)
		clientHost, _, _ := net.SplitHostPort(r.RemoteAddr)
		client, err := netip.ParseAddr(clientHost)
		if lanRequestAddress(r) != prefix.Addr().String() || err != nil || !prefix.Contains(client) {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

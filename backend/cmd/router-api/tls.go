package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	tlsCertEnvironment = "ROUTER_TLS_CERT_FILE"
	tlsKeyEnvironment  = "ROUTER_TLS_KEY_FILE"
)

func tlsFilePaths(stateDir string) (string, string, error) {
	certPath, certConfigured := os.LookupEnv(tlsCertEnvironment)
	keyPath, keyConfigured := os.LookupEnv(tlsKeyEnvironment)
	if certConfigured != keyConfigured {
		return "", "", fmt.Errorf("%s and %s must be set together", tlsCertEnvironment, tlsKeyEnvironment)
	}
	if certConfigured {
		if certPath == "" || keyPath == "" {
			return "", "", errors.New("TLS certificate and key paths must not be empty")
		}
		return certPath, keyPath, nil
	}
	return filepath.Join(stateDir, "tls", "server.crt"), filepath.Join(stateDir, "tls", "server.key"), nil
}

func loadTLSCertificate(stateDir string) (string, string, tls.Certificate, error) {
	certPath, keyPath, err := tlsFilePaths(stateDir)
	if err != nil {
		return "", "", tls.Certificate{}, err
	}
	if os.Getenv(tlsCertEnvironment) == "" {
		if err := ensureLocalCertificate(stateDir, certPath, keyPath, "localhost"); err != nil {
			return "", "", tls.Certificate{}, err
		}
	}
	if err := checkPrivateKeyPermissions(keyPath); err != nil {
		return "", "", tls.Certificate{}, err
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return "", "", tls.Certificate{}, fmt.Errorf("load TLS certificate pair: %w", err)
	}
	return certPath, keyPath, certificate, nil
}

func ensureLocalCertificate(stateDir, certPath, keyPath, hostname string) error {
	tlsDir := filepath.Dir(certPath)
	info, err := os.Stat(tlsDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := createLocalCertificate(stateDir, tlsDir, hostname); err != nil {
			return fmt.Errorf("create local TLS certificate: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect TLS directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("TLS path %q is not a directory", tlsDir)
	}
	certInfo, certErr := os.Stat(certPath)
	keyInfo, keyErr := os.Stat(keyPath)
	if errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist) {
		return fmt.Errorf("TLS directory %q exists without a certificate pair", tlsDir)
	}
	if certErr != nil || keyErr != nil {
		return fmt.Errorf("incomplete TLS certificate pair in %q", tlsDir)
	}
	if !certInfo.Mode().IsRegular() || !keyInfo.Mode().IsRegular() {
		return errors.New("TLS certificate and key must be regular files")
	}
	return nil
}

func createLocalCertificate(stateDir, tlsDir, hostname string) error {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	stagingDir, err := os.MkdirTemp(stateDir, ".tls-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)
	if err := os.Chmod(stagingDir, 0700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hostname},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{hostname},
	}
	if hostname == "localhost" {
		template.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})
	if err := os.WriteFile(filepath.Join(stagingDir, "server.crt"), certPEM, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "server.key"), keyPEM, 0600); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(stagingDir, "server.crt"), 0644); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(stagingDir, "server.key"), 0600); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, tlsDir); err != nil {
		return err
	}
	return nil
}

func checkPrivateKeyPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect TLS private key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("TLS private key must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("TLS private key %q must not be readable or writable by group or others", path)
	}
	return nil
}

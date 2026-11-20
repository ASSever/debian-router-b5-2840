package main

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalTLSCertificateIsPersistentAndLoopbackScoped(t *testing.T) {
	stateDir := t.TempDir()
	certPath, keyPath, first, err := loadTLSCertificate(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0644 || keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("certificate permissions = %04o, key permissions = %04o", certInfo.Mode().Perm(), keyInfo.Mode().Perm())
	}
	parsed, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.IsCA {
		t.Fatal("exported trust certificate is not marked as a CA")
	}
	if err := parsed.VerifyHostname("localhost"); err != nil {
		t.Fatalf("localhost SAN missing: %v", err)
	}
	if err := parsed.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("loopback SAN missing: %v", err)
	}
	_, _, second, err := loadTLSCertificate(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) != string(second.Certificate[0]) {
		t.Fatal("local TLS certificate changed between starts")
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadTLSCertificate(stateDir); err == nil {
		t.Fatal("incomplete certificate pair was accepted")
	}
}

func TestTLSCustomPathsRequireACompletePair(t *testing.T) {
	t.Setenv(tlsCertEnvironment, filepath.Join(t.TempDir(), "certificate.pem"))
	t.Setenv(tlsKeyEnvironment, "")
	if _, _, err := tlsFilePaths(t.TempDir()); err == nil {
		t.Fatal("incomplete TLS environment configuration was accepted")
	}
}

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func ensureLocalTLSFiles(certFile, keyFile string) error {
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("tls cert/key file paths are required")
	}
	if fileExists(certFile) && fileExists(keyFile) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return fmt.Errorf("create tls cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return fmt.Errorf("create tls key dir: %w", err)
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate tls key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return fmt.Errorf("generate cert serial: %w", err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "accessd-connector-local",
		},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.Add(3650 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create tls certificate: %w", err)
	}

	certOut, err := os.OpenFile(certFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open cert file: %w", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certOut.Close()
		return fmt.Errorf("write cert pem: %w", err)
	}
	if err := certOut.Close(); err != nil {
		return fmt.Errorf("close cert file: %w", err)
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(priv)
	keyOut, err := os.OpenFile(keyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open key file: %w", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes}); err != nil {
		_ = keyOut.Close()
		return fmt.Errorf("write key pem: %w", err)
	}
	if err := keyOut.Close(); err != nil {
		return fmt.Errorf("close key file: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

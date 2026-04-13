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
	"strings"
	"time"
)

const (
	localTLSLeafCN       = "accessd-connector-local"
	localTLSRootCN       = "accessd-connector-local-root"
	localTLSRotateCAEnv  = "ACCESSD_CONNECTOR_TLS_FORCE_ROTATE_CA"
	localTLSRootLifetime = 20 * 365 * 24 * time.Hour
	localTLSLeafLifetime = 397 * 24 * time.Hour
)

func localTrustCertPath(certFile string) string {
	return filepath.Join(filepath.Dir(certFile), "local-root-ca.crt")
}

func localTrustKeyPath(certFile string) string {
	return filepath.Join(filepath.Dir(certFile), "local-root-ca.key")
}

func ensureLocalTLSFiles(certFile, keyFile string) error {
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("tls cert/key file paths are required")
	}
	trustCertFile := localTrustCertPath(certFile)
	trustKeyFile := localTrustKeyPath(certFile)

	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return fmt.Errorf("create tls cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return fmt.Errorf("create tls key dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(trustCertFile), 0o700); err != nil {
		return fmt.Errorf("create tls trust cert dir: %w", err)
	}

	rootCert, rootKey, rootDER, err := loadOrCreateLocalCA(trustCertFile, trustKeyFile, shouldForceRotateLocalCA())
	if err != nil {
		return err
	}
	if shouldReuseLeafTLSFiles(certFile, keyFile, rootCert) {
		return nil
	}

	leafPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate tls leaf key: %w", err)
	}
	leafSerial, err := newSerialNumber()
	if err != nil {
		return fmt.Errorf("generate leaf cert serial: %w", err)
	}
	now := time.Now()
	leafTemplate := x509.Certificate{
		SerialNumber: leafSerial,
		Subject: pkix.Name{
			CommonName: localTLSLeafCN,
		},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.Add(localTLSLeafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, &leafTemplate, rootCert, &leafPriv.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("create tls leaf certificate: %w", err)
	}

	certOut, err := os.OpenFile(certFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open cert file: %w", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER}); err != nil {
		_ = certOut.Close()
		return fmt.Errorf("write leaf cert pem: %w", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: rootDER}); err != nil {
		_ = certOut.Close()
		return fmt.Errorf("write root chain pem: %w", err)
	}
	if err := certOut.Close(); err != nil {
		return fmt.Errorf("close cert file: %w", err)
	}

	leafKeyOut, err := os.OpenFile(keyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open key file: %w", err)
	}
	leafKeyBytes := x509.MarshalPKCS1PrivateKey(leafPriv)
	if err := pem.Encode(leafKeyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: leafKeyBytes}); err != nil {
		_ = leafKeyOut.Close()
		return fmt.Errorf("write key pem: %w", err)
	}
	if err := leafKeyOut.Close(); err != nil {
		return fmt.Errorf("close key file: %w", err)
	}

	return nil
}

func shouldForceRotateLocalCA() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(localTLSRotateCAEnv)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func loadOrCreateLocalCA(trustCertFile, trustKeyFile string, forceRotate bool) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	if !forceRotate {
		if rootCert, rootKey, rootDER, err := loadLocalCA(trustCertFile, trustKeyFile); err == nil {
			return rootCert, rootKey, rootDER, nil
		}
	}
	return createAndPersistLocalCA(trustCertFile, trustKeyFile)
}

func loadLocalCA(trustCertFile, trustKeyFile string) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	if !fileExists(trustCertFile) || !fileExists(trustKeyFile) {
		return nil, nil, nil, fmt.Errorf("local ca files missing")
	}
	rootRaw, err := os.ReadFile(trustCertFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read local ca cert: %w", err)
	}
	rootBlock, _ := pem.Decode(rootRaw)
	if rootBlock == nil || rootBlock.Type != "CERTIFICATE" {
		return nil, nil, nil, fmt.Errorf("parse local ca cert pem")
	}
	rootCert, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse local ca cert: %w", err)
	}
	if !rootCert.IsCA || rootCert.Subject.CommonName != localTLSRootCN {
		return nil, nil, nil, fmt.Errorf("invalid local ca metadata")
	}
	if rootCert.NotAfter.Before(time.Now().Add(24 * time.Hour)) {
		return nil, nil, nil, fmt.Errorf("local ca expired or expiring")
	}

	keyRaw, err := os.ReadFile(trustKeyFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read local ca key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyRaw)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		return nil, nil, nil, fmt.Errorf("parse local ca key pem")
	}
	rootKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse local ca key: %w", err)
	}
	pub, ok := rootCert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, nil, nil, fmt.Errorf("local ca public key not rsa")
	}
	if pub.N.Cmp(rootKey.PublicKey.N) != 0 || pub.E != rootKey.PublicKey.E {
		return nil, nil, nil, fmt.Errorf("local ca key mismatch")
	}
	return rootCert, rootKey, rootBlock.Bytes, nil
}

func createAndPersistLocalCA(trustCertFile, trustKeyFile string) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	rootPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate tls root key: %w", err)
	}
	rootSerial, err := newSerialNumber()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate root cert serial: %w", err)
	}
	now := time.Now()
	rootTemplate := x509.Certificate{
		SerialNumber: rootSerial,
		Subject: pkix.Name{
			CommonName: localTLSRootCN,
		},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.Add(localTLSRootLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, &rootTemplate, &rootTemplate, &rootPriv.PublicKey, rootPriv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create tls root certificate: %w", err)
	}
	rootCertOut, err := os.OpenFile(trustCertFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open root cert file: %w", err)
	}
	if err := pem.Encode(rootCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: rootDER}); err != nil {
		_ = rootCertOut.Close()
		return nil, nil, nil, fmt.Errorf("write root cert pem: %w", err)
	}
	if err := rootCertOut.Close(); err != nil {
		return nil, nil, nil, fmt.Errorf("close root cert file: %w", err)
	}

	rootKeyOut, err := os.OpenFile(trustKeyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open root key file: %w", err)
	}
	rootKeyBytes := x509.MarshalPKCS1PrivateKey(rootPriv)
	if err := pem.Encode(rootKeyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: rootKeyBytes}); err != nil {
		_ = rootKeyOut.Close()
		return nil, nil, nil, fmt.Errorf("write root key pem: %w", err)
	}
	if err := rootKeyOut.Close(); err != nil {
		return nil, nil, nil, fmt.Errorf("close root key file: %w", err)
	}

	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse generated root cert: %w", err)
	}
	return rootCert, rootPriv, rootDER, nil
}

func newSerialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, big.NewInt(1<<62))
}

func shouldReuseLeafTLSFiles(certFile, keyFile string, root *x509.Certificate) bool {
	if root == nil || !fileExists(certFile) || !fileExists(keyFile) {
		return false
	}
	leafRaw, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	leafBlock, rest := pem.Decode(leafRaw)
	if leafBlock == nil || leafBlock.Type != "CERTIFICATE" {
		return false
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return false
	}
	if leaf.IsCA || leaf.Subject.CommonName != localTLSLeafCN {
		return false
	}
	if leaf.NotAfter.Before(time.Now().Add(7 * 24 * time.Hour)) {
		return false
	}
	// Safari/WebKit can reject local TLS leaf certs with excessive validity
	// as "not standards compliant". Keep leaf lifetime capped.
	if leaf.NotAfter.Sub(leaf.NotBefore) > localTLSLeafLifetime {
		return false
	}
	if !hasDNS(leaf.DNSNames, "localhost") {
		return false
	}
	if !hasIP(leaf.IPAddresses, "127.0.0.1") || !hasIP(leaf.IPAddresses, "::1") {
		return false
	}
	if err := leaf.CheckSignatureFrom(root); err != nil {
		return false
	}

	keyRaw, err := os.ReadFile(keyFile)
	if err != nil {
		return false
	}
	keyBlock, _ := pem.Decode(keyRaw)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		return false
	}
	leafKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return false
	}
	leafPub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false
	}
	if leafPub.N.Cmp(leafKey.PublicKey.N) != 0 || leafPub.E != leafKey.PublicKey.E {
		return false
	}

	chainBlock, _ := pem.Decode(rest)
	if chainBlock == nil || chainBlock.Type != "CERTIFICATE" {
		return false
	}
	chainCert, err := x509.ParseCertificate(chainBlock.Bytes)
	if err != nil {
		return false
	}
	if !chainCert.Equal(root) {
		return false
	}
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func hasDNS(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

func hasIP(values []net.IP, want string) bool {
	target := net.ParseIP(want)
	if target == nil {
		return false
	}
	for _, ip := range values {
		if ip != nil && ip.Equal(target) {
			return true
		}
	}
	return false
}

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	directory := flag.String("out", "/certs", "certificate output directory")
	owner := flag.Int("owner", 10001, "UID/GID that should own generated files")
	flag.Parse()
	if err := generate(*directory, *owner); err != nil {
		panic(err)
	}
}

func generate(directory string, owner int) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Watchdog Demo CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(5, 0, 0),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serverTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "demo-target"},
		DNSNames: []string{"demo-target", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, &serverTemplate, &caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	files := []struct {
		name  string
		block *pem.Block
		mode  os.FileMode
	}{
		{"ca.pem", &pem.Block{Type: "CERTIFICATE", Bytes: caDER}, 0o644},
		{"server.pem", &pem.Block{Type: "CERTIFICATE", Bytes: serverDER}, 0o644},
		{"server-key.pem", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}, 0o600},
	}
	for _, file := range files {
		path := filepath.Join(directory, file.name)
		if err := os.WriteFile(path, pem.EncodeToMemory(file.block), file.mode); err != nil {
			return err
		}
		if err := os.Chown(path, owner, owner); err != nil && !os.IsPermission(err) {
			return err
		}
	}
	return nil
}

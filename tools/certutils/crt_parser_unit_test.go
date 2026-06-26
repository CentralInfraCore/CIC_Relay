package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func writeCertPEM(t *testing.T, der []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cert.crt")
	err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	if err != nil {
		t.Fatalf("writeCertPEM: %v", err)
	}
	return p
}

func Test_printHelp_DoesNotPanic(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	printHelp()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if out == "" {
		t.Fatal("help output empty")
	}
}

func Test_main_VerboseYAML(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// reset default FlagSet to avoid "flag redefined"
	flag.CommandLine = flag.NewFlagSet("crt_parser_test_v", flag.ContinueOnError)
	os.Args = []string{"crt_parser", "-v"}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	main()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		t.Fatalf("verbose output empty")
	}
}

func Test_main_CertPath_YAML(t *testing.T) {
	// Self-signed certificate generation to temporary file
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "cert.crt")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// reset default FlagSet to avoid "flag redefined"
	flag.CommandLine = flag.NewFlagSet("crt_parser_test_cert", flag.ContinueOnError)
	os.Args = []string{"crt_parser", "-cert", p}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	main()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	// Minimal sanity check: must be non-empty and structured
	if strings.TrimSpace(out) == "" || !strings.Contains(out, ":") {
		t.Fatalf("unexpected YAML-like output:\n%s", out)
	}
	_ = io.Discard // import guard
}

func Test_main_Help_LongFlag(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	flag.CommandLine = flag.NewFlagSet("crt_help_long", flag.ContinueOnError)
	os.Args = []string{"crt_parser", "--help"}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	main()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	assert.Contains(t, buf.String(), "Usage:")
}

func Test_main_CertPath_ECDSACert(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	p := writeCertPEM(t, der)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	flag.CommandLine = flag.NewFlagSet("crt_ecdsa", flag.ContinueOnError)
	os.Args = []string{"crt_parser", "-cert", p}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	main()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	assert.Contains(t, buf.String(), "P-256")
}

func Test_main_CertPath_Ed25519Cert(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	p := writeCertPEM(t, der)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	flag.CommandLine = flag.NewFlagSet("crt_ed25519", flag.ContinueOnError)
	os.Args = []string{"crt_parser", "-cert", p}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	main()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	assert.NotEmpty(t, out)
}

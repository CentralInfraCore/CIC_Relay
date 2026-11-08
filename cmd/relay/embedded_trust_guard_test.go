package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The dev trust store (bootstrapPKI in dev mode) trusts every self-signed CA that
// is embedded under cmd/relay/embedded/pki. A CA whose private key is in this
// repository is not a trust anchor: anyone who can read the repository can issue
// a chain that the dev trust store accepts. These tests keep such a CA out of
// the embedded directory.

// spkiDigest is the SHA-256 of a public key's PKIX encoding.
func spkiDigest(pub crypto.PublicKey) ([32]byte, bool) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(der), true
}

// privateKeyPublic parses a PEM private key (PKCS#8, SEC1 or PKCS#1) and returns its public key.
func privateKeyPublic(data []byte) (crypto.PublicKey, bool) {
	blk, _ := pem.Decode(data)
	if blk == nil {
		return nil, false
	}
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s.Public(), true
		}
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k.Public(), true
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k.Public(), true
	}
	return nil, false
}

// repoKnownKeys maps the SPKI digest of every private key file (*.key) under root to its path.
func repoKnownKeys(t *testing.T, root string) map[[32]byte]string {
	t.Helper()
	keys := map[[32]byte]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".key") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		pub, ok := privateKeyPublic(b)
		if !ok {
			return nil
		}
		if sum, ok := spkiDigest(pub); ok {
			keys[sum] = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return keys
}

// embeddedCertsWithKnownKey returns one message per certificate in dir of fsys whose
// private key is among keys.
func embeddedCertsWithKnownKey(fsys fs.FS, dir string, keys map[[32]byte]string) []string {
	var hits []string
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return []string{"cannot read " + dir + ": " + err.Error()}
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			hits = append(hits, "cannot read "+e.Name()+": "+err.Error())
			continue
		}
		for rest := b; len(rest) > 0; {
			var blk *pem.Block
			blk, rest = pem.Decode(rest)
			if blk == nil {
				break
			}
			if blk.Type != "CERTIFICATE" {
				continue
			}
			c, err := x509.ParseCertificate(blk.Bytes)
			if err != nil {
				continue
			}
			if sum, ok := spkiDigest(c.PublicKey); ok {
				if keyPath, hit := keys[sum]; hit {
					hits = append(hits, e.Name()+" ("+c.Subject.String()+") has its private key in the repository: "+keyPath)
				}
			}
		}
	}
	return hits
}

func TestEmbeddedTrustHasNoRepoKnownPrivateKey(t *testing.T) {
	keys := repoKnownKeys(t, "../..")
	if len(keys) == 0 {
		t.Fatal("found no private key file under the repository root: the guard would pass vacuously")
	}
	for _, h := range embeddedCertsWithKnownKey(embeddedFS, "embedded/pki", keys) {
		t.Error(h)
	}
}

// The guard has to be able to fail: a CA whose key is among the known keys is reported.
func TestEmbeddedTrustGuardDetectsKnownKey(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Guard Self-Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	sum, ok := spkiDigest(&k.PublicKey)
	if !ok {
		t.Fatal("cannot digest the self-test public key")
	}
	fsys := fstest.MapFS{"pki/ca.pem": {Data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}}

	if hits := embeddedCertsWithKnownKey(fsys, "pki", map[[32]byte]string{sum: "repo/ca.key"}); len(hits) != 1 {
		t.Fatalf("a CA with a known key must be reported exactly once, got %v", hits)
	}
	if hits := embeddedCertsWithKnownKey(fsys, "pki", map[[32]byte]string{}); len(hits) != 0 {
		t.Fatalf("a CA with no known key must not be reported, got %v", hits)
	}
}

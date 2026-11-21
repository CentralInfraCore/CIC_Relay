// Package pki provides embedded PKI trust anchors for the CIC relay.
//
// CICSourceCAPEM is the CIC Source CA certificate, fetched from the local
// Vault at build time via `make fetch-pki` and embedded at compile time.
// It is the trust anchor for source release validation (leaf cert → CICSourceCA).
//
// To regenerate: make fetch-pki (requires local Vault with EEC certs loaded).
package pki

import _ "embed"

// CICSourceCAPEM is the PEM-encoded CIC Source CA certificate (cic-source-ca.pem, written by
// make fetch-pki), embedded at compile time.
//
//go:embed cic-source-ca.pem
var CICSourceCAPEM []byte

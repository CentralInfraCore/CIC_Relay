package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// R3a — bind and enforce a module's capability manifest (feature-011,
// r3-plan.md). A module *declares* its reach in its signed project.yaml; the host
// derives the enforced policy solely from that declaration and rejects a module
// whose imports exceed it. This file is the declaration side; cryptographic
// verification of the declaration (cicSign against the CIC Root CA) is R3b and
// activates pki_verify.go — until then a module is untrusted and cic-flow stays
// inert (fail-closed).

// capabilityManifest is a module's declared reach — the `capability_manifest`
// block of its project.yaml. The module cannot widen it: it is part of the signed
// artifact and the host enforces exactly this.
type capabilityManifest struct {
	Egress  egressPolicy `yaml:"egress"`
	Secrets []secretRule `yaml:"secrets"`
}

// egressRule is one permitted outbound reach. Host is an exact host or a leading
// "*." suffix glob. Empty Methods / Paths / Ports mean any-method / any-path /
// port 443.
type egressRule struct {
	Host    string   `yaml:"host"`
	Methods []string `yaml:"methods"`
	Paths   []string `yaml:"paths"`
	Ports   []int    `yaml:"ports"`
}

// egressPolicy is the ordered set of egress rules; the first match permits.
type egressPolicy []egressRule

// secretRule is one secret handle the module may operate on — never the value.
type secretRule struct {
	Handle string   `yaml:"handle"`
	Ops    []string `yaml:"ops"`
}

// moduleProject is the subset of a module's project.yaml the relay reads at load.
type moduleProject struct {
	Metadata struct {
		BuildHash string `yaml:"buildHash"`
		CICSign   string `yaml:"cicSign"` // verified in R3b (pki_verify.go)
	} `yaml:"metadata"`
	ABI struct {
		Imports []struct {
			Module    string   `yaml:"module"`
			Functions []string `yaml:"functions"`
		} `yaml:"imports"`
	} `yaml:"abi"`
	CapabilityManifest capabilityManifest `yaml:"capability_manifest"`
}

// parseModuleProject unmarshals a module's project.yaml.
func parseModuleProject(data []byte) (*moduleProject, error) {
	var p moduleProject
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse project.yaml: %w", err)
	}
	return &p, nil
}

// allows reports whether the policy permits method + host:port + path. An empty
// policy allows nothing — egress is opt-in, never caller-widened.
func (p egressPolicy) allows(method, host string, port int, path string) bool {
	for _, r := range p {
		if hostGlobMatch(r.Host, host) &&
			methodAllowed(r.Methods, method) &&
			portAllowed(r.Ports, port) &&
			pathAllowed(r.Paths, path) {
			return true
		}
	}
	return false
}

// hostGlobMatch matches host against an exact host or a leading "*." suffix glob.
// A bare "*" and any other/infix wildcard never match — unbounded egress is
// rejected. "*.oraclecloud.com" matches "iaas.…oraclecloud.com" but not the apex
// "oraclecloud.com" nor "evil-oraclecloud.com".
func hostGlobMatch(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(host)
	if pattern == "" || pattern == "*" {
		return false
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		if suffix == "" || strings.Contains(suffix, "*") {
			return false
		}
		return strings.HasSuffix(host, "."+suffix) && len(host) > len(suffix)+1
	}
	if strings.Contains(pattern, "*") {
		return false
	}
	return host == pattern
}

func methodAllowed(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	return containsFold(methods, method)
}

func portAllowed(ports []int, port int) bool {
	if len(ports) == 0 {
		return port == 443
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

func pathAllowed(paths []string, path string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, prefix := range paths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// keyResolver maps a manifest secret handle (e.g. "oci/prod") to a signer over
// the relay-provisioned Vault key behind it. The module never names key material;
// provisioning the OCI key into Vault is a separate operator flow (r3-plan.md).
type keyResolver func(handle string) (signer, error)

// grantsSign reports whether the manifest declares a secret usable for signing.
func (m capabilityManifest) grantsSign() bool {
	_, ok := m.signHandle()
	return ok
}

// signHandle returns the first secret handle carrying a `sign` op.
func (m capabilityManifest) signHandle() (string, bool) {
	for _, s := range m.Secrets {
		if containsFold(s.Ops, "sign") {
			return s.Handle, true
		}
	}
	return "", false
}

// bindCICFlowDeps builds the per-module host deps from the verified manifest: the
// egress policy, and a sign-only signer resolved from the manifest's sign handle.
// A nil resolver or an absent sign handle yields a nil signer (sign stays inert).
func bindCICFlowDeps(m capabilityManifest, resolve keyResolver, doer httpDoer) (cicFlowDeps, error) {
	deps := cicFlowDeps{doer: doer, egress: m.Egress}
	if handle, ok := m.signHandle(); ok && resolve != nil {
		sg, err := resolve(handle)
		if err != nil {
			return cicFlowDeps{}, fmt.Errorf("resolve sign handle %q: %w", handle, err)
		}
		deps.sign = sg
	}
	return deps, nil
}

// cicFlowImports returns the set of cic-flow functions the module imports.
func (p *moduleProject) cicFlowImports() map[string]bool {
	out := map[string]bool{}
	for _, imp := range p.ABI.Imports {
		if imp.Module != "cic-flow" {
			continue
		}
		for _, fn := range imp.Functions {
			out[fn] = true
		}
	}
	return out
}

// checkImportsWithinManifest rejects a module whose cic-flow imports exceed its
// capability_manifest: importing `sign` requires a secrets entry with ops:[sign];
// importing `actuate` requires at least one egress entry. Enforced at load, not
// at call time.
func (p *moduleProject) checkImportsWithinManifest() error {
	imports := p.cicFlowImports()
	if imports["sign"] && !p.CapabilityManifest.grantsSign() {
		return fmt.Errorf("module imports cic-flow.sign but declares no secrets entry with ops:[sign]")
	}
	if imports["actuate"] && len(p.CapabilityManifest.Egress) == 0 {
		return fmt.Errorf("module imports cic-flow.actuate but declares no egress")
	}
	return nil
}

// verifyBuildHash checks the loaded wasm against the module's declared buildHash
// (integrity: the artifact the signature covers is the one being loaded).
func (p *moduleProject) verifyBuildHash(wasm []byte) error {
	if p.Metadata.BuildHash == "" {
		return fmt.Errorf("metadata.buildHash is empty")
	}
	got := sha256.Sum256(wasm)
	if gotHex := hex.EncodeToString(got[:]); gotHex != p.Metadata.BuildHash {
		return fmt.Errorf("buildHash mismatch: wasm %s != declared %s", gotHex, p.Metadata.BuildHash)
	}
	return nil
}

func containsFold(ss []string, target string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}

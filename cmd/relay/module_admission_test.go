package main

// #162 acceptance (AC-162-01..19) at the PRODUCTION entry point, loadModule (and loadComponentsFromFS for the
// single-read cases). Every negative case asserts the named result code (admissionCode), not just "an error", and
// has a positive control on the same fixture that differs only in the property under test.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"centralrelay/core/cabinet"
)

// gitOnlyManifest is a manifest that grants git status (no cic-flow import).
const gitOnlyManifest = `abi:
  imports:
    - module: git
      functions: [status]
capability_manifest:
  git:
    - repo: infra
      ops: [status]
`

// wasmWithCustom is a minimal module with one custom section ("x", payload 0xaa): its executed (stripped)
// representation differs from the raw bytes.
var wasmWithCustom = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x00, 0x03, 0x01, 'x', 0xaa}

// admFixture is one enveloped release of module adm.mod@1.0.0 with the given manifest and wasm, signed by the test
// module signer, and a prod trust context in which that signer is a module:sign authority.
type admFixture struct {
	fsys   fstest.MapFS
	signer *envelopeSigner
	assets *PKIAssets
	reg    *cicFlowRegistry
	svc    cabinet.Cabinet
}

// newAdmFixture builds the fixture; the registry hooks are installed for the test's lifetime.
func newAdmFixture(t *testing.T, manifest string, wasm []byte) *admFixture {
	t.Helper()
	fsys, _ := moduleFSWith(t, "adm.mod", manifest)
	if wasm != nil {
		setWasm(t, fsys, wasm)
	}
	f := &admFixture{fsys: fsys, signer: testModuleSigner(t), reg: newCICFlowRegistry(nil, &stubDoer{}), svc: newCabinetForTest(t)}
	f.assets = f.signer.authorize(loadedPKIWithSource(t))
	f.assets.TrustMode = TrustModeProd
	addEnvelope(t, fsys, "m.yaml", f.signer, nil)
	t.Cleanup(hooks(f.reg))
	return f
}

// setWasm replaces the module's wasm and rewrites wasmDigest, wasmSize and buildHash to match it.
func setWasm(t *testing.T, fsys fstest.MapFS, wasm []byte) {
	t.Helper()
	old := fsys["m.wasm"].Data
	y := strings.NewReplacer(hexSHA(old), hexSHA(wasm),
		"wasmSize: "+strconv.Itoa(len(old)), "wasmSize: "+strconv.Itoa(len(wasm))).Replace(string(fsys["m.yaml"].Data))
	fsys["m.yaml"] = &fstest.MapFile{Data: []byte(y)}
	fsys["m.wasm"] = &fstest.MapFile{Data: wasm}
}

// load runs loadModule on the fixture's current files and returns the captured log and the error.
func (f *admFixture) load(t *testing.T) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&buf)
	logger.SetFormatter(&logrus.JSONFormatter{})
	err := loadModule(context.Background(), f.svc, f.fsys, "m.yaml", f.fsys["m.yaml"].Data, f.assets, int64(16*1024*1024), logger)
	return buf.String(), err
}

// resign re-signs the current files (after a legitimate rebuild) with the fixture signer.
func (f *admFixture) resign(t *testing.T, mutate func(*moduleReleaseEnvelope)) string {
	t.Helper()
	return addEnvelope(t, f.fsys, "m.yaml", f.signer, mutate)
}

// registered reports whether adm.mod@1.0.0 is in the cabinet.
func (f *admFixture) registered() bool {
	_, err := f.svc.GetModule("adm.mod@1.0.0")
	return err == nil
}

// requireRejected asserts the load failed with code, left no module and no grant.
func requireRejected(t *testing.T, f *admFixture, code string) {
	t.Helper()
	_, err := f.load(t)
	require.Error(t, err)
	assert.Equal(t, code, admissionCode(err), "rejected for the named reason: %v", err)
	assert.False(t, f.registered(), "a rejected module is not registered")
	assert.Equal(t, "", f.reg.bindingFor("adm.mod@1.0.0"), "a rejected module gets no capability")
	_, flow := f.reg.lookup("adm.mod@1.0.0")
	_, git := f.reg.git["adm.mod@1.0.0"]
	assert.False(t, flow || git, "no grant of any kind")
}

// AC-162-10 (positive control of every negative below): a valid envelope, authorised signer, verified chain and
// provenance and matching digests → authenticated, capability bound to (module_id, envelope_digest).
func TestAdmission_AC10_PositiveAuthenticated(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest+`  git:
    - repo: infra
      ops: [status]
`, nil)
	envDigest := sha256Ref(envelopeFor(t, f.fsys, "m.yaml", f.signer).signedBytes())
	logs, err := f.load(t)
	require.NoError(t, err)
	assert.Contains(t, logs, `"trust_state":"authenticated"`)
	assert.Contains(t, logs, `"envelope_digest":"`+envDigest+`"`)
	assert.True(t, f.registered())
	assert.Equal(t, envDigest, f.reg.bindingFor("adm.mod@1.0.0"))
	stored, err := f.svc.GetModule("adm.mod@1.0.0")
	require.NoError(t, err)
	id := cabinet.InstanceIdentity{Name: "adm.mod@1.0.0", Artifact: stored.Checksum(), Serial: 1, Binding: envDigest}
	_, ok := f.reg.lookupFor(id)
	assert.True(t, ok, "the authenticated instance resolves its cic-flow deps")
	assert.NoError(t, f.reg.authorizeGit(id, "status", gitAuthRequest{RepoID: "infra"}))
}

// AC-162-01: a module built by someone else, presented with the genuine (valid, trusted) certificate: the envelope
// names the right digests and key, but the signature is not by the certificate's key.
func TestAdmission_AC01_ForeignBuildWithValidCertificate(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	setWasm(t, f.fsys, wasmWithCustom) // the foreign build
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	env := envelopeFor(t, f.fsys, "m.yaml", f.signer) // key_id of the presented certificate
	forger := &envelopeSigner{key: other}
	writeEnvelope(t, f.fsys, "m.yaml", env, forger.sign(t, env))
	requireRejected(t, f, codeSignatureInvalid)

	// control: the same foreign build signed by the certificate's key is admitted
	f.resign(t, nil)
	_, err = f.load(t)
	require.NoError(t, err)
}

// AC-162-02: the manifest edited after signing (wider egress, an extra secret) → MANIFEST_DIGEST_MISMATCH.
func TestAdmission_AC02_ManifestEditedAfterSigning(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"wider egress":  func(y string) string { return strings.Replace(y, `"*.oraclecloud.com"`, `"*.example.com"`, 1) },
		"extra secrets": func(y string) string { return y + "    - handle: \"vault/*\"\n      ops: [sign]\n" },
		"one byte":      func(y string) string { return y + "\n" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(edit(string(f.fsys["m.yaml"].Data)))}
			requireRejected(t, f, codeManifestDigestMismatch)
		})
	}
}

// AC-162-03 / AC-162-16: the wasm replaced while the manifest and the certificate stay → WASM_DIGEST_MISMATCH,
// on every route the replacement can take.
func TestAdmission_AC03_AC16_WasmReplaced(t *testing.T) {
	t.Run("same size, manifest untouched", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		w := append([]byte(nil), f.fsys["m.wasm"].Data...)
		w[7] ^= 0x01
		f.fsys["m.wasm"] = &fstest.MapFile{Data: w}
		requireRejected(t, f, codeWasmDigestMismatch)
	})
	t.Run("envelope signed over another raw digest", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		f.resign(t, func(e *moduleReleaseEnvelope) { e.WasmSHA256 = sha256Ref([]byte("another build")) })
		requireRejected(t, f, codeWasmDigestMismatch)
	})
	t.Run("manifest wasmDigest and buildHash rewritten too", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		setWasm(t, f.fsys, wasmWithCustom)            // the manifest now differs from the signed one as well
		requireRejected(t, f, codeWasmDigestMismatch) // the raw digest is checked before the manifest digest
	})
}

// AC-162-09 (functional half; the call path is in the report): verifyBuildHash runs on the load path. A signed
// release whose metadata.buildHash does not name the raw wasm is rejected even though wasmDigest and the envelope
// agree; a release without buildHash is rejected too.
func TestAdmission_AC09_BuildHashOnTheLoadPath(t *testing.T) {
	for name, edit := range map[string]func(string, string) string{
		"buildHash names another wasm": func(y, h string) string {
			return strings.Replace(y, "buildHash: "+h, "buildHash: "+hexSHA([]byte("x")), 1)
		},
		"buildHash missing": func(y, h string) string { return strings.Replace(y, "  buildHash: "+h+"\n", "", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			h := hexSHA(f.fsys["m.wasm"].Data)
			f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(edit(string(f.fsys["m.yaml"].Data), h))}
			f.resign(t, nil) // the signer signs exactly this manifest: only buildHash is wrong
			_, err := f.load(t)
			require.Error(t, err)
			assert.Equal(t, codeWasmDigestMismatch, admissionCode(err), err.Error())
			assert.Contains(t, err.Error(), "buildHash")
		})
	}
}

// AC-162-04: a reused module ID with another digest does not inherit the earlier module's capability: neither a
// running instance of the old artifact nor the new artifact under the old identity is authorised.
func TestAdmission_AC04_ReusedModuleIDDoesNotInherit(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest+`  git:
    - repo: infra
      ops: [status]
`, nil)
	_, err := f.load(t)
	require.NoError(t, err)
	oldStored, _ := f.svc.GetModule("adm.mod@1.0.0")
	oldID := cabinet.InstanceIdentity{Name: "adm.mod@1.0.0", Artifact: oldStored.Checksum(), Serial: 1, Binding: f.reg.bindingFor("adm.mod@1.0.0")}
	_, ok := f.reg.lookupFor(oldID)
	require.True(t, ok, "control: the first module is authorised")

	t.Run("re-released with another wasm", func(t *testing.T) {
		setWasm(t, f.fsys, wasmWithCustom)
		newDigest := f.resign(t, nil)
		_, err := f.load(t)
		require.NoError(t, err)
		require.NotEqual(t, oldID.Binding, newDigest)
		_, ok := f.reg.lookupFor(oldID)
		assert.False(t, ok, "the old instance does not inherit the new grant")
		assert.Error(t, f.reg.authorizeGit(oldID, "status", gitAuthRequest{RepoID: "infra"}))
		newStored, _ := f.svc.GetModule("adm.mod@1.0.0")
		mixed := cabinet.InstanceIdentity{Name: "adm.mod@1.0.0", Artifact: newStored.Checksum(), Serial: 2, Binding: oldID.Binding}
		_, ok = f.reg.lookupFor(mixed)
		assert.False(t, ok, "the new artifact under the old envelope identity is not authorised")
	})
	t.Run("reloaded without an envelope", func(t *testing.T) {
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		f.assets.TrustMode = TrustModeDev // transitional is loadable in dev only for a cic-flow module
		logs, err := f.load(t)
		require.NoError(t, err)
		assert.Contains(t, logs, `"trust_state":"transitional"`)
		assert.Equal(t, "", f.reg.bindingFor("adm.mod@1.0.0"), "a transitional reload has no grant at all")
	})
}

// AC-162-05: an unknown envelope_version → ENVELOPE_VERSION (even when it is signed).
func TestAdmission_AC05_UnknownEnvelopeVersion(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	f.resign(t, func(e *moduleReleaseEnvelope) { e.EnvelopeVersion = 2 })
	requireRejected(t, f, codeEnvelopeVersion)
}

// AC-162-06: the envelope's key_id is not the presented certificate's key → KEY_MISMATCH.
func TestAdmission_AC06_KeyIDNotTheCertificateKey(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	f.resign(t, func(e *moduleReleaseEnvelope) { e.Signer.KeyID = sha256Ref([]byte("someone else")) })
	requireRejected(t, f, codeKeyMismatch)
}

// AC-162-07: in prod, a cic-flow module without its .sig is rejected, never loaded without its capability; without
// any envelope it is rejected too (V6). In dev the envelope-less module loads as transitional, with no capability.
func TestAdmission_AC07_MissingSignatureInProd(t *testing.T) {
	t.Run("envelope without .sig", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		delete(f.fsys, "m"+signatureSuffix)
		requireRejected(t, f, codeEnvelopeMissing)
	})
	t.Run(".sig without envelope", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		delete(f.fsys, "m"+envelopeSuffix)
		requireRejected(t, f, codeEnvelopeMissing)
	})
	t.Run("no envelope, no .sig: prod", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		requireRejected(t, f, codeModuleRejected)
	})
	t.Run("no envelope, no .sig: unset trust mode counts as prod", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		f.assets.TrustMode = ""
		requireRejected(t, f, codeModuleRejected)
	})
	t.Run("no envelope, no cic-flow import: prod loads transitional, no capability", func(t *testing.T) {
		f := newAdmFixture(t, gitOnlyManifest, nil)
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		logs, err := f.load(t)
		require.NoError(t, err)
		assert.Contains(t, logs, `"trust_state":"transitional"`)
		_, git := f.reg.git["adm.mod@1.0.0"]
		assert.False(t, git)
	})
	t.Run("no envelope, no .sig: dev loads transitional, no capability", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		f.assets.TrustMode = TrustModeDev
		logs, err := f.load(t)
		require.NoError(t, err)
		assert.Contains(t, logs, `"trust_state":"transitional"`)
		_, ok := f.reg.lookup("adm.mod@1.0.0")
		assert.False(t, ok)
	})
}

// AC-162-08: the dev opt-in admits a module with NO signature material only: trust_state dev, no capability, the
// registry is never asked. Envelope or .sig without a certificate is not "unsigned" and is refused.
func TestAdmission_AC08_DevOptIn(t *testing.T) {
	t.Run("unsigned cic-flow module, dev opt-in", func(t *testing.T) {
		f := newAdmFixture(t, ociFlowManifest, nil)
		for _, s := range []string{".pem", envelopeSuffix, signatureSuffix} {
			delete(f.fsys, "m"+s)
		}
		f.assets.TrustMode, f.assets.AllowUnsignedModules = TrustModeDev, true
		called := false
		registerModuleCapability = func(string, []byte, string, string) error { called = true; return nil }
		logs, err := f.load(t)
		require.NoError(t, err)
		assert.Contains(t, logs, `"trust_state":"dev"`)
		assert.NotContains(t, logs, `"trust_state":"authenticated"`)
		assert.False(t, called, "a dev module never reaches the capability binding")
		assert.True(t, f.registered())
	})
	for _, keep := range []string{envelopeSuffix, signatureSuffix} {
		t.Run("no certificate but "+keep+" present", func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			delete(f.fsys, "m.pem")
			for _, s := range []string{envelopeSuffix, signatureSuffix} {
				if s != keep {
					delete(f.fsys, "m"+s)
				}
			}
			f.assets.TrustMode, f.assets.AllowUnsignedModules = TrustModeDev, true
			requireRejected(t, f, codeModuleRejected)
		})
	}
}

// AC-162-11: #155 does not regress: with a valid envelope present, a garbage or unparsable certificate, an
// untrusted chain or no trust store is still refused (the certificate is checked before the envelope).
func TestAdmission_AC11_Issue155FailClosed(t *testing.T) {
	cases := map[string]func(*admFixture){
		"not a PEM": func(f *admFixture) { f.fsys["m.pem"] = &fstest.MapFile{Data: []byte("not a pem")} },
		"PEM, not certificate": func(f *admFixture) {
			f.fsys["m.pem"] = &fstest.MapFile{Data: []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")}
		},
		"untrusted chain":       func(f *admFixture) { f.fsys["m.pem"] = &fstest.MapFile{Data: generateForeignLeafPEM(t)} },
		"no trust store loaded": func(f *admFixture) { f.assets.TrustStoreLoaded = false },
		"unsigned, no opt-in": func(f *admFixture) {
			for _, s := range []string{".pem", envelopeSuffix, signatureSuffix} {
				delete(f.fsys, "m"+s)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			breakIt(f)
			_, err := f.load(t)
			require.Error(t, err)
			assert.False(t, f.registered())
			assert.Equal(t, "", f.reg.bindingFor("adm.mod@1.0.0"))
		})
	}
}

// AC-162-12 / AC-162-17: the executed (stripped) representation differs from executed_wasm_sha256 while the raw
// digest matches → EXECUTED_DIGEST_MISMATCH.
func TestAdmission_AC12_AC17_ExecutedRepresentationDiffers(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, wasmWithCustom)
	require.NotEqual(t, sha256Ref(wasmWithCustom), sha256Ref(executedOf(t, wasmWithCustom)), "fixture: strip changes the bytes")
	f.resign(t, func(e *moduleReleaseEnvelope) { e.ExecutedWasmSHA256 = sha256Ref(wasmWithCustom) }) // raw as executed
	requireRejected(t, f, codeExecutedDigestMismatch)

	// control: the same release with the real executed digest is admitted
	f.resign(t, nil)
	_, err := f.load(t)
	require.NoError(t, err)
	stored, err := f.svc.GetModule("adm.mod@1.0.0")
	require.NoError(t, err)
	assert.Equal(t, sha256Ref(executedOf(t, wasmWithCustom)), sha256Ref(stored.WasmCode), "the stored bytes are the authenticated executed bytes")
}

// AC-162-13: a key present in a valid certificate but without the module:sign authority → SIGNER_NOT_AUTHORIZED.
func TestAdmission_AC13_SignerNotAuthorized(t *testing.T) {
	for name, set := range map[string]func(*PKIAssets){
		"no bindings at all":  func(a *PKIAssets) { a.ModuleSignAuthorities = nil },
		"key not listed":      func(a *PKIAssets) { a.ModuleSignAuthorities = map[string]bool{sha256Ref([]byte("other")): true} },
		"key listed, no role": func(a *PKIAssets) { a.ModuleSignAuthorities = map[string]bool{testModuleSigner(t).keyID: false} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newAdmFixture(t, ociFlowManifest, nil)
			set(f.assets)
			requireRejected(t, f, codeSignerNotAuthorized)
		})
	}
}

// AC-162-15: metadata.sign stays its own claim. The relay neither treats it as an envelope (with it but without an
// envelope a prod cic-flow module is still rejected) nor lets the envelope free it (editing it after the envelope was
// signed is a manifest change).
func TestAdmission_AC15_MetadataSignStaysSeparate(t *testing.T) {
	withSign := func(t *testing.T) *admFixture {
		f := newAdmFixture(t, ociFlowManifest, nil)
		y := strings.Replace(string(f.fsys["m.yaml"].Data), "  buildHash:", "  sign: MEUCIQ-producer-signature\n  buildHash:", 1)
		f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(y)}
		f.resign(t, nil)
		return f
	}
	t.Run("present, with envelope: admitted on the envelope", func(t *testing.T) {
		f := withSign(t)
		logs, err := f.load(t)
		require.NoError(t, err)
		assert.Contains(t, logs, `"trust_state":"authenticated"`)
	})
	t.Run("present, no envelope: does not replace the envelope", func(t *testing.T) {
		f := withSign(t)
		delete(f.fsys, "m"+envelopeSuffix)
		delete(f.fsys, "m"+signatureSuffix)
		requireRejected(t, f, codeModuleRejected)
	})
	t.Run("edited after the envelope was signed", func(t *testing.T) {
		f := withSign(t)
		y := strings.Replace(string(f.fsys["m.yaml"].Data), "producer-signature", "another-signature", 1)
		f.fsys["m.yaml"] = &fstest.MapFile{Data: []byte(y)}
		requireRejected(t, f, codeManifestDigestMismatch)
	})
}

// countingFS serves a MapFS but counts content reads per path and, for the paths in swap, serves the replacement
// from the second read on: an attacker who swaps a file after the relay read it once.
type countingFS struct {
	fstest.MapFS
	mu    sync.Mutex
	reads map[string]int
	swap  map[string][]byte
}

// ReadFile counts the read and serves the swapped content after the first read.
func (c *countingFS) ReadFile(name string) ([]byte, error) {
	c.mu.Lock()
	c.reads[name]++
	n := c.reads[name]
	c.mu.Unlock()
	if b, ok := c.swap[name]; ok && n > 1 {
		return append([]byte(nil), b...), nil
	}
	return c.MapFS.ReadFile(name)
}

// Open counts an open of a file as a read too (fs.ReadFile falls back to Open on other filesystems).
func (c *countingFS) Open(name string) (fs.File, error) {
	if !strings.HasSuffix(name, ".") && filepath.Ext(name) != "" {
		c.mu.Lock()
		c.reads["open:"+name]++
		c.mu.Unlock()
	}
	return c.MapFS.Open(name)
}

// AC-162-14 / 14b / 19: one read. Through the production walker (loadComponentsFromFS) the manifest and the wasm are
// each read exactly once; the bytes hashed are the bytes stored and the bytes the capability parse sees, so a file
// swapped after that read can not get in.
func TestAdmission_AC14_AC19_SingleReadNoTOCTOU(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	genuineManifest := append([]byte(nil), f.fsys["m.yaml"].Data...)
	evilWasm := wasmWithCustom
	evilManifest := []byte(strings.Replace(string(genuineManifest), `"*.oraclecloud.com"`, `"*.example.com"`, 1))
	cfs := &countingFS{MapFS: f.fsys, reads: map[string]int{}, swap: map[string][]byte{"m.wasm": evilWasm, "m.yaml": evilManifest}}

	var seenManifest []byte
	registerModuleCapability = func(id string, y []byte, artifact, env string) error {
		seenManifest = append([]byte(nil), y...)
		return f.reg.stageVerified(id, y, artifact, env)
	}
	require.NoError(t, loadComponentsFromFS(context.Background(), f.svc, cfs, ".", true, f.assets, int64(16*1024*1024), newDiscardLogger()))

	assert.Equal(t, 1, cfs.reads["m.wasm"], "the wasm is read exactly once")
	assert.Equal(t, 1, cfs.reads["m.yaml"], "the manifest is read exactly once")
	for _, k := range []string{"open:m.wasm", "open:m.yaml"} {
		assert.Zero(t, cfs.reads[k], "no second read through Open: %s", k)
	}
	stored, err := f.svc.GetModule("adm.mod@1.0.0")
	require.NoError(t, err)
	assert.Equal(t, sha256Ref(executedOf(t, f.fsys["m.wasm"].Data)), sha256Ref(stored.WasmCode), "the stored module is built from the hashed buffer, not the swapped file")
	assert.Equal(t, genuineManifest, seenManifest, "AC-162-14b: the capability parse sees the hashed manifest buffer")
	deps, ok := f.reg.lookup("adm.mod@1.0.0")
	require.True(t, ok)
	assert.Equal(t, "*.oraclecloud.com", deps.egress[0].Host, "the swapped (wider) manifest never reaches the policy")
}

// The envelope file's strict form (V1): every malformed envelope is ENVELOPE_INVALID (or ENVELOPE_VERSION) before
// any signature work, and the signature file's form is SIG_ENCODING.
func TestAdmission_EnvelopeAndSignatureForm(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	good := append([]byte(nil), f.fsys["m"+envelopeSuffix].Data...)
	var m map[string]any
	require.NoError(t, json.Unmarshal(good, &m))
	with := func(k string, v any) []byte {
		c := map[string]any{}
		for kk, vv := range m {
			c[kk] = vv
		}
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		return b
	}
	envCases := map[string]struct {
		data []byte
		code string
	}{
		"not JSON":            {[]byte("{"), codeEnvelopeInvalid},
		"trailing data":       {append(append([]byte(nil), good...), []byte(" {}")...), codeEnvelopeInvalid},
		"unknown field":       {with("capability_manifest", map[string]any{}), codeEnvelopeInvalid},
		"wrong kind":          {with("kind", "Other"), codeEnvelopeInvalid},
		"missing version":     {with("envelope_version", nil), codeEnvelopeVersion},
		"version as string":   {with("envelope_version", "1"), codeEnvelopeVersion},
		"bad module_id":       {with("module_id", "Adm@1"), codeEnvelopeInvalid},
		"uppercase digest":    {with("wasm_sha256", strings.ToUpper(m["wasm_sha256"].(string))), codeEnvelopeInvalid},
		"missing manifest":    {with("manifest_digest", nil), codeEnvelopeInvalid},
		"alg not allowlisted": {with("signer", map[string]any{"key_id": f.signer.keyID, "alg": "rsa-pkcs1-sha1"}), codeEnvelopeInvalid},
		"alg vs key type":     {with("signer", map[string]any{"key_id": f.signer.keyID, "alg": "ed25519"}), codeAlgMismatch},
		"module_id differs":   {with("module_id", "other.mod@1.0.0"), codeSignatureInvalid},
	}
	for name, c := range envCases {
		t.Run("envelope "+name, func(t *testing.T) {
			f.fsys["m"+envelopeSuffix] = &fstest.MapFile{Data: c.data}
			requireRejected(t, f, c.code)
		})
	}
	f.fsys["m"+envelopeSuffix] = &fstest.MapFile{Data: good}
	sig := string(f.fsys["m"+signatureSuffix].Data)
	for name, s := range map[string]string{
		"no final LF":   strings.TrimSuffix(sig, "\n"),
		"two lines":     sig[:10] + "\n" + sig[10:],
		"space":         sig[:10] + " " + sig[10:],
		"CRLF":          strings.TrimSuffix(sig, "\n") + "\r\n",
		"url alphabet":  strings.NewReplacer("+", "-", "/", "_").Replace(sig) + "",
		"no padding":    strings.TrimRight(strings.TrimSuffix(sig, "\n"), "=") + "\n",
		"empty":         "\n",
		"not base64":    "!!!!\n",
		"valid but bad": base64.StdEncoding.EncodeToString([]byte("not a signature")) + "\n",
	} {
		t.Run("signature "+name, func(t *testing.T) {
			if name == "url alphabet" && !strings.ContainsAny(sig, "+/") {
				t.Skip("this signature has no +/ characters")
			}
			if name == "no padding" && !strings.Contains(sig, "=") {
				t.Skip("this signature has no padding")
			}
			f.fsys["m"+signatureSuffix] = &fstest.MapFile{Data: []byte(s)}
			want := codeSigEncoding
			if name == "valid but bad" {
				want = codeSignatureInvalid
			}
			requireRejected(t, f, want)
		})
	}
}

// The envelope module_id must be the manifest's module ID: a valid signature over another module's identity does
// not admit this one.
func TestAdmission_EnvelopeModuleIDMustMatchManifest(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	f.resign(t, func(e *moduleReleaseEnvelope) { e.ModuleID = "other.mod@1.0.0" })
	requireRejected(t, f, codeEnvelopeInvalid)
}

// An Ed25519 signer is admitted the same way (ENV-02 allowlist).
func TestAdmission_Ed25519Signer(t *testing.T) {
	_, signer := ed25519TestSigner(t)
	f := newAdmFixture(t, ociFlowManifest, nil)
	f.signer = signer
	f.assets = f.signer.authorize(f.assets)
	f.resign(t, nil)
	logs, err := f.load(t)
	require.NoError(t, err)
	assert.Contains(t, logs, `"trust_state":"authenticated"`)
}

// AC-162-18 (default build): SetStripWasmModuleFunc is not part of any production build of core/cabinet, and no
// non-test file there assigns the transformation. (The tagged half, cic_test_seams, is admission_strip_seam_test.go.)
func TestAdmission_AC18_StripSeamNotInProductionBuild(t *testing.T) {
	pkg, err := build.Default.ImportDir("../../core/cabinet", 0)
	require.NoError(t, err)
	fset := token.NewFileSet()
	assignments := 0
	for _, name := range pkg.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, name), nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				assert.NotEqual(t, "SetStripWasmModuleFunc", d.Name.Name, "%s declares the strip seam in a production build", name)
			case *ast.AssignStmt:
				for _, lhs := range d.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "stripWasmModuleFunc" {
						assignments++
						t.Errorf("%s assigns stripWasmModuleFunc in a production build", name)
					}
				}
			}
			return true
		})
	}
	assert.Zero(t, assignments)
	// The tag-only file exists and is excluded from the default build.
	assert.Contains(t, pkg.IgnoredGoFiles, "strip_seam_testhook.go")
	// No production file of the relay names it.
	relay, err := build.Default.ImportDir(".", 0)
	require.NoError(t, err)
	for _, name := range relay.GoFiles {
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.NotContains(t, string(src), "SetStripWasmModuleFunc", name)
	}
}

// The testdata module signer is RSA: no envelope it could sign is admissible (ENV-02 allowlist; the declared
// algorithm must match the certificate key type), so an RSA-keyed release is refused with ALG_MISMATCH.
func TestAdmission_RSASignerCanNotSignAnEnvelope(t *testing.T) {
	f := newAdmFixture(t, ociFlowManifest, nil)
	rsaCert, err := fs.ReadFile(getSubFS(t, "pki/signers/sources/module"), "ee.pem")
	require.NoError(t, err)
	rsaSigner := newEnvelopeSigner(t, rsaCert, f.signer.key) // the key is never used: the check stops before
	env := envelopeFor(t, f.fsys, "m.yaml", rsaSigner)
	env.Signer.Alg = algECDSAP256
	f.fsys["m.pem"] = &fstest.MapFile{Data: rsaCert}
	writeEnvelope(t, f.fsys, "m.yaml", env, []byte(base64.StdEncoding.EncodeToString([]byte("x"))+"\n"))
	f.assets.ModuleSignAuthorities[rsaSigner.keyID] = true
	requireRejected(t, f, codeAlgMismatch)
}

// The pinned module-sign authority bindings (V2b): strict parsing, and the start-up loader.
func TestModuleSignAuthorities_ParseAndLoad(t *testing.T) {
	k1, k2 := sha256Ref([]byte("k1")), sha256Ref([]byte("k2"))
	ok := `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"` + k1 + `","roles":["module:sign"]},{"key_id":"` + k2 + `","roles":[]}]}`
	got, err := parseModuleSignAuthorities([]byte(ok))
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{k1: true, k2: false}, got, "a listed key without the role holds no authority")

	for name, bad := range map[string]string{
		"not JSON":      `{`,
		"unknown field": `{"kind":"ModuleSignAuthorities","version":1,"authorities":[],"x":1}`,
		"wrong kind":    `{"kind":"Other","version":1,"authorities":[]}`,
		"wrong version": `{"kind":"ModuleSignAuthorities","version":2,"authorities":[]}`,
		"bad key_id":    `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"sha256:ABC","roles":["module:sign"]}]}`,
		"unknown role":  `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"` + k1 + `","roles":["artifact:sign"]}]}`,
		"duplicate key": `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"` + k1 + `","roles":["module:sign"]},{"key_id":"` + k1 + `","roles":[]}]}`,
	} {
		_, err := parseModuleSignAuthorities([]byte(bad))
		assert.Error(t, err, name)
	}

	t.Run("unset: no authority, no error", func(t *testing.T) {
		t.Setenv(moduleSignAuthoritiesEnv, "")
		a := &PKIAssets{}
		require.NoError(t, loadModuleSignAuthorities(a, newDiscardLogger()))
		assert.Nil(t, a.ModuleSignAuthorities)
	})
	t.Run("set and valid", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "authorities.json")
		require.NoError(t, os.WriteFile(p, []byte(ok), 0o600))
		t.Setenv(moduleSignAuthoritiesEnv, p)
		a := &PKIAssets{}
		require.NoError(t, loadModuleSignAuthorities(a, newDiscardLogger()))
		assert.True(t, a.ModuleSignAuthorities[k1])
	})
	t.Run("set but unreadable", func(t *testing.T) {
		t.Setenv(moduleSignAuthoritiesEnv, filepath.Join(t.TempDir(), "missing.json"))
		assert.Error(t, loadModuleSignAuthorities(&PKIAssets{}, newDiscardLogger()))
	})
	t.Run("set but invalid", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "authorities.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"kind":"x"}`), 0o600))
		t.Setenv(moduleSignAuthoritiesEnv, p)
		assert.Error(t, loadModuleSignAuthorities(&PKIAssets{}, newDiscardLogger()))
	})
}

// bootstrapTrust records the trust mode in the assets: the V6 rule reads it.
func TestBootstrapTrust_RecordsTrustMode(t *testing.T) {
	t.Setenv("CIC_TRUST_MODE", TrustModeDev)
	t.Setenv(allowUnsignedEnv, "")
	_, a, err := bootstrapTrust(embeddedFS, newDiscardLogger())
	require.NoError(t, err)
	assert.Equal(t, TrustModeDev, a.TrustMode)
	assert.False(t, requiresAuthentication(a))
	assert.True(t, requiresAuthentication(&PKIAssets{TrustMode: TrustModeProd}))
	assert.True(t, requiresAuthentication(nil))
}

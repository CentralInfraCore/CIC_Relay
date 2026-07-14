//go:build ffi_cgo

// Command trustflow-demo is a live, self-contained demonstration of the
// feature-008/009 FFI trust-flow: the Go relay hands one request to the Rust
// airlock (cic-ffi), and the entire trust-flow runs *inside Rust* against a real
// (throwaway) Vault — mint a per-request module identity, have Vault countersign
// it, log in over mTLS cert-auth, read the pubkey-sealed secret, open it inside
// the module, and actuate over HTTP with the opened credential. Only the outcome
// crosses back; the secret is sealed in transit and opened only inside the module.
//
// It expects VAULT_LIVE_ADDR / VAULT_LIVE_CACERT (and the cic-ffi live staticlib
// linked) in the environment — set by scripts/trustflow-demo.sh. Run via
// `make demo-trustflow`.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"centralrelay/pkg/cicffi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[!] demo failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("VAULT_LIVE_ADDR")
	if addr == "" {
		return fmt.Errorf("VAULT_LIVE_ADDR unset — run via `make demo-trustflow`")
	}
	caPem, err := os.ReadFile(os.Getenv("VAULT_LIVE_CACERT"))
	if err != nil {
		return fmt.Errorf("read CA cert: %w", err)
	}

	// The endpoint the actuation targets; it captures the credential it received.
	const secret = "hvs.LIVE-DEMO-SECRET"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}

	fmt.Print("[*] Go relay hands ONE request to the Rust FFI (cic-ffi); the whole\n" +
		"    trust-flow runs inside Rust against live Vault — only the outcome returns.\n\n")
	fmt.Println("[*] Rust trust-flow steps (all inside the airlock):")
	fmt.Println("      ModuleIdentity::generate  — per-request P-256 key + CSR (key never exported)")
	fmt.Println("      Vault PKI countersign     — sign-verbatim; the CA key stays in Vault")
	fmt.Println("      mTLS cert-auth login      — countersigned cert in the TLS handshake → scoped token")
	fmt.Println("      read_sealed (KV)          — the pubkey-sealed secret envelope")
	fmt.Println("      module.open (ECIES)       — plaintext exists ONLY inside the module")
	fmt.Print("      HTTP actuation            — the opened credential applied as a Bearer token\n\n")

	reqJSON, err := json.Marshal(map[string]any{
		"profile":     "live",
		"subject":     "relay.worker",
		"scope":       []string{"db.read"},
		"secret":      secret,
		"target":      srv.URL + "/x",
		"allow_hosts": []string{"127.0.0.1"},
		"insecure":    true,
		"trace_id":    "trustflow-demo-1",
		"vault": map[string]any{
			"addr":              addr,
			"token":             envOr("VAULT_LIVE_TOKEN", "root"),
			"cacert_pem_base64": base64.StdEncoding.EncodeToString(caPem),
			"pki_mount":         envOr("VAULT_LIVE_PKI_MOUNT", "cic-pki"),
			"pki_role":          envOr("VAULT_LIVE_PKI_ROLE", "cic-module"),
			"cert_mount":        envOr("VAULT_LIVE_CERT_MOUNT", "cic-cert"),
			"cert_role":         envOr("VAULT_LIVE_CERT_ROLE", "cic-module"),
			"kv_mount":          envOr("VAULT_LIVE_KV_MOUNT", "cic-kv"),
			"kv_path":           envOr("VAULT_LIVE_KV_PATH", "module-secret"),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	out, err := cicffi.RunFlow(reqJSON)
	if err != nil {
		return fmt.Errorf("RunFlow rejected the request: %w", err)
	}
	res, err := cicffi.ParseRunFlowResult(out)
	if err != nil {
		return fmt.Errorf("parse result: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("live flow failed: %s", res.Error)
	}

	body, _ := base64.StdEncoding.DecodeString(res.BodyBase64)
	fmt.Printf("[*] outcome crossed back to Go: status=%d body=%q scope=%v\n", res.Status, body, res.Scope)
	if res.Audit != nil {
		fmt.Printf("    non-secret audit: decision=%s method=%s host=%s record_hash=%s\n",
			res.Audit.Decision, res.Audit.Method, res.Audit.Host, short(res.Audit.RecordHash))
	}

	fmt.Printf("\n[*] the endpoint received the opened credential as: %q\n", gotAuth)
	if gotAuth != "Bearer "+secret {
		return fmt.Errorf("expected the sealed secret to arrive as a Bearer token, got %q", gotAuth)
	}
	fmt.Println("    → sealed in Vault → read_sealed → opened INSIDE the module → applied as Bearer.")

	if bytes.Contains(out, []byte(secret)) {
		return fmt.Errorf("the secret leaked into the FFI result")
	}
	fmt.Print("\n[ok] the secret NEVER crossed back through the FFI seam — only the outcome did.\n" +
		"     Go → Rust → live Vault → HTTP; the plaintext existed only inside the module.\n")
	return nil
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + ".."
}

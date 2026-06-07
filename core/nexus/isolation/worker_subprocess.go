package isolation

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// SubprocessWorker implements Worker by spawning a subprocess for each Decrypt call.
// Communication is via stdin/stdout using newline-delimited JSON (IPCRequest / IPCResponse).
//
// Each Decrypt call spawns one subprocess, sends one request, reads one response, and waits.
// This is intentionally simple — no connection pooling, no persistent worker process.
//
// Activation: set CIC_ISOLATION_MODE=subprocess. Without it the existing LocalWorker
// (in-process) is used, preserving backward compatibility.
type SubprocessWorker struct {
	executable string   // path to the relay binary
	args       []string // extra args passed to the subprocess (e.g. --mode=module-worker)
	env        []string // environment for the subprocess; nil = inherit os.Environ()
}

// NewSubprocessWorker returns a SubprocessWorker that re-executes the running binary
// with --mode=module-worker.
func NewSubprocessWorker() (*SubprocessWorker, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}
	return &SubprocessWorker{
		executable: exe,
		args:       []string{"--mode=module-worker"},
	}, nil
}

// newSubprocessWorkerWithExe constructs a SubprocessWorker with explicit executable, args, and env.
// Intended for tests only.
func newSubprocessWorkerWithExe(executable string, args []string, env []string) *SubprocessWorker {
	return &SubprocessWorker{executable: executable, args: args, env: env}
}

// Decrypt spawns a subprocess, sends the encrypted payload, and returns the decrypted result.
func (w *SubprocessWorker) Decrypt(ctx context.Context, req DecryptRequest) (*DecryptResponse, error) {
	cmd := exec.CommandContext(ctx, w.executable, w.args...)
	if w.env != nil {
		cmd.Env = w.env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("subprocess stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("subprocess stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("subprocess start: %w", err)
	}

	// Send request.
	ipcReq := IPCRequest{
		Op:      "decrypt",
		Payload: base64.StdEncoding.EncodeToString(req.Payload),
		KeyID:   req.KeyID,
	}
	if err := json.NewEncoder(stdin).Encode(ipcReq); err != nil {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		return nil, fmt.Errorf("write request to subprocess: %w", err)
	}
	// Signal EOF so the worker loop exits after processing the single request.
	if err := stdin.(io.Closer).Close(); err != nil {
		return nil, fmt.Errorf("close subprocess stdin: %w", err)
	}

	// Read response.
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		cmd.Wait() //nolint:errcheck
		return nil, fmt.Errorf("subprocess produced no response")
	}
	var ipcResp IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &ipcResp); err != nil {
		cmd.Wait() //nolint:errcheck
		return nil, fmt.Errorf("parse subprocess response: %w", err)
	}
	cmd.Wait() //nolint:errcheck

	if ipcResp.Error != "" {
		return &DecryptResponse{Error: fmt.Errorf("%s", ipcResp.Error)}, nil
	}

	result, err := base64.StdEncoding.DecodeString(ipcResp.Result)
	if err != nil {
		return nil, fmt.Errorf("decode subprocess result base64: %w", err)
	}
	return &DecryptResponse{Payload: result}, nil
}

package isolation

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// IPCRequest is the JSON message sent from the coordinator to the worker via stdin.
// Payload is base64-encoded to allow arbitrary binary data over newline-delimited JSON.
type IPCRequest struct {
	Op      string `json:"op"`      // currently only "decrypt"
	Payload string `json:"payload"` // base64-encoded ciphertext
	KeyID   string `json:"key_id"`
}

// IPCResponse is the JSON message sent from the worker to the coordinator via stdout.
type IPCResponse struct {
	Result string `json:"result"` // base64-encoded plaintext; empty on error
	Error  string `json:"error"`  // non-empty on failure
}

// RunWorkerLoop reads newline-delimited JSON IPCRequests from in, processes each with worker,
// and writes IPCResponse lines to out. Runs until in is exhausted or ctx is cancelled.
//
// If worker is nil the loop operates in echo mode: the payload is returned unchanged.
// Echo mode is used for testing and for subprocess startup before a crypto service is wired in.
func RunWorkerLoop(ctx context.Context, in io.Reader, out io.Writer, worker Worker) {
	scanner := bufio.NewScanner(in)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var req IPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			enc.Encode(IPCResponse{Error: fmt.Sprintf("invalid request: %v", err)}) //nolint:errcheck
			continue
		}

		payload, err := base64.StdEncoding.DecodeString(req.Payload)
		if err != nil {
			enc.Encode(IPCResponse{Error: fmt.Sprintf("invalid base64 payload: %v", err)}) //nolint:errcheck
			continue
		}

		var result []byte
		if worker == nil {
			// Echo mode: return payload unchanged (no crypto).
			result = payload
		} else {
			resp, err := worker.Decrypt(ctx, DecryptRequest{Payload: payload, KeyID: req.KeyID})
			if err != nil {
				enc.Encode(IPCResponse{Error: err.Error()}) //nolint:errcheck
				continue
			}
			if resp.Error != nil {
				enc.Encode(IPCResponse{Error: resp.Error.Error()}) //nolint:errcheck
				continue
			}
			result = resp.Payload
		}

		enc.Encode(IPCResponse{ //nolint:errcheck
			Result: base64.StdEncoding.EncodeToString(result),
		})
	}
}

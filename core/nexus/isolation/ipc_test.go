package isolation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain allows the test binary to act as a subprocess worker in echo mode.
// When CIC_WORKER_TEST_MODE=echo is set the process runs RunWorkerLoop and exits,
// instead of running the test suite. This enables the subprocess round-trip test
// without needing a separately compiled binary.
func TestMain(m *testing.M) {
	if os.Getenv("CIC_WORKER_TEST_MODE") == "echo" {
		RunWorkerLoop(context.Background(), os.Stdin, os.Stdout, nil)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// --- RunWorkerLoop unit tests ---

func TestRunWorkerLoop_Echo(t *testing.T) {
	input := []byte("hello subprocess world")
	encoded := base64.StdEncoding.EncodeToString(input)
	req := `{"op":"decrypt","payload":"` + encoded + `","key_id":""}` + "\n"

	var out bytes.Buffer
	RunWorkerLoop(context.Background(), strings.NewReader(req), &out, nil)

	var resp IPCResponse
	require.NoError(t, json.NewDecoder(&out).Decode(&resp))
	assert.Empty(t, resp.Error)

	result, err := base64.StdEncoding.DecodeString(resp.Result)
	require.NoError(t, err)
	assert.Equal(t, input, result)
}

func TestRunWorkerLoop_MultipleRequests(t *testing.T) {
	payloads := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	var lines strings.Builder
	for _, p := range payloads {
		lines.WriteString(`{"op":"decrypt","payload":"` + base64.StdEncoding.EncodeToString(p) + `","key_id":""}` + "\n")
	}

	var out bytes.Buffer
	RunWorkerLoop(context.Background(), strings.NewReader(lines.String()), &out, nil)

	dec := json.NewDecoder(&out)
	for _, want := range payloads {
		var resp IPCResponse
		require.NoError(t, dec.Decode(&resp))
		assert.Empty(t, resp.Error)
		got, err := base64.StdEncoding.DecodeString(resp.Result)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestRunWorkerLoop_InvalidJSON(t *testing.T) {
	var out bytes.Buffer
	RunWorkerLoop(context.Background(), strings.NewReader("not valid json\n"), &out, nil)

	var resp IPCResponse
	require.NoError(t, json.NewDecoder(&out).Decode(&resp))
	assert.NotEmpty(t, resp.Error)
	assert.Contains(t, resp.Error, "invalid request")
}

func TestRunWorkerLoop_InvalidBase64(t *testing.T) {
	var out bytes.Buffer
	RunWorkerLoop(context.Background(), strings.NewReader(`{"op":"decrypt","payload":"!!!","key_id":""}`+"\n"), &out, nil)

	var resp IPCResponse
	require.NoError(t, json.NewDecoder(&out).Decode(&resp))
	assert.NotEmpty(t, resp.Error)
	assert.Contains(t, resp.Error, "invalid base64")
}

func TestRunWorkerLoop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	input := base64.StdEncoding.EncodeToString([]byte("payload"))
	req := `{"op":"decrypt","payload":"` + input + `","key_id":""}` + "\n"

	var out bytes.Buffer
	RunWorkerLoop(ctx, strings.NewReader(req), &out, nil)
	// Should exit early without writing a response.
	assert.Equal(t, 0, out.Len())
}

// --- SubprocessWorker round-trip integration test ---

func TestSubprocessWorker_RoundTrip(t *testing.T) {
	// Spawn this test binary in echo mode as the worker subprocess.
	// The TestMain above intercepts CIC_WORKER_TEST_MODE=echo and runs RunWorkerLoop,
	// so the subprocess acts as a pure echo worker without any real crypto dependency.
	exe := os.Args[0]
	env := append(os.Environ(), "CIC_WORKER_TEST_MODE=echo")

	worker := newSubprocessWorkerWithExe(exe, []string{"-test.run=^$"}, env)

	payload := []byte("round-trip test payload")
	resp, err := worker.Decrypt(context.Background(), DecryptRequest{Payload: payload})

	require.NoError(t, err)
	require.NoError(t, resp.Error)
	assert.Equal(t, payload, resp.Payload)
}
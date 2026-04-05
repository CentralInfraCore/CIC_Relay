package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"centralrelay/core/cabinet"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// assertErrorJSON checks that the response body is a valid error JSON with the expected code.
func assertErrorJSON(t *testing.T, w *httptest.ResponseRecorder, expectedCode string) {
	t.Helper()
	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	assert.NoError(t, err, "error response must be valid JSON")
	assert.Equal(t, expectedCode, body["code"], "error code mismatch")
	assert.NotEmpty(t, body["error"], "error message must not be empty")
}

// TestSetHandler_Success tests the happy path for the /set endpoint.
func TestSetHandler_Success(t *testing.T) {
	// 1. Setup
	// Create a mock cabinet API and tell it what to expect.
	mockAPI := new(mockCabinetService)
	// When the Set method is called with any context and any payload, return a specific response and no error.
	expectedResponse := &cabinet.SetResponse{Results: map[string]any{"status": "all good"}}
	mockAPI.On("GetWorkflow", mock.Anything).Return(&cabinet.Workflow{}, nil)
	mockAPI.On("GetModule", mock.Anything).Return(&cabinet.ModuleDescriptor{}, nil)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	// Create a logger that discards output for clean test logs.
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	// Create the server with our mock API.
	server := &relayServer{
		api: mockAPI,
		config: &config{
			MaxJSONInputSize: 1024 * 1024, // 1MB limit for test
			HashingTimeout:   5 * time.Second,
		},
		logger: logger,
	}

	// Create the HTTP request body.
	payload := map[string]any{
		"workflow_id": "test.workflow@1.0",
		"modules":    []string{"test.module@1.0"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 2. Execute
	// Directly call the handler function.
	server.setHandler(w, req)

	// 3. Verify
	// Check the HTTP status code.
	assert.Equal(t, http.StatusOK, w.Code, "Expected HTTP status 200 OK")

	// Check the response body.
	var responseBody cabinet.SetResponse
	err := json.Unmarshal(w.Body.Bytes(), &responseBody)
	assert.NoError(t, err, "Failed to unmarshal response body")
	assert.Equal(t, "all good", responseBody.Results["status"], "Response body does not match expected")

	// Verify that the mock API's Set method was called exactly once.
	mockAPI.AssertExpectations(t)
}

// TestSetHandler_MethodNotAllowed tests that the /set endpoint only accepts POST requests.
func TestSetHandler_MethodNotAllowed(t *testing.T) {
	// 1. Setup
	mockAPI := new(mockCabinetService) // The API won't even be called, but we need a server.
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api:    mockAPI,
		config: &config{MaxJSONInputSize: 1024},
		logger: logger,
	}

	// Create a GET request instead of POST.
	req := httptest.NewRequest(http.MethodGet, "/set", nil)
	w := httptest.NewRecorder()

	// 2. Execute
	server.setHandler(w, req)

	// 3. Verify
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "Expected HTTP status 405 Method Not Allowed")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "method_not_allowed")

	// Ensure the mock API was not called at all.
	mockAPI.AssertNotCalled(t, "Set")
}

// TestSetHandler_BodyTooLarge tests that the handler rejects bodies larger than the configured limit.
func TestSetHandler_BodyTooLarge(t *testing.T) {
	// 1. Setup
	mockAPI := new(mockCabinetService)
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api: mockAPI,
		config: &config{
			MaxJSONInputSize: 10, // A very small limit for testing.
		},
		logger: logger,
	}

	// Create a body that is larger than the 10-byte limit.
	largeBody := []byte("{\"key\": \"this is definitely more than 10 bytes\"}")

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 2. Execute
	server.setHandler(w, req)

	// 3. Verify
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "Expected HTTP status 413 Request Entity Too Large")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "body_too_large")

	// Ensure the mock API was not called.
	mockAPI.AssertNotCalled(t, "Set")
}

// TestSetHandler_InvalidJSON tests that the handler returns a 400 error for malformed JSON.
func TestSetHandler_InvalidJSON(t *testing.T) {
	// 1. Setup
	mockAPI := new(mockCabinetService)
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api:    mockAPI,
		config: &config{MaxJSONInputSize: 1024},
		logger: logger,
	}

	// Create a request with a malformed JSON body.
	invalidJSON := []byte(`{"key": "value"`) // Missing closing brace

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(invalidJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 2. Execute
	server.setHandler(w, req)

	// 3. Verify
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected HTTP status 400 Bad Request")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "invalid_json")

	// Ensure the mock API was not called.
	mockAPI.AssertNotCalled(t, "Set")
}

// TestSetHandler_CabinetInvalidError tests the handler's response to a validation error from the cabinet.
func TestSetHandler_CabinetInvalidError(t *testing.T) {
	// 1. Setup
	mockAPI := new(mockCabinetService)
	// Tell the mock to return an ErrInvalid error.
	mockAPI.On("GetWorkflow", mock.Anything).Return(&cabinet.Workflow{}, nil)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(nil, cabinet.ErrInvalid)

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api:    mockAPI,
		config: &config{MaxJSONInputSize: 1024},
		logger: logger,
	}

	// Create a valid request body, as the error happens deeper in the logic.
	validBody := []byte(`{"workflow_id": "any"}`)

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 2. Execute
	server.setHandler(w, req)

	// 3. Verify
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected HTTP status 400 for ErrInvalid")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "invalid_request")

	// Ensure the mock API was called.
	mockAPI.AssertCalled(t, "Setx", mock.Anything, mock.Anything)
}

// TestSetHandler_Timeout tests the handler's response to a timeout from the cabinet.
func TestSetHandler_Timeout(t *testing.T) {
	mockAPI := new(mockCabinetService)
	mockAPI.On("GetWorkflow", mock.Anything).Return(&cabinet.Workflow{}, nil)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("%w: step timed out", cabinet.ErrTimeout))

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api:    mockAPI,
		config: &config{MaxJSONInputSize: 1024},
		logger: logger,
	}

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader([]byte(`{"workflow_id":"any"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.setHandler(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code, "Expected HTTP 504 for timeout")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "timeout")
	mockAPI.AssertCalled(t, "Setx", mock.Anything, mock.Anything)
}

// TestSetHandler_CabinetInternalError tests the handler's response to an unexpected internal error.
func TestSetHandler_CabinetInternalError(t *testing.T) {
	// 1. Setup
	mockAPI := new(mockCabinetService)
	// Tell the mock to return a generic, non-validation error.
	expectedErr := fmt.Errorf("unexpected database error")
	mockAPI.On("GetWorkflow", mock.Anything).Return(&cabinet.Workflow{}, nil)
	mockAPI.On("Setx", mock.Anything, mock.Anything).Return(nil, expectedErr)

	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))

	server := &relayServer{
		api:    mockAPI,
		config: &config{MaxJSONInputSize: 1024},
		logger: logger,
	}

	// Create a valid request body.
	validBody := []byte(`{"workflow_id": "any"}`)

	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 2. Execute
	server.setHandler(w, req)

	// 3. Verify
	assert.Equal(t, http.StatusInternalServerError, w.Code, "Expected HTTP status 500 for internal errors")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assertErrorJSON(t, w, "internal_error")

	// Ensure the mock API was called.
	mockAPI.AssertCalled(t, "Setx", mock.Anything, mock.Anything)
}

// TestSimpleHandlers tests the simple, non-mutating endpoints like /healthz, /readyz, and /.
func TestSimpleHandlers(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(new(bytes.Buffer))
	server := &relayServer{logger: logger}

	testCases := []struct {
		name       string
		handler    http.HandlerFunc
		expected   string
		statusCode int
	}{
		{
			name:       "Health Check",
			handler:    server.healthzHandler,
			expected:   `{"status":"ok"}`,
			statusCode: http.StatusOK,
		},
		{
			name:       "Readiness Check",
			handler:    server.readyzHandler,
			expected:   `{"status":"ok"}`,
			statusCode: http.StatusOK,
		},
		{
			name:       "Root Handler",
			handler:    server.rootHandler,
			expected:   `{"status": "ok", "message": "central relay responding"}`,
			statusCode: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()
			tc.handler(w, req)

			assert.Equal(t, tc.statusCode, w.Code)
			assert.JSONEq(t, tc.expected, w.Body.String())
		})
	}
}

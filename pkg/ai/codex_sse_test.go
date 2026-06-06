package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// helper to build a basic Model for testing
func testModel(url string) Model {
	return Model{
		ID:            "gpt-5.3-codex-spark",
		Provider:      ProviderIDOpenAICodex,
		API:           APIIDOpenAICodexResponses,
		BaseURL:       url,
		ContextWindow: 128000,
	}
}

func TestStreamCodexSSE_Success(t *testing.T) {
	// 1. Build test token
	claims := map[string]any{
		"chatgpt_account_id": "acct_root_123",
	}
	token := makeFakeJWT(t, claims)

	// 2. Setup mock SSE server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify required headers are present
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("expected Authorization header with token, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("ChatGPT-Account-ID") != "acct_root_123" {
			t.Errorf("expected ChatGPT-Account-ID, got %q", r.Header.Get("ChatGPT-Account-ID"))
		}
		if r.Header.Get("OpenAI-Beta") != "responses=experimental" {
			t.Errorf("expected OpenAI-Beta responses=experimental, got %q", r.Header.Get("OpenAI-Beta"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Send SSE events
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_123\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.added\", \"item\": {\"type\": \"message\", \"id\": \"item_456\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_text.delta\", \"delta\": \"Hello\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_123\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 15, \"output_tokens\": 10, \"total_tokens\": 25, \"input_tokens_details\": {\"cached_tokens\": 5}}}}\n\n")
	}))
	defer srv.Close()

	// 3. Prepare parameters
	model := testModel(srv.URL)
	body := map[string]any{"dummy": "data"}
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	// 4. Test callbacks
	var onPayloadCalled, onRequestCalled, onResponseCalled int32
	opts.OnPayload = func(payload any, m Model) (any, bool, error) {
		atomic.AddInt32(&onPayloadCalled, 1)
		return payload, false, nil
	}
	opts.OnRequest = func(req *http.Request, raw []byte) {
		atomic.AddInt32(&onRequestCalled, 1)
	}
	opts.OnResponse = func(resp *http.Response) {
		atomic.AddInt32(&onResponseCalled, 1)
	}

	// 5. Execute stream
	ctx := context.Background()
	eventChan, err := StreamCodexSSE(ctx, model, body, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	// 6. Consume events
	var events []*CodexResponseStreamEvent
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected streaming error: %v", res.Err)
		}
		events = append(events, res.Event)
	}

	// 7. Verify result
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Check event types and content
	if events[0].Type != "response.created" || events[0].Response.ID != "resp_123" {
		t.Errorf("first event mismatch: %+v", events[0])
	}
	if events[1].Type != "response.output_item.added" || events[1].Item.ID != "item_456" {
		t.Errorf("second event mismatch: %+v", events[1])
	}
	if events[2].Type != "response.output_text.delta" || events[2].Delta != "Hello" {
		t.Errorf("third event mismatch: %+v", events[2])
	}
	if events[3].Type != "response.completed" || events[3].Response.ID != "resp_123" || events[3].Response.Status != "completed" {
		t.Errorf("fourth event mismatch: %+v", events[3])
	}

	// Verify nested details in response.completed
	usage := events[3].Response.Usage
	if usage == nil || usage.InputTokens != 15 || usage.OutputTokens != 10 || usage.InputTokensDetails.CachedTokens != 5 {
		t.Errorf("usage payload details missing/mismatched: %+v", usage)
	}

	// Verify callbacks were executed
	if atomic.LoadInt32(&onPayloadCalled) != 1 {
		t.Error("expected OnPayload to be called exactly once")
	}
	if atomic.LoadInt32(&onRequestCalled) != 1 {
		t.Error("expected OnRequest to be called exactly once")
	}
	if atomic.LoadInt32(&onResponseCalled) != 1 {
		t.Error("expected OnResponse to be called exactly once")
	}
}

func TestStreamCodexSSE_Timeout(t *testing.T) {
	// 1. Setup mock server that hangs on headers
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	timeoutMs := 20
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			TimeoutMs: &timeoutMs,
		},
	}

	// 2. Stream should time out waiting for headers
	_, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err == nil {
		t.Fatal("expected error due to header timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "Client.Timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestStreamCodexSSE_RetryableErrors(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count == 1 {
			w.WriteHeader(http.StatusBadGateway) // 502 (retryable)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	maxRetries := 2
	maxRetryDelayMs := 5
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:          token,
			MaxRetries:      &maxRetries,
			MaxRetryDelayMs: &maxRetryDelayMs,
		},
	}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	// Drain stream
	var events []*CodexResponseStreamEvent
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected streaming error: %v", res.Err)
		}
		events = append(events, res.Event)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "response.completed" {
		t.Errorf("expected response.completed, got %q", events[0].Type)
	}

	if atomic.LoadInt32(&requestCount) != 2 {
		t.Errorf("expected 2 request attempts, got %d", requestCount)
	}
}

func TestStreamCodexSSE_Terminal429(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusTooManyRequests) // 429
		w.Write([]byte(`{"error": {"code": "insufficient_quota", "message": "Monthly usage limit reached"}}`))
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	maxRetries := 2
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:     token,
			MaxRetries: &maxRetries,
		},
	}

	_, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "Monthly usage limit reached") {
		t.Errorf("expected terminal rate limit error message, got: %v", err)
	}

	// Ensure no retries occurred
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected exactly 1 attempt due to terminal error, got %d", requestCount)
	}
}

func TestStreamCodexSSE_MalformedSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {invalid json}\n\n")
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	var results []CodexStreamResult
	for res := range eventChan {
		results = append(results, res)
	}

	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result containing error, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected error in result, got nil")
	}
	if !strings.Contains(results[0].Err.Error(), "invalid Codex SSE JSON") {
		t.Errorf("expected json syntax error, got: %v", results[0].Err)
	}
}

func TestStreamCodexSSE_Cancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.created\"}\n\n")
		w.(http.Flusher).Flush()
		// sleep/block indefinitely to wait for client cancellation
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventChan, err := StreamCodexSSE(ctx, model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	var events []*CodexResponseStreamEvent
	var streamErr error

	for res := range eventChan {
		if res.Err != nil {
			streamErr = res.Err
			break
		}
		events = append(events, res.Event)
		// Cancel context immediately after receiving first event
		cancel()
	}

	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event before cancellation, got %d", len(events))
	}
	if events[0].Type != "response.created" {
		t.Errorf("expected response.created, got %q", events[0].Type)
	}
	if streamErr == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(streamErr, context.Canceled) {
		t.Errorf("expected context.Canceled error, got: %v", streamErr)
	}
}

func TestStreamCodexSSE_TokenFallback(t *testing.T) {
	// Set up temporary directory and write auth.json
	tempDir, err := os.MkdirTemp("", "pi-auth-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir := os.Getenv("PI_CODING_AGENT_DIR")
	defer os.Setenv("PI_CODING_AGENT_DIR", oldDir)
	os.Setenv("PI_CODING_AGENT_DIR", tempDir)

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct_fallback_999"})
	creds := map[string]any{
		"openai-codex": map[string]any{
			"type":    "oauth",
			"access":  token,
			"refresh": "refresh-123",
			"expires": time.Now().UnixMilli() + 3600000,
		},
	}

	credsData, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("failed to marshal credentials: %v", err)
	}
	err = os.WriteFile(filepath.Join(tempDir, "auth.json"), credsData, 0o600)
	if err != nil {
		t.Fatalf("failed to write auth.json: %v", err)
	}

	// Setup server to verify token from auth.json was used
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("expected fallback token in Authorization, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	// Empty APIKey to force fallback
	opts := &CodexResponsesOptions{}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	// Drain
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected stream error: %v", res.Err)
		}
	}
}

func TestStreamCodexSSE_TimeoutHeaderOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		// Delay body sending by more than the timeout duration
		time.Sleep(50 * time.Millisecond)

		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	timeoutMs := 20
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			TimeoutMs: &timeoutMs,
		},
	}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error starting stream: %v", err)
	}

	var events []*CodexResponseStreamEvent
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected stream error: %v", res.Err)
		}
		events = append(events, res.Event)
	}

	if len(events) != 1 || events[0].Type != "response.completed" {
		t.Errorf("expected 1 completed event, got: %+v", events)
	}
}

func TestStreamCodexSSE_RetryAfterAndCap(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count == 1 {
			w.Header().Set("retry-after-ms", "200")
			w.WriteHeader(http.StatusServiceUnavailable) // 503
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()
	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	maxRetries := 1
	maxRetryDelayMs := 10
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:          token,
			MaxRetries:      &maxRetries,
			MaxRetryDelayMs: &maxRetryDelayMs,
		},
	}
	var capturedDelay time.Duration
	oldNewTimer := timeNewTimer
	timeNewTimer = func(d time.Duration) *time.Timer {
		capturedDelay = d
		return time.NewTimer(0) // instantly fire timer
	}
	defer func() {
		timeNewTimer = oldNewTimer
	}()
	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected stream error: %v", res.Err)
		}
	}
	expectedCap := 10 * time.Millisecond
	if capturedDelay != expectedCap {
		t.Errorf("expected sleep duration to be capped at %v, got %v", expectedCap, capturedDelay)
	}
}

func TestStreamCodexSSE_APIKeyAuthoritativeOnRetry(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-auth-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir := os.Getenv("PI_CODING_AGENT_DIR")
	defer os.Setenv("PI_CODING_AGENT_DIR", oldDir)
	os.Setenv("PI_CODING_AGENT_DIR", tempDir)

	// auth.json token
	authToken := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "auth-json-acct"})
	creds := map[string]any{
		"openai-codex": map[string]any{
			"type":    "oauth",
			"access":  authToken,
			"refresh": "refresh-123",
			"expires": time.Now().UnixMilli() + 3600000,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(filepath.Join(tempDir, "auth.json"), credsData, 0o600)

	// APIKey override token
	overrideToken := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "override-acct"})

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&reqCount, 1)
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+overrideToken {
			t.Errorf("attempt %d: expected Authorization header to use overrideToken, got %q", count, authHeader)
		}
		if authHeader == "Bearer "+authToken {
			t.Errorf("attempt %d: Authorization header fell back to auth.json token!", count)
		}

		if count == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	maxRetries := 1
	maxRetryDelayMs := 5
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:          overrideToken,
			MaxRetries:      &maxRetries,
			MaxRetryDelayMs: &maxRetryDelayMs,
		},
	}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected stream error: %v", res.Err)
		}
	}
	if atomic.LoadInt32(&reqCount) != 2 {
		t.Errorf("expected 2 requests, got %d", reqCount)
	}
}

func TestStreamCodexSSE_OnPayloadReplacement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(bodyBytes, &body)
		if body["dummy"] != "replaced-data" {
			t.Errorf("expected replaced-data in request body, got %q", body["dummy"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	var onPayloadCalled, onRequestCalled int32
	opts.OnPayload = func(payload any, m Model) (any, bool, error) {
		atomic.AddInt32(&onPayloadCalled, 1)
		return map[string]any{"dummy": "replaced-data"}, true, nil
	}
	opts.OnRequest = func(req *http.Request, raw []byte) {
		atomic.AddInt32(&onRequestCalled, 1)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["dummy"] != "replaced-data" {
			t.Errorf("OnRequest: expected replaced-data, got %q", body["dummy"])
		}
	}

	eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{"dummy": "original-data"}, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for res := range eventChan {
		if res.Err != nil {
			t.Fatalf("unexpected stream error: %v", res.Err)
		}
	}

	if atomic.LoadInt32(&onPayloadCalled) != 1 {
		t.Error("expected OnPayload to be called exactly once")
	}
	if atomic.LoadInt32(&onRequestCalled) != 1 {
		t.Error("expected OnRequest to be called exactly once")
	}
}

func TestStreamCodexSSE_TerminalNormalizations(t *testing.T) {
	cases := []struct {
		name           string
		sseData        string
		expectedType   string
		expectedStatus string
		expectedCode   string
		expectedMsg    string
	}{
		{
			name:           "response.done -> response.completed",
			sseData:        "data: {\"type\": \"response.done\", \"response\": {\"id\": \"r1\", \"status\": \"completed\"}}\n\n",
			expectedType:   "response.completed",
			expectedStatus: "completed",
		},
		{
			name:           "response.incomplete -> response.completed",
			sseData:        "data: {\"type\": \"response.incomplete\", \"response\": {\"id\": \"r2\", \"status\": \"incomplete\"}}\n\n",
			expectedType:   "response.completed",
			expectedStatus: "incomplete",
		},
		{
			name:           "response.failed terminal",
			sseData:        "data: {\"type\": \"response.failed\", \"response\": {\"id\": \"r3\", \"status\": \"failed\", \"error\": {\"code\": \"failed_code\", \"message\": \"failed_msg\"}}}\n\n",
			expectedType:   "response.failed",
			expectedStatus: "failed",
			expectedCode:   "failed_code",
			expectedMsg:    "failed_msg",
		},
		{
			name:         "error terminal",
			sseData:      "data: {\"type\": \"error\", \"code\": \"err_code\", \"message\": \"err_msg\"}\n\n",
			expectedType: "error",
			expectedCode: "err_code",
			expectedMsg:  "err_msg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, tc.sseData)
				// Write a redundant extra event to prove iteration stops immediately on terminal event
				fmt.Fprint(w, "data: {\"type\": \"response.output_text.delta\", \"delta\": \"leak\"}\n\n")
			}))
			defer srv.Close()
			token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
			model := testModel(srv.URL)
			opts := &CodexResponsesOptions{
				StreamOptions: StreamOptions{
					APIKey: token,
				},
			}
			eventChan, err := StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
			if err != nil {
				t.Fatalf("unexpected error starting stream: %v", err)
			}
			var events []*CodexResponseStreamEvent
			for res := range eventChan {
				if res.Err != nil {
					t.Fatalf("unexpected streaming error: %v", res.Err)
				}
				events = append(events, res.Event)
			}
			// Assert only 1 event received (iteration stopped correctly on terminal event, ignoring the "leak" event)
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event, got %d: %+v", len(events), events)
			}
			ev := events[0]
			if ev.Type != tc.expectedType {
				t.Errorf("expected event type %q, got %q", tc.expectedType, ev.Type)
			}
			if tc.expectedStatus != "" {
				if ev.Response == nil || ev.Response.Status != tc.expectedStatus {
					t.Errorf("expected status %q, got %+v", tc.expectedStatus, ev.Response)
				}
			}
			if tc.expectedCode != "" {
				if ev.Code != tc.expectedCode && (ev.Response == nil || ev.Response.Error == nil || ev.Response.Error.Code != tc.expectedCode) {
					t.Errorf("expected code %q, got code %q (response: %+v)", tc.expectedCode, ev.Code, ev.Response)
				}
			}
			if tc.expectedMsg != "" {
				if ev.Message != tc.expectedMsg && (ev.Response == nil || ev.Response.Error == nil || ev.Response.Error.Message != tc.expectedMsg) {
					t.Errorf("expected message %q, got message %q (response: %+v)", tc.expectedMsg, ev.Message, ev.Response)
				}
			}
			expectedRaw := strings.TrimPrefix(tc.sseData, "data: ")
			expectedRaw = strings.TrimSpace(expectedRaw)
			if string(ev.Raw) != expectedRaw {
				t.Errorf("expected Raw payload to be %q, got %q", expectedRaw, string(ev.Raw))
			}
		})
	}
}

func TestStreamCodexSSE_RetryAuthFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-auth-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir := os.Getenv("PI_CODING_AGENT_DIR")
	defer os.Setenv("PI_CODING_AGENT_DIR", oldDir)
	os.Setenv("PI_CODING_AGENT_DIR", tempDir)

	// Setup initial valid token
	token := makeFakeJWT(t, map[string]any{"chatgpt_account_id": "acct"})
	creds := map[string]any{
		"openai-codex": map[string]any{
			"type":    "oauth",
			"access":  token,
			"refresh": "refresh-123",
			"expires": time.Now().UnixMilli() + 3600000,
		},
	}
	credsData, _ := json.Marshal(creds)
	authPath := filepath.Join(tempDir, "auth.json")
	_ = os.WriteFile(authPath, credsData, 0o600)

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&reqCount, 1)
		if count == 1 {
			// Corrupt the auth.json file during the first request
			_ = os.WriteFile(authPath, []byte("corrupted-json"), 0o600)
			w.WriteHeader(http.StatusBadGateway) // 502 -> trigger retry
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.completed\"}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	maxRetries := 1
	maxRetryDelayMs := 5
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			MaxRetries:      &maxRetries,
			MaxRetryDelayMs: &maxRetryDelayMs,
		},
	}

	_, err = StreamCodexSSE(context.Background(), model, map[string]any{}, opts)
	if err == nil {
		t.Fatal("expected failure on retry due to corrupted auth token, got nil")
	}

	if !strings.Contains(err.Error(), "failed to refresh token on retry") {
		t.Errorf("expected failed to refresh token on retry error, got: %v", err)
	}

	if atomic.LoadInt32(&reqCount) != 1 {
		t.Errorf("expected only 1 request attempt because retry failed fast during token refresh, got %d", reqCount)
	}
}

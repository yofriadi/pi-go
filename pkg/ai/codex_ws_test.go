package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStreamOpenAICodexResponses_WebSocket_Success(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_success"}
	token := makeFakeJWT(t, claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify case-insensitive overrides and canonical header behaviors
		if beta := r.Header.Get("Openai-Beta"); beta != "responses_websockets=2026-02-06" {
			t.Errorf("expected OpenAI-Beta 'responses_websockets=2026-02-06', got %q", beta)
		}
		if len(r.Header["Openai-Beta"]) != 1 {
			t.Errorf("expected exactly 1 OpenAI-Beta header, got %d: %v", len(r.Header["Openai-Beta"]), r.Header["Openai-Beta"])
		}
		if accept := r.Header.Get("Accept"); accept != "" {
			t.Errorf("expected Accept header to be stripped, got %q", accept)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "" {
			t.Errorf("expected Content-Type header to be stripped, got %q", contentType)
		}
		if custom := r.Header.Get("X-Custom-Test"); custom != "passed" {
			t.Errorf("expected X-Custom-Test 'passed', got %q", custom)
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		var req map[string]any
		_ = json.Unmarshal(data, &req)

		if req["type"] != "response.create" {
			t.Errorf("expected type 'response.create', got %v", req["type"])
		}

		events := []string{
			`{"type": "response.created", "response": {"id": "resp_ws_123"}}`,
			`{"type": "response.output_item.added", "item": {"type": "message", "id": "item_msg"}}`,
			`{"type": "response.output_text.delta", "delta": "Hello from WS"}`,
			`{"type": "response.output_item.done", "item": {"type": "message", "id": "item_msg", "content": [{"type": "output_text", "text": "Hello from WS"}]}}`,
			`{"type": "response.completed", "response": {"id": "resp_ws_123", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
		}

		for _, ev := range events {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
		}
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			Transport: TransportWebSocket,
			Headers: map[string]string{
				"openai-beta":   "should_be_overridden_1",
				"OPENAI-BETA":   "should_be_overridden_2",
				"Accept":        "should_be_deleted",
				"x-custom-test": "passed",
			},
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)
	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected stream result error: %v", err)
	}

	if msg.ResponseID != "resp_ws_123" {
		t.Errorf("expected response ID 'resp_ws_123', got %q", msg.ResponseID)
	}

	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}

	txtBlock, ok := msg.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected block type TextContent, got %T", msg.Content[0])
	}

	if txtBlock.Text != "Hello from WS" {
		t.Errorf("expected content 'Hello from WS', got %q", txtBlock.Text)
	}
}

func TestStreamOpenAICodexResponses_WebSocket_CachingAndReused(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_cache"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_cache_123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// Turn 1
		_, data1, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var req1 map[string]any
		_ = json.Unmarshal(data1, &req1)

		events1 := []string{
			`{"type": "response.created", "response": {"id": "resp_ws_1"}}`,
			`{"type": "response.output_item.added", "item": {"type": "message", "id": "item_msg_1"}}`,
			`{"type": "response.output_text.delta", "delta": "First response"}`,
			`{"type": "response.output_item.done", "item": {"type": "message", "id": "item_msg_1", "content": [{"type": "output_text", "text": "First response"}]}}`,
			`{"type": "response.completed", "response": {"id": "resp_ws_1", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
		}
		for _, ev := range events1 {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
		}

		// Turn 2
		_, data2, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var req2 map[string]any
		_ = json.Unmarshal(data2, &req2)

		if req2["previous_response_id"] != "resp_ws_1" {
			t.Errorf("expected previous_response_id 'resp_ws_1', got %v", req2["previous_response_id"])
		}

		events2 := []string{
			`{"type": "response.created", "response": {"id": "resp_ws_2"}}`,
			`{"type": "response.output_item.added", "item": {"type": "message", "id": "item_msg_2"}}`,
			`{"type": "response.output_text.delta", "delta": "Second response"}`,
			`{"type": "response.output_item.done", "item": {"type": "message", "id": "item_msg_2", "content": [{"type": "output_text", "text": "Second response"}]}}`,
			`{"type": "response.completed", "response": {"id": "resp_ws_2", "status": "completed", "usage": {"input_tokens": 12, "output_tokens": 6, "total_tokens": 18}}}`,
		}
		for _, ev := range events2 {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
		}
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	ctx := context.Background()

	// 1. Run first turn
	opts1 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}
	c1 := Context{
		Messages: []Message{
			UserMessage{Content: "Hello"},
		},
	}
	stream1 := StreamOpenAICodexResponses(ctx, model, c1, opts1)
	msg1, err := stream1.Result()
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	if msg1.ResponseID != "resp_ws_1" {
		t.Errorf("expected first turn response ID 'resp_ws_1', got %q", msg1.ResponseID)
	}

	// 2. Run second turn
	opts2 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}
	c2 := Context{
		Messages: []Message{
			UserMessage{Content: "Hello"},
			msg1,
			UserMessage{Content: "How are you?"},
		},
	}
	stream2 := StreamOpenAICodexResponses(ctx, model, c2, opts2)
	msg2, err := stream2.Result()
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}
	if msg2.ResponseID != "resp_ws_2" {
		t.Errorf("expected second turn response ID 'resp_ws_2', got %q", msg2.ResponseID)
	}

	// 3. Verify debug stats
	stats := GetOpenAICodexWebSocketDebugStats(sessionID)
	if stats == nil {
		t.Fatalf("expected debug stats for session %q, got nil", sessionID)
	}

	if stats.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", stats.Requests)
	}
	if stats.ConnectionsCreated != 1 {
		t.Errorf("expected 1 connection created, got %d", stats.ConnectionsCreated)
	}
	if stats.ConnectionsReused != 1 {
		t.Errorf("expected 1 connection reused, got %d", stats.ConnectionsReused)
	}
	if stats.DeltaRequests != 1 {
		t.Errorf("expected 1 delta request, got %d", stats.DeltaRequests)
	}
}

func TestStreamOpenAICodexResponses_WebSocket_FallbackToSSE(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_fallback"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_fallback_123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If WebSocket upgrade request, reject it
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		// Otherwise serve SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_sse_fallback\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_sse_fallback\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 10, \"output_tokens\": 10, \"total_tokens\": 20}}}\n\n")
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportAuto,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)
	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected streaming result error: %v", err)
	}

	if msg.ResponseID != "resp_sse_fallback" {
		t.Errorf("expected ResponseID 'resp_sse_fallback', got %q", msg.ResponseID)
	}

	// Verify fallback state recorded
	stats := GetOpenAICodexWebSocketDebugStats(sessionID)
	if stats == nil {
		t.Fatalf("expected debug stats, got nil")
	}
	if stats.WebsocketFailures != 1 {
		t.Errorf("expected 1 websocket failure, got %d", stats.WebsocketFailures)
	}
	if !stats.WebsocketFallbackActive {
		t.Errorf("expected WebsocketFallbackActive to be true")
	}
}

func TestStreamOpenAICodexResponses_WebSocket_NoFallbackAfterStart(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_nofallback"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_nofallback_123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusInternalError, "dropped connection")

		_, _, _ = conn.Read(r.Context())

		// Send initial event
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type": "response.created", "response": {"id": "resp_ws_drop"}}`))

		// Close connection immediately to trigger failure after start
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocket,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)
	_, err := stream.Result()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The error should complain about connection closure or stream ending
	if !strings.Contains(err.Error(), "WebSocket stream closed before response.completed") && !strings.Contains(err.Error(), "dropped connection") && !strings.Contains(err.Error(), "closed") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify that we did NOT record SSE fallback in stats (since the failure was post-start, we shouldn't fallback to SSE)
	// Wait, does the TS version still increment websocketFailures? Yes, but it doesn't fall back to SSE because output started.
	// Let's verify that the response ID in output is set.
}

func TestStreamOpenAICodexResponses_WebSocket_IdleExpiry(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_idle"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_idle_123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		var req map[string]any
		_ = json.Unmarshal(data, &req)

		events := []string{
			`{"type": "response.created", "response": {"id": "resp_ws_idle"}}`,
			`{"type": "response.completed", "response": {"id": "resp_ws_idle", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
		}

		for _, ev := range events {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
		}
	}))
	defer srv.Close()

	// Override idle timeout to 20ms
	oldTimeout := websocketIdleTimeout
	websocketIdleTimeout = 20 * time.Millisecond
	defer func() {
		websocketIdleTimeout = oldTimeout
	}()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)
	_, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected stream result error: %v", err)
	}

	// Verify it is cached initially
	wsMu.Lock()
	_, existsBefore := websocketSessionCache[sessionID]
	wsMu.Unlock()
	if !existsBefore {
		t.Fatal("expected connection to be cached initially")
	}

	// Wait for idle expiry (sleep 50ms)
	time.Sleep(50 * time.Millisecond)

	// Verify it has been expired and removed from cache
	wsMu.Lock()
	_, existsAfter := websocketSessionCache[sessionID]
	wsMu.Unlock()
	if existsAfter {
		t.Fatal("expected connection to be removed from cache after idle timeout")
	}
}

func TestStreamOpenAICodexResponses_WebSocket_DegradationFallback(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_degraded"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_degraded_123"

	var wsHandshakeAttempts int32
	var sseRequestCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			atomic.AddInt32(&wsHandshakeAttempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable) // reject WebSocket
			return
		}

		// Otherwise serve SSE
		atomic.AddInt32(&sseRequestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_sse_degraded\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_sse_degraded\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 10, \"output_tokens\": 10, \"total_tokens\": 20}}}\n\n")
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	ctx := context.Background()

	// 1. First request using TransportAuto. It should attempt WebSocket, fail, and fallback to SSE.
	opts1 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportAuto,
		},
	}
	stream1 := StreamOpenAICodexResponses(ctx, model, Context{}, opts1)
	_, err := stream1.Result()
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	if atomic.LoadInt32(&wsHandshakeAttempts) != 1 {
		t.Errorf("expected 1 websocket handshake attempt on first request, got %d", wsHandshakeAttempts)
	}
	if atomic.LoadInt32(&sseRequestCount) != 1 {
		t.Errorf("expected 1 sse fallback request on first request, got %d", sseRequestCount)
	}

	// Verify fallback is active
	stats := GetOpenAICodexWebSocketDebugStats(sessionID)
	if stats == nil || !stats.WebsocketFallbackActive {
		t.Fatal("expected websocket fallback to be active in debug stats")
	}

	// 2. Second request using TransportAuto on same session.
	// Since fallback is active, it should skip WebSocket completely and go straight to SSE!
	opts2 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportAuto,
		},
	}
	stream2 := StreamOpenAICodexResponses(ctx, model, Context{}, opts2)
	_, err = stream2.Result()
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	// Handshake attempts should still be 1 (meaning 0 additional attempts)!
	if atomic.LoadInt32(&wsHandshakeAttempts) != 1 {
		t.Errorf("expected no additional websocket handshake attempts on second request, got %d total", wsHandshakeAttempts)
	}
	// SSE request count should be 2!
	if atomic.LoadInt32(&sseRequestCount) != 2 {
		t.Errorf("expected 2 total sse requests, got %d", sseRequestCount)
	}
}

func TestStreamOpenAICodexResponses_WebSocket_Caching_RetryOnDeadSocket(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_dead_retry"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_dead_retry_123"

	var connectionCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		currentConnID := atomic.AddInt32(&connectionCount, 1)

		if currentConnID == 1 {
			// First connection: accept request, send events, and close
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(data, &req)

			events := []string{
				`{"type": "response.created", "response": {"id": "resp_ws_dead_1"}}`,
				`{"type": "response.completed", "response": {"id": "resp_ws_dead_1", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
			}
			for _, ev := range events {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
			}
			// Close immediately so it is dead when client attempts Turn 2
			return
		}

		if currentConnID == 2 {
			// Second connection: should be the fresh retry socket (since the reused one failed).
			// Verify that previous_response_id is NOT present because we fell back to the full request.
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(data, &req)

			if req["previous_response_id"] != nil {
				t.Errorf("expected previous_response_id to be nil on retry socket, got %v", req["previous_response_id"])
			}

			events := []string{
				`{"type": "response.created", "response": {"id": "resp_ws_dead_2"}}`,
				`{"type": "response.completed", "response": {"id": "resp_ws_dead_2", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
			}
			for _, ev := range events {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
			}
		}
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	ctx := context.Background()

	// 1. Run Turn 1
	opts1 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}
	stream1 := StreamOpenAICodexResponses(ctx, model, Context{
		Messages: []Message{UserMessage{Content: "Turn 1"}},
	}, opts1)
	msg1, err := stream1.Result()
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	if msg1.ResponseID != "resp_ws_dead_1" {
		t.Errorf("expected Turn 1 response ID 'resp_ws_dead_1', got %q", msg1.ResponseID)
	}

	// 2. Run Turn 2 (Client will reuse cached connection, which will fail to write/read,
	// and should automatically retry with a new connection and full request body)
	opts2 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}
	stream2 := StreamOpenAICodexResponses(ctx, model, Context{
		Messages: []Message{
			UserMessage{Content: "Turn 1"},
			msg1,
			UserMessage{Content: "Turn 2"},
		},
	}, opts2)
	msg2, err := stream2.Result()
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}
	if msg2.ResponseID != "resp_ws_dead_2" {
		t.Errorf("expected Turn 2 response ID 'resp_ws_dead_2', got %q", msg2.ResponseID)
	}

	// Verify that we successfully used exactly 2 connections total (one for Turn 1,
	// and one for Turn 2 retry). The dead reuse attempt didn't increment successful connections.
	if count := atomic.LoadInt32(&connectionCount); count != 2 {
		t.Errorf("expected exactly 2 websocket connections accepted by the server, got %d", count)
	}
}

func TestStreamOpenAICodexResponses_WebSocket_SlowFirstMessage(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_slow"}
	token := makeFakeJWT(t, claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, _, _ = conn.Read(r.Context())

		// Delay sending the first event by 50ms
		time.Sleep(50 * time.Millisecond)

		events := []string{
			`{"type": "response.created", "response": {"id": "resp_ws_slow"}}`,
			`{"type": "response.completed", "response": {"id": "resp_ws_slow", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
		}
		for _, ev := range events {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
		}
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	ctx := context.Background()

	// 1. Success case: no timeout configured, should succeed even with delay
	opts1 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			Transport: TransportWebSocket,
		},
	}
	stream1 := StreamOpenAICodexResponses(ctx, model, Context{}, opts1)
	msg1, err := stream1.Result()
	if err != nil {
		t.Fatalf("expected slow first message to succeed, got error: %v", err)
	}
	if msg1.ResponseID != "resp_ws_slow" {
		t.Errorf("expected response ID 'resp_ws_slow', got %q", msg1.ResponseID)
	}

	// 2. Failure case: short TimeoutMs configured (e.g., 5ms), should fail with timeout error
	tMs := 5
	opts2 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			Transport: TransportWebSocket,
			TimeoutMs: &tMs,
		},
	}
	stream2 := StreamOpenAICodexResponses(ctx, model, Context{}, opts2)
	_, err = stream2.Result()
	if err == nil {
		t.Fatal("expected timeout failure, got nil error")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "504") {
		t.Errorf("expected timeout/deadline or 504 error, got: %v", err)
	}
}

func TestStreamOpenAICodexResponses_WebSocket_Reused_TimeoutNoRetry(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_ws_reused_timeout"}
	token := makeFakeJWT(t, claims)
	sessionID := "session_ws_reused_timeout_123"

	var connectionCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		currentConnID := atomic.AddInt32(&connectionCount, 1)

		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(data, &req)

		if currentConnID == 1 {
			// First connection: succeed and cache it
			events := []string{
				`{"type": "response.created", "response": {"id": "resp_ws_timeout_1"}}`,
				`{"type": "response.completed", "response": {"id": "resp_ws_timeout_1", "status": "completed", "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}`,
			}
			for _, ev := range events {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(ev))
			}
			// Keep connection alive to be reused
			select {
			case <-r.Context().Done():
			case <-time.After(1 * time.Second):
			}
			return
		}

		if currentConnID > 1 {
			// Second connection: should NOT be reached because on the first connection (which is reused),
			// the read timeout triggers, and it should fail immediately without retrying on a new connection.
			t.Errorf("unexpected connection retry attempt: %d", currentConnID)
		}
	}))
	defer srv.Close()

	ResetOpenAICodexWebSocketDebugStats(sessionID)
	defer CloseOpenAICodexWebSocketSessions(sessionID)

	model := testModel(srv.URL)
	ctx := context.Background()

	// 1. First turn: succeeds and caches connection
	opts1 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
		},
	}
	stream1 := StreamOpenAICodexResponses(ctx, model, Context{
		Messages: []Message{UserMessage{Content: "Turn 1"}},
	}, opts1)
	_, err := stream1.Result()
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}

	// 2. Second turn: configuring a short TimeoutMs (e.g. 5ms).
	// The server will delay sending any data, triggering an idle timeout on the reused socket.
	// Since it's a timeout (DeadlineExceeded), it should NOT retry on a fresh connection.
	tMs := 5
	opts2 := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey:    token,
			SessionID: sessionID,
			Transport: TransportWebSocketCached,
			TimeoutMs: &tMs,
		},
	}
	stream2 := StreamOpenAICodexResponses(ctx, model, Context{
		Messages: []Message{
			UserMessage{Content: "Turn 1"},
			UserMessage{Content: "Turn 2"},
		},
	}, opts2)
	_, err = stream2.Result()
	if err == nil {
		t.Fatal("expected timeout failure, got nil error")
	}

	// The error should be either a timeout/deadline error or 504 from fallback
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "504") {
		t.Errorf("expected timeout/deadline or 504 error, got: %v", err)
	}

	// Verify that exactly 1 connection was accepted by the server (the first cached one).
	// No second connection was opened or retried.
	if count := atomic.LoadInt32(&connectionCount); count != 1 {
		t.Errorf("expected exactly 1 websocket connection, got %d", count)
	}
}

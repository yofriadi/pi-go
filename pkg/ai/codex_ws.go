package ai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// OpenAICodexWebSocketDebugStats tracks performance and operational metrics for Codex WebSockets.
type OpenAICodexWebSocketDebugStats struct {
	Requests                int     `json:"requests"`
	ConnectionsCreated      int     `json:"connectionsCreated"`
	ConnectionsReused       int     `json:"connectionsReused"`
	CachedContextRequests   int     `json:"cachedContextRequests"`
	StoreTrueRequests       int     `json:"storeTrueRequests"`
	FullContextRequests     int     `json:"fullContextRequests"`
	DeltaRequests           int     `json:"deltaRequests"`
	LastInputItems          int     `json:"lastInputItems"`
	LastDeltaInputItems     *int    `json:"lastDeltaInputItems,omitempty"`
	LastPreviousResponseID  *string `json:"lastPreviousResponseId,omitempty"`
	WebsocketFailures       int     `json:"websocketFailures"`
	SseFallbacks            int     `json:"sseFallbacks"`
	WebsocketFallbackActive bool    `json:"websocketFallbackActive"`
	LastWebSocketError      string  `json:"lastWebSocketError,omitempty"`
}

type cachedWebSocketContinuationState struct {
	lastRequestBody   map[string]any
	lastResponseID    string
	lastResponseItems []any
}

type cachedWebSocketConnection struct {
	mu           sync.Mutex
	conn         *websocket.Conn
	busy         bool
	idleTimer    *time.Timer
	continuation *cachedWebSocketContinuationState
}

var (
	websocketSessionCache        = make(map[string]*cachedWebSocketConnection)
	websocketDebugStats          = make(map[string]*OpenAICodexWebSocketDebugStats)
	websocketSseFallbackSessions = make(map[string]bool)
	wsMu                         sync.Mutex
	websocketIdleTimeout         = 5 * time.Minute
)

// GetOpenAICodexWebSocketDebugStats retrieves debug statistics for a given session.
func GetOpenAICodexWebSocketDebugStats(sessionID string) *OpenAICodexWebSocketDebugStats {
	wsMu.Lock()
	defer wsMu.Unlock()

	stats, ok := websocketDebugStats[sessionID]
	if !ok {
		return nil
	}
	cp := *stats
	return &cp
}

// ResetOpenAICodexWebSocketDebugStats clears debug stats and fallback flags.
// If sessionID is empty, it resets all sessions.
func ResetOpenAICodexWebSocketDebugStats(sessionID string) {
	wsMu.Lock()
	defer wsMu.Unlock()

	if sessionID == "" {
		websocketDebugStats = make(map[string]*OpenAICodexWebSocketDebugStats)
		websocketSseFallbackSessions = make(map[string]bool)
	} else {
		delete(websocketDebugStats, sessionID)
		delete(websocketSseFallbackSessions, sessionID)
	}
}

// CloseOpenAICodexWebSocketSessions closes active WebSocket connections.
// If sessionID is empty, it closes all cached connections.
func CloseOpenAICodexWebSocketSessions(sessionID string) {
	wsMu.Lock()
	var toClose []*cachedWebSocketConnection

	if sessionID == "" {
		for _, entry := range websocketSessionCache {
			toClose = append(toClose, entry)
		}
		websocketSessionCache = make(map[string]*cachedWebSocketConnection)
	} else {
		if entry, ok := websocketSessionCache[sessionID]; ok {
			toClose = append(toClose, entry)
			delete(websocketSessionCache, sessionID)
		}
	}
	wsMu.Unlock()

	for _, entry := range toClose {
		entry.mu.Lock()
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
			entry.idleTimer = nil
		}
		_ = entry.conn.Close(websocket.StatusNormalClosure, "debug_close")
		entry.mu.Unlock()
	}
}

func isWebSocketSseFallbackActive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	wsMu.Lock()
	defer wsMu.Unlock()
	return websocketSseFallbackSessions[sessionID]
}

func recordWebSocketSseFallback(sessionID string) {
	if sessionID == "" {
		return
	}
	wsMu.Lock()
	defer wsMu.Unlock()
	stats := getOrCreateWebSocketDebugStats(sessionID)
	stats.SseFallbacks++
	stats.WebsocketFallbackActive = websocketSseFallbackSessions[sessionID]
}

func recordWebSocketFailure(sessionID string, err error) {
	if sessionID == "" {
		return
	}
	wsMu.Lock()
	defer wsMu.Unlock()
	websocketSseFallbackSessions[sessionID] = true

	stats := getOrCreateWebSocketDebugStats(sessionID)
	stats.WebsocketFailures++
	stats.LastWebSocketError = err.Error()
	stats.WebsocketFallbackActive = true
}

func getOrCreateWebSocketDebugStats(sessionID string) *OpenAICodexWebSocketDebugStats {
	stats, ok := websocketDebugStats[sessionID]
	if !ok {
		stats = &OpenAICodexWebSocketDebugStats{
			Requests:           0,
			ConnectionsCreated: 0,
			ConnectionsReused:  0,
		}
		websocketDebugStats[sessionID] = stats
	}
	return stats
}

func createCodexRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func isWebSocketReusable(ctx context.Context, conn *websocket.Conn) bool {
	return conn != nil
}

func connectWebSocket(
	ctx context.Context,
	urlStr string,
	headers map[string]string,
	connectTimeout time.Duration,
) (*websocket.Conn, error) {
	dialOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{},
	}
	for k, v := range headers {
		dialOpts.HTTPHeader.Set(k, v)
	}

	dialCtx := ctx
	if connectTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}

	conn, _, err := websocket.Dial(dialCtx, urlStr, dialOpts)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

type acquireResult struct {
	conn      *websocket.Conn
	entry     *cachedWebSocketConnection
	reused    bool
	releaseFn func(keep bool)
}

func acquireWebSocket(
	ctx context.Context,
	urlStr string,
	headers map[string]string,
	sessionID string,
	connectTimeout time.Duration,
) (*acquireResult, error) {
	if sessionID == "" {
		conn, err := connectWebSocket(ctx, urlStr, headers, connectTimeout)
		if err != nil {
			return nil, err
		}
		return &acquireResult{
			conn:   conn,
			reused: false,
			releaseFn: func(keep bool) {
				_ = conn.Close(websocket.StatusNormalClosure, "done")
			},
		}, nil
	}

	wsMu.Lock()
	cached, ok := websocketSessionCache[sessionID]
	if ok {
		cached.mu.Lock()
		if cached.idleTimer != nil {
			cached.idleTimer.Stop()
			cached.idleTimer = nil
		}
		if !cached.busy && isWebSocketReusable(ctx, cached.conn) {
			cached.busy = true
			cached.mu.Unlock()
			wsMu.Unlock()
			return &acquireResult{
				conn:   cached.conn,
				entry:  cached,
				reused: true,
				releaseFn: func(keep bool) {
					releaseCachedConnection(sessionID, cached, keep)
				},
			}, nil
		}
		if cached.busy {
			cached.mu.Unlock()
			wsMu.Unlock()
			conn, err := connectWebSocket(ctx, urlStr, headers, connectTimeout)
			if err != nil {
				return nil, err
			}
			return &acquireResult{
				conn:   conn,
				reused: false,
				releaseFn: func(keep bool) {
					_ = conn.Close(websocket.StatusNormalClosure, "done")
				},
			}, nil
		}
		_ = cached.conn.Close(websocket.StatusNormalClosure, "cleanup")
		delete(websocketSessionCache, sessionID)
		cached.mu.Unlock()
	}
	wsMu.Unlock()

	conn, err := connectWebSocket(ctx, urlStr, headers, connectTimeout)
	if err != nil {
		return nil, err
	}

	entry := &cachedWebSocketConnection{
		conn: conn,
		busy: true,
	}

	wsMu.Lock()
	websocketSessionCache[sessionID] = entry
	wsMu.Unlock()

	return &acquireResult{
		conn:   conn,
		entry:  entry,
		reused: false,
		releaseFn: func(keep bool) {
			releaseCachedConnection(sessionID, entry, keep)
		},
	}, nil
}

func releaseCachedConnection(sessionID string, entry *cachedWebSocketConnection, keep bool) {
	wsMu.Lock()
	defer wsMu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if !keep {
		_ = entry.conn.Close(websocket.StatusNormalClosure, "done")
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
			entry.idleTimer = nil
		}
		if websocketSessionCache[sessionID] == entry {
			delete(websocketSessionCache, sessionID)
		}
		return
	}

	entry.busy = false
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
	}
	entry.idleTimer = time.AfterFunc(websocketIdleTimeout, func() {
		wsMu.Lock()
		entry.mu.Lock()
		if !entry.busy {
			_ = entry.conn.Close(websocket.StatusNormalClosure, "idle_timeout")
			if websocketSessionCache[sessionID] == entry {
				delete(websocketSessionCache, sessionID)
			}
		}
		entry.mu.Unlock()
		wsMu.Unlock()
	})
}

func requestBodyWithoutInput(body map[string]any) map[string]any {
	res := make(map[string]any)
	for k, v := range body {
		if k != "input" && k != "previous_response_id" {
			res[k] = v
		}
	}
	return res
}

func responseInputsEqual(a, b []any) bool {
	aBytes, err1 := json.Marshal(a)
	bBytes, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}

func requestBodiesMatchExceptInput(a, b map[string]any) bool {
	aClean := requestBodyWithoutInput(a)
	bClean := requestBodyWithoutInput(b)
	aBytes, err1 := json.Marshal(aClean)
	bBytes, err2 := json.Marshal(bClean)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}

func getCachedWebSocketInputDelta(body map[string]any, continuation *cachedWebSocketContinuationState) []any {
	if !requestBodiesMatchExceptInput(body, continuation.lastRequestBody) {
		return nil
	}

	currentInputRaw, ok := body["input"]
	if !ok {
		return nil
	}

	var currentInput []any
	if sliceOfMaps, ok := currentInputRaw.([]map[string]any); ok {
		currentInput = make([]any, len(sliceOfMaps))
		for i, v := range sliceOfMaps {
			currentInput[i] = v
		}
	} else if sliceOfAny, ok := currentInputRaw.([]any); ok {
		currentInput = sliceOfAny
	} else {
		return nil
	}

	var baseline []any
	lastInputRaw, _ := continuation.lastRequestBody["input"]
	if lastInput, ok := lastInputRaw.([]any); ok {
		baseline = append(baseline, lastInput...)
	} else if lastInputMaps, ok := lastInputRaw.([]map[string]any); ok {
		for _, v := range lastInputMaps {
			baseline = append(baseline, v)
		}
	}
	baseline = append(baseline, continuation.lastResponseItems...)

	if len(currentInput) < len(baseline) {
		return nil
	}

	prefix := currentInput[:len(baseline)]
	if !responseInputsEqual(prefix, baseline) {
		return nil
	}

	return currentInput[len(baseline):]
}

func buildCachedWebSocketRequestBody(entry *cachedWebSocketConnection, body map[string]any) map[string]any {
	entry.mu.Lock()
	continuation := entry.continuation
	entry.mu.Unlock()

	if continuation == nil {
		return body
	}

	delta := getCachedWebSocketInputDelta(body, continuation)
	if delta == nil || continuation.lastResponseID == "" {
		entry.mu.Lock()
		entry.continuation = nil
		entry.mu.Unlock()
		return body
	}

	newBody := make(map[string]any)
	for k, v := range body {
		newBody[k] = v
	}
	newBody["previous_response_id"] = continuation.lastResponseID
	newBody["input"] = delta
	return newBody
}

// StreamCodexWebSocket connects to OpenAI Codex WebSocket and streams responses.
func StreamCodexWebSocket(
	ctx context.Context,
	model Model,
	bodyMap map[string]any,
	opts *CodexResponsesOptions,
	output *AssistantMessage,
	stream *AssistantStream,
) (<-chan CodexStreamResult, *bool, error) {
	var token string
	var err error
	if opts != nil && opts.APIKey != "" {
		token = opts.APIKey
	} else {
		token, err = ResolveCodexToken(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve codex token: %w", err)
		}
	}
	if token == "" {
		return nil, nil, errors.New("empty oauth token")
	}

	if _, err := ExtractChatGPTAccountID(token); err != nil {
		return nil, nil, fmt.Errorf("failed to extract account ID from token: %w", err)
	}

	wsURL, err := ResolveCodexWebSocketUrl(model.BaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve websocket url: %w", err)
	}

	var sessionID string
	if opts != nil {
		sessionID = opts.SessionID
	}
	reqID := sessionID
	if reqID == "" {
		reqID = createCodexRequestID()
	}

	accountID, _ := ExtractChatGPTAccountID(token)
	baseHeaders := BuildCodexHeaders(token, accountID, "pi-go/0.1.0", false)

	headers := make(map[string]string)
	for k, v := range baseHeaders {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	if model.Headers != nil {
		for k, v := range model.Headers {
			headers[http.CanonicalHeaderKey(k)] = v
		}
	}
	if opts != nil && opts.Headers != nil {
		for k, v := range opts.Headers {
			headers[http.CanonicalHeaderKey(k)] = v
		}
	}

	delete(headers, http.CanonicalHeaderKey("Content-Type"))
	delete(headers, http.CanonicalHeaderKey("Accept"))
	delete(headers, http.CanonicalHeaderKey("openai-beta"))

	headers[http.CanonicalHeaderKey("OpenAI-Beta")] = "responses_websockets=2026-02-06"
	headers[http.CanonicalHeaderKey("x-client-request-id")] = reqID
	headers[http.CanonicalHeaderKey("session-id")] = reqID

	connectTimeout := 15 * time.Second
	if opts != nil && opts.WebsocketConnectTimeoutMs != nil {
		connectTimeout = time.Duration(*opts.WebsocketConnectTimeoutMs) * time.Millisecond
	}

	var acq *acquireResult
	var requestBody map[string]any
	var firstMsg []byte
	var firstMsgType websocket.MessageType
	idleTimeout := 0 * time.Second
	if opts != nil && opts.TimeoutMs != nil {
		idleTimeout = time.Duration(*opts.TimeoutMs) * time.Millisecond
	}

	useCachedContext := (opts != nil) && (opts.Transport == TransportWebSocketCached || opts.Transport == TransportAuto)
	for attempt := 1; attempt <= 2; attempt++ {
		var acqErr error
		acq, acqErr = acquireWebSocket(ctx, wsURL, headers, sessionID, connectTimeout)
		if acqErr != nil {
			return nil, nil, acqErr
		}

		reusedAttempt := acq.reused

		requestBody = bodyMap
		if useCachedContext && acq.entry != nil && reusedAttempt {
			requestBody = buildCachedWebSocketRequestBody(acq.entry, bodyMap)
			acq.entry.mu.Lock()
			acq.entry.continuation = nil
			acq.entry.mu.Unlock()
		}

		if sessionID != "" {
			wsMu.Lock()
			stats := getOrCreateWebSocketDebugStats(sessionID)
			stats.Requests++
			if acq.reused {
				stats.ConnectionsReused++
			} else {
				stats.ConnectionsCreated++
			}
			if useCachedContext && acq.reused {
				stats.CachedContextRequests++
			}
			if store, _ := requestBody["store"].(bool); store {
				stats.StoreTrueRequests++
			}
			if input, ok := requestBody["input"].([]any); ok {
				stats.LastInputItems = len(input)
			} else if input, ok := requestBody["input"].([]map[string]any); ok {
				stats.LastInputItems = len(input)
			}
			if prevID, ok := requestBody["previous_response_id"].(string); ok && prevID != "" {
				stats.DeltaRequests++
				if input, ok := requestBody["input"].([]any); ok {
					n := len(input)
					stats.LastDeltaInputItems = &n
				} else if input, ok := requestBody["input"].([]map[string]any); ok {
					n := len(input)
					stats.LastDeltaInputItems = &n
				}
				stats.LastPreviousResponseID = &prevID
			} else {
				stats.FullContextRequests++
				stats.LastDeltaInputItems = nil
				stats.LastPreviousResponseID = nil
			}
			wsMu.Unlock()
		}

		reqMsg := map[string]any{
			"type": "response.create",
		}
		for k, v := range requestBody {
			reqMsg[k] = v
		}
		reqBytes, marshalErr := json.Marshal(reqMsg)
		if marshalErr != nil {
			acq.releaseFn(false)
			return nil, nil, fmt.Errorf("failed to marshal websocket message: %w", marshalErr)
		}

		writeErr := acq.conn.Write(ctx, websocket.MessageText, reqBytes)
		if writeErr != nil {
			acq.releaseFn(false)
			if reusedAttempt && attempt == 1 {
				continue
			}
			return nil, nil, fmt.Errorf("failed to send websocket message: %w", writeErr)
		}

		readCtx := ctx
		var readCancel context.CancelFunc
		if idleTimeout > 0 {
			readCtx, readCancel = context.WithTimeout(ctx, idleTimeout)
		}
		firstMsgType, firstMsg, err = acq.conn.Read(readCtx)
		if readCancel != nil {
			readCancel()
		}

		if err != nil {
			acq.releaseFn(false)
			if reusedAttempt && attempt == 1 && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return nil, nil, fmt.Errorf("failed to read first message from websocket: %w", err)
		}

		break
	}

	eventChan := make(chan CodexStreamResult, 100)
	websocketStarted := new(bool)
	*websocketStarted = false

	go func() {
		keepConnection := true
		var sawCompletion bool
		defer func() {
			acq.releaseFn(keepConnection)
			close(eventChan)
		}()

		if firstMsgType == websocket.MessageText {
			var ev CodexResponseStreamEvent
			if err := json.Unmarshal(firstMsg, &ev); err != nil {
				keepConnection = false
				if acq.entry != nil {
					acq.entry.mu.Lock()
					acq.entry.continuation = nil
					acq.entry.mu.Unlock()
				}
				eventChan <- CodexStreamResult{Err: fmt.Errorf("invalid Codex WebSocket JSON: %w", err)}
				return
			}
			ev.Raw = firstMsg

			*websocketStarted = true
			stream.Push(AssistantMessageEvent{
				Type:    EventStart,
				Partial: output,
			})

			eventChan <- CodexStreamResult{Event: &ev}

			isTerminal := false
			if ev.Type == "response.done" || ev.Type == "response.completed" || ev.Type == "response.incomplete" {
				ev.Type = "response.completed"
				if ev.Response != nil {
					ev.Response.Status = normalizeCodexStatus(ev.Response.Status)
				}
				isTerminal = true
				sawCompletion = true
			} else if ev.Type == "response.failed" || ev.Type == "error" {
				isTerminal = true
				keepConnection = false
				if acq.entry != nil {
					acq.entry.mu.Lock()
					acq.entry.continuation = nil
					acq.entry.mu.Unlock()
				}
			}
			if isTerminal {
				return
			}
		}

		for {
			var messageType websocket.MessageType
			var data []byte
			var readErr error

			if idleTimeout > 0 {
				readCtx, cancel := context.WithTimeout(ctx, idleTimeout)
				messageType, data, readErr = acq.conn.Read(readCtx)
				cancel()
			} else {
				messageType, data, readErr = acq.conn.Read(ctx)
			}

			if readErr != nil {
				keepConnection = false
				if acq.entry != nil {
					acq.entry.mu.Lock()
					acq.entry.continuation = nil
					acq.entry.mu.Unlock()
				}
				if errors.Is(readErr, context.DeadlineExceeded) && idleTimeout > 0 {
					eventChan <- CodexStreamResult{Err: fmt.Errorf("WebSocket idle timeout after %v", idleTimeout)}
				} else if errors.Is(readErr, context.Canceled) {
					eventChan <- CodexStreamResult{Err: ctx.Err()}
				} else {
					var closeErr websocket.CloseError
					if errors.As(readErr, &closeErr) {
						eventChan <- CodexStreamResult{Err: fmt.Errorf("WebSocket closed %d %s", closeErr.Code, closeErr.Reason)}
					} else {
						eventChan <- CodexStreamResult{Err: readErr}
					}
				}
				return
			}

			if messageType != websocket.MessageText {
				continue
			}

			var ev CodexResponseStreamEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				keepConnection = false
				if acq.entry != nil {
					acq.entry.mu.Lock()
					acq.entry.continuation = nil
					acq.entry.mu.Unlock()
				}
				eventChan <- CodexStreamResult{Err: fmt.Errorf("invalid Codex WebSocket JSON: %w", err)}
				return
			}
			ev.Raw = data

			if !*websocketStarted {
				*websocketStarted = true
				stream.Push(AssistantMessageEvent{
					Type:    EventStart,
					Partial: output,
				})
			}

			isTerminal := false
			if ev.Type == "response.done" || ev.Type == "response.completed" || ev.Type == "response.incomplete" {
				ev.Type = "response.completed"
				if ev.Response != nil {
					ev.Response.Status = normalizeCodexStatus(ev.Response.Status)
				}
				isTerminal = true
				sawCompletion = true
			} else if ev.Type == "response.failed" || ev.Type == "error" {
				isTerminal = true
				keepConnection = false
				if acq.entry != nil {
					acq.entry.mu.Lock()
					acq.entry.continuation = nil
					acq.entry.mu.Unlock()
				}
			}

			eventChan <- CodexStreamResult{Event: &ev}

			if isTerminal {
				break
			}
		}

		if !sawCompletion {
			keepConnection = false
			if acq.entry != nil {
				acq.entry.mu.Lock()
				acq.entry.continuation = nil
				acq.entry.mu.Unlock()
			}
			eventChan <- CodexStreamResult{Err: errors.New("WebSocket stream closed before response.completed")}
			return
		}
	}()

	return eventChan, websocketStarted, nil
}

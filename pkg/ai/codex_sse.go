package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CodexResponseStreamEvent represents a single normalized event from the OpenAI Codex Responses SSE API.
type CodexResponseStreamEvent struct {
	Type      string                `json:"type"`
	Response  *CodexResponsePayload `json:"response,omitempty"`
	Item      *CodexItemPayload     `json:"item,omitempty"`
	Part      *CodexPartPayload     `json:"part,omitempty"`
	Delta     string                `json:"delta,omitempty"`
	Arguments string                `json:"arguments,omitempty"`
	Code      string                `json:"code,omitempty"`
	Message   string                `json:"message,omitempty"`
	Raw       []byte                `json:"-"`
}

type CodexResponsePayload struct {
	ID                string                  `json:"id,omitempty"`
	Status            string                  `json:"status,omitempty"`
	Usage             *CodexUsage             `json:"usage,omitempty"`
	ServiceTier       string                  `json:"service_tier,omitempty"`
	Error             *CodexErrorPayload      `json:"error,omitempty"`
	IncompleteDetails *CodexIncompleteDetails `json:"incomplete_details,omitempty"`
}

type CodexErrorPayload struct {
	Code     string `json:"code,omitempty"`
	Type     string `json:"type,omitempty"`
	Message  string `json:"message,omitempty"`
	PlanType string `json:"plan_type,omitempty"`
	ResetsAt int64  `json:"resets_at,omitempty"`
}

type CodexIncompleteDetails struct {
	Reason string `json:"reason,omitempty"`
}

type CodexUsage struct {
	InputTokens        int                      `json:"input_tokens"`
	OutputTokens       int                      `json:"output_tokens"`
	TotalTokens        int                      `json:"total_tokens"`
	InputTokensDetails *CodexInputTokensDetails `json:"input_tokens_details,omitempty"`
}

type CodexInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type CodexItemPayload struct {
	ID        string             `json:"id,omitempty"`
	Type      string             `json:"type,omitempty"` // reasoning, message, function_call
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Content   []CodexContentPart `json:"content,omitempty"`
	Summary   []CodexSummaryPart `json:"summary,omitempty"`
	Phase     string             `json:"phase,omitempty"`
	Status    string             `json:"status,omitempty"`
}

type CodexContentPart struct {
	Type    string `json:"type"` // output_text, refusal
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

type CodexSummaryPart struct {
	Text string `json:"text"`
}

type CodexPartPayload struct {
	Type    string `json:"type"` // output_text, refusal, etc.
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// CodexStreamResult contains either a normalized Codex response event or an error.
type CodexStreamResult struct {
	Event *CodexResponseStreamEvent
	Err   error
}

var (
	terminalErrorRx = regexp.MustCompile(`(?i)GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)
	retryableMsgRx  = regexp.MustCompile(`(?i)rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)
)

func isTerminalRateLimitError(errorText string) bool {
	return terminalErrorRx.MatchString(errorText)
}

func isRetryableError(status int, errorText string) bool {
	if status == 429 && isTerminalRateLimitError(errorText) {
		return false
	}
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}
	return retryableMsgRx.MatchString(errorText)
}

func getRetryAfterDelay(headers http.Header) (time.Duration, bool) {
	if msStr := headers.Get("retry-after-ms"); msStr != "" {
		if ms, err := strconv.ParseInt(msStr, 10, 64); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond, true
		}
	}
	if secStr := headers.Get("retry-after"); secStr != "" {
		if sec, err := strconv.ParseInt(secStr, 10, 64); err == nil && sec >= 0 {
			return time.Duration(sec) * time.Second, true
		}
		// Try parsing as HTTP Date
		if t, err := http.ParseTime(secStr); err == nil {
			delay := time.Until(t)
			if delay < 0 {
				return 0, true
			}
			return delay, true
		}
	}
	return 0, false
}

var timeNewTimer = func(d time.Duration) *time.Timer {
	return time.NewTimer(d)
}

func normalizeCodexStatus(status string) string {
	switch status {
	case "completed", "incomplete", "failed", "cancelled", "queued", "in_progress":
		return status
	default:
		return ""
	}
}

// processSSEData parses raw bytes and converts them into CodexResponseStreamEvent, normalized.
// Returns true if this is a terminal event that indicates stream completion/termination.
func processSSEData(dataBytes []byte, eventChan chan<- CodexStreamResult) bool {
	dataStr := string(bytes.TrimSpace(dataBytes))
	if dataStr == "" || dataStr == "[DONE]" {
		return false
	}

	var ev CodexResponseStreamEvent
	if err := json.Unmarshal([]byte(dataStr), &ev); err != nil {
		eventChan <- CodexStreamResult{Err: fmt.Errorf("invalid Codex SSE JSON: %w (payload: %s)", err, dataStr)}
		return true
	}
	ev.Raw = []byte(dataStr)

	isTerminal := false
	if ev.Type == "response.done" || ev.Type == "response.completed" || ev.Type == "response.incomplete" {
		ev.Type = "response.completed"
		if ev.Response != nil {
			ev.Response.Status = normalizeCodexStatus(ev.Response.Status)
		}
		isTerminal = true
	} else if ev.Type == "response.failed" || ev.Type == "error" {
		isTerminal = true
	}

	eventChan <- CodexStreamResult{Event: &ev}
	return isTerminal
}

// parseSSE scans line-by-line and extracts data: payloads.
func parseSSE(reader io.Reader, eventChan chan<- CodexStreamResult, ctx context.Context) {
	br := bufio.NewReader(reader)
	var dataBuffer bytes.Buffer

	for {
		select {
		case <-ctx.Done():
			eventChan <- CodexStreamResult{Err: ctx.Err()}
			return
		default:
		}

		lineBytes, err := br.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				eventChan <- CodexStreamResult{Err: ctx.Err()}
				return
			}
			if err == io.EOF {
				if dataBuffer.Len() > 0 {
					processSSEData(dataBuffer.Bytes(), eventChan)
				}
				return
			}
			eventChan <- CodexStreamResult{Err: err}
			return
		}

		line := string(bytes.TrimRight(lineBytes, "\r\n"))

		if line == "" {
			if dataBuffer.Len() > 0 {
				shouldStop := processSSEData(dataBuffer.Bytes(), eventChan)
				dataBuffer.Reset()
				if shouldStop {
					return
				}
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataVal := strings.TrimPrefix(line, "data:")
			dataVal = strings.TrimSpace(dataVal)
			if dataBuffer.Len() > 0 {
				dataBuffer.WriteByte('\n')
			}
			dataBuffer.WriteString(dataVal)
		}
	}
}

// StreamCodexSSE streams responses from the OpenAI Codex Responses API using SSE POST.
func StreamCodexSSE(ctx context.Context, model Model, bodyMap map[string]any, opts *CodexResponsesOptions) (<-chan CodexStreamResult, error) {
	// 1. Resolve token
	var token string
	var err error
	if opts != nil && opts.APIKey != "" {
		token = opts.APIKey
	} else {
		token, err = ResolveCodexToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve codex token: %w", err)
		}
	}
	if token == "" {
		return nil, errors.New("empty oauth token")
	}

	// 2. Validate token (extract account ID to verify token structure)
	if _, err := ExtractChatGPTAccountID(token); err != nil {
		return nil, fmt.Errorf("failed to extract account ID from token: %w", err)
	}

	// 3. Resolve URL
	requestURL := ResolveCodexUrl(model.BaseURL)

	// 4. Handle OnPayload callback
	var requestBody any = bodyMap
	if opts != nil && opts.OnPayload != nil {
		replaced, didReplace, err := opts.OnPayload(bodyMap, model)
		if err != nil {
			return nil, fmt.Errorf("OnPayload callback failed: %w", err)
		}
		if didReplace {
			requestBody = replaced
		}
	}

	// 5. Serialize request body
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// 6. Setup custom http Client with ResponseHeaderTimeout
	headerTimeout := 10 * time.Second
	if opts != nil && opts.TimeoutMs != nil {
		headerTimeout = time.Duration(*opts.TimeoutMs) * time.Millisecond
	}

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
	}
	client := &http.Client{
		Transport: tr,
	}

	maxRetries := 0
	if opts != nil && opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	maxRetryDelay := 60 * time.Second
	if opts != nil && opts.MaxRetryDelayMs != nil {
		maxRetryDelay = time.Duration(*opts.MaxRetryDelayMs) * time.Millisecond
	}

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			tr.CloseIdleConnections()
			return nil, ctx.Err()
		}

		currentToken := token
		if attempt > 0 && (opts == nil || opts.APIKey == "") {
			t, err := ResolveCodexToken(ctx)
			if err != nil {
				tr.CloseIdleConnections()
				return nil, fmt.Errorf("failed to refresh token on retry: %w", err)
			}
			if t == "" {
				tr.CloseIdleConnections()
				return nil, errors.New("empty oauth token resolved on retry")
			}
			currentToken = t
		}

		// Re-extract account ID just in case token changed/refreshed
		currentAccountID, err := ExtractChatGPTAccountID(currentToken)
		if err != nil {
			lastErr = fmt.Errorf("failed to extract account ID from token: %w", err)
			break
		}

		reqHeaders := BuildCodexHeaders(currentToken, currentAccountID, "pi-go/0.1.0", true)
		if model.Headers != nil {
			for k, v := range model.Headers {
				reqHeaders[k] = v
			}
		}
		if opts != nil && opts.Headers != nil {
			for k, v := range opts.Headers {
				reqHeaders[k] = v
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", requestURL, bytes.NewReader(bodyBytes))
		if err != nil {
			tr.CloseIdleConnections()
			return nil, fmt.Errorf("failed to create http request: %w", err)
		}

		for k, v := range reqHeaders {
			req.Header.Set(k, v)
		}

		if opts != nil && opts.OnRequest != nil {
			opts.OnRequest(req, bodyBytes)
		}

		resp, err = client.Do(req)
		if err == nil {
			if opts != nil && opts.OnResponse != nil {
				opts.OnResponse(resp)
			}

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			}

			// Read error body
			errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			errorText := string(errBytes)

			lastErr = fmt.Errorf("HTTP error %d: %s", resp.StatusCode, errorText)

			if attempt < maxRetries && isRetryableError(resp.StatusCode, errorText) {
				delay := 1000 * time.Millisecond * time.Duration(1<<attempt)
				if headerDelay, ok := getRetryAfterDelay(resp.Header); ok {
					delay = headerDelay
				}
				if maxRetryDelay > 0 && delay > maxRetryDelay {
					delay = maxRetryDelay
				}

				timer := timeNewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					tr.CloseIdleConnections()
					return nil, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			break
		} else {
			lastErr = err
			if attempt < maxRetries && !strings.Contains(err.Error(), "usage limit") {
				delay := 1000 * time.Millisecond * time.Duration(1<<attempt)
				if maxRetryDelay > 0 && delay > maxRetryDelay {
					delay = maxRetryDelay
				}
				timer := timeNewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					tr.CloseIdleConnections()
					return nil, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			break
		}
	}

	if resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tr.CloseIdleConnections()
		return nil, lastErr
	}

	eventChan := make(chan CodexStreamResult, 100)
	go func() {
		defer resp.Body.Close()
		defer tr.CloseIdleConnections()
		defer close(eventChan)
		parseSSE(resp.Body, eventChan, ctx)
	}()

	return eventChan, nil
}

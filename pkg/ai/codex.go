package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CodexResponsesOptions contains options specific to the OpenAI Codex Responses API.
type CodexResponsesOptions struct {
	StreamOptions
	ReasoningEffort  string
	ReasoningSummary string
	ServiceTier      string
	TextVerbosity    string
}

// StreamOpenAICodexResponses streams responses from the OpenAI Codex Responses API.
func StreamOpenAICodexResponses(ctx context.Context, model Model, c Context, opts *CodexResponsesOptions) *AssistantStream {
	if model.Provider != ProviderIDOpenAICodex || model.API != APIIDOpenAICodexResponses {
		return newErrorStream(fmt.Errorf("invalid model provider %q or API %q", model.Provider, model.API))
	}

	stream := NewAssistantStream(0)

	// Resolve token for error sanitization/redaction
	var token string
	if opts != nil && opts.APIKey != "" {
		token = opts.APIKey
	} else {
		// Best-effort resolve
		token, _ = ResolveCodexToken(ctx)
	}

	go func() {
		output := AssistantMessage{
			Content:  []AssistantContent{},
			API:      APIIDOpenAICodexResponses,
			Provider: model.Provider,
			Model:    model.ID,
			Usage: Usage{
				Input:       0,
				Output:      0,
				CacheRead:   0,
				CacheWrite:  0,
				TotalTokens: 0,
				Cost:        UsageCost{},
			},
			StopReason: StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}

		bodyMap, err := buildCodexRequestBody(model, c, opts)
		if err != nil {
			sanitizedErr := sanitizeError(err, token)
			output.StopReason = StopReasonError
			output.ErrorMessage = sanitizedErr.Error()
			output.Diagnostics = append(output.Diagnostics, AssistantMessageDiagnostic{
				Code:     "request_shaping_failure",
				Message:  sanitizedErr.Error(),
				Severity: "error",
			})
			stream.Error(sanitizedErr, &output)
			return
		}

		eventChan, err := StreamCodexSSE(ctx, model, bodyMap, opts)
		if err != nil {
			sanitizedErr := sanitizeError(parseSSEStartError(err), token)
			output.StopReason = StopReasonError
			output.ErrorMessage = sanitizedErr.Error()
			output.Diagnostics = append(output.Diagnostics, AssistantMessageDiagnostic{
				Code:     "provider_transport_failure",
				Message:  sanitizedErr.Error(),
				Severity: "error",
			})
			stream.Error(sanitizedErr, &output)
			return
		}

		// Initial start event
		stream.Push(AssistantMessageEvent{
			Type:    EventStart,
			Partial: &output,
		})

		var currentBlockThinking *ThinkingContent
		var currentBlockText *TextContent
		var currentBlockToolCall *ToolCall
		var currentBlockPartialJson string
		var terminalErr error

		for res := range eventChan {
			if res.Err != nil {
				if strings.Contains(res.Err.Error(), "invalid Codex SSE JSON") {
					terminalErr = errors.New("provider stream parse error")
				} else {
					terminalErr = sanitizeError(res.Err, token)
				}
				break
			}

			event := res.Event
			if event == nil {
				continue
			}

			switch event.Type {
			case "response.created":
				if event.Response != nil {
					output.ResponseID = event.Response.ID
				}

			case "response.output_item.added":
				if event.Item != nil {
					item := event.Item
					switch item.Type {
					case "reasoning":
						currentBlockThinking = &ThinkingContent{Thinking: ""}
						output.Content = append(output.Content, currentBlockThinking)
						idx := len(output.Content) - 1
						stream.Push(AssistantMessageEvent{
							Type:         EventThinkingStart,
							ContentIndex: &idx,
							Partial:      &output,
						})
					case "message":
						currentBlockText = &TextContent{Text: ""}
						output.Content = append(output.Content, currentBlockText)
						idx := len(output.Content) - 1
						stream.Push(AssistantMessageEvent{
							Type:         EventTextStart,
							ContentIndex: &idx,
							Partial:      &output,
						})
					case "function_call":
						id := fmt.Sprintf("%s|%s", item.CallID, item.ID)
						currentBlockToolCall = &ToolCall{
							ID:        id,
							Name:      item.Name,
							Arguments: make(map[string]any),
						}
						currentBlockPartialJson = item.Arguments
						output.Content = append(output.Content, currentBlockToolCall)
						idx := len(output.Content) - 1
						stream.Push(AssistantMessageEvent{
							Type:         EventToolCallStart,
							ContentIndex: &idx,
							Partial:      &output,
						})
					}
				}

			case "response.reasoning_summary_text.delta":
				if currentBlockThinking != nil {
					currentBlockThinking.Thinking += event.Delta
					idx := len(output.Content) - 1
					stream.Push(AssistantMessageEvent{
						Type:         EventThinkingDelta,
						ContentIndex: &idx,
						Delta:        event.Delta,
						Partial:      &output,
					})
				}

			case "response.reasoning_summary_part.done":
				if currentBlockThinking != nil {
					currentBlockThinking.Thinking += "\n\n"
					idx := len(output.Content) - 1
					stream.Push(AssistantMessageEvent{
						Type:         EventThinkingDelta,
						ContentIndex: &idx,
						Delta:        "\n\n",
						Partial:      &output,
					})
				}

			case "response.reasoning_text.delta":
				if currentBlockThinking != nil {
					currentBlockThinking.Thinking += event.Delta
					idx := len(output.Content) - 1
					stream.Push(AssistantMessageEvent{
						Type:         EventThinkingDelta,
						ContentIndex: &idx,
						Delta:        event.Delta,
						Partial:      &output,
					})
				}

			case "response.output_text.delta", "response.refusal.delta":
				if currentBlockText != nil {
					currentBlockText.Text += event.Delta
					idx := len(output.Content) - 1
					stream.Push(AssistantMessageEvent{
						Type:         EventTextDelta,
						ContentIndex: &idx,
						Delta:        event.Delta,
						Partial:      &output,
					})
				}

			case "response.function_call_arguments.delta":
				if currentBlockToolCall != nil {
					currentBlockPartialJson += event.Delta
					currentBlockToolCall.Arguments = parseStreamingJson(currentBlockPartialJson)
					idx := len(output.Content) - 1
					stream.Push(AssistantMessageEvent{
						Type:         EventToolCallDelta,
						ContentIndex: &idx,
						Delta:        event.Delta,
						Partial:      &output,
					})
				}

			case "response.function_call_arguments.done":
				if currentBlockToolCall != nil {
					prevPartialJson := currentBlockPartialJson
					currentBlockPartialJson = event.Arguments
					currentBlockToolCall.Arguments = parseStreamingJson(currentBlockPartialJson)
					if strings.HasPrefix(event.Arguments, prevPartialJson) {
						delta := event.Arguments[len(prevPartialJson):]
						if len(delta) > 0 {
							idx := len(output.Content) - 1
							stream.Push(AssistantMessageEvent{
								Type:         EventToolCallDelta,
								ContentIndex: &idx,
								Delta:        delta,
								Partial:      &output,
							})
						}
					}
				}

			case "response.output_item.done":
				if event.Item != nil {
					item := event.Item
					switch item.Type {
					case "reasoning":
						if currentBlockThinking != nil {
							var summaryParts []string
							for _, s := range item.Summary {
								if s.Text != "" {
									summaryParts = append(summaryParts, s.Text)
								}
							}
							summaryText := strings.Join(summaryParts, "\n\n")

							var contentParts []string
							for _, c := range item.Content {
								if c.Text != "" {
									contentParts = append(contentParts, c.Text)
								}
							}
							contentText := strings.Join(contentParts, "\n\n")

							finalThinking := summaryText
							if finalThinking == "" {
								finalThinking = contentText
							}
							if finalThinking == "" {
								finalThinking = currentBlockThinking.Thinking
							}
							currentBlockThinking.Thinking = finalThinking

							itemBytes, _ := json.Marshal(item)
							currentBlockThinking.ThinkingSignature = string(itemBytes)

							idx := len(output.Content) - 1
							stream.Push(AssistantMessageEvent{
								Type:         EventThinkingEnd,
								ContentIndex: &idx,
								Content:      currentBlockThinking.Thinking,
								Partial:      &output,
							})
							currentBlockThinking = nil
						}

					case "message":
						if currentBlockText != nil {
							var textParts []string
							for _, c := range item.Content {
								if c.Type == "output_text" {
									textParts = append(textParts, c.Text)
								} else if c.Type == "refusal" {
									textParts = append(textParts, c.Refusal)
								}
							}
							if len(textParts) > 0 {
								currentBlockText.Text = strings.Join(textParts, "")
							}
							currentBlockText.TextSignature = encodeTextSignatureV1(item.ID, item.Phase)

							idx := len(output.Content) - 1
							stream.Push(AssistantMessageEvent{
								Type:         EventTextEnd,
								ContentIndex: &idx,
								Content:      currentBlockText.Text,
								Partial:      &output,
							})
							currentBlockText = nil
						}

					case "function_call":
						args := make(map[string]any)
						if currentBlockToolCall != nil && currentBlockPartialJson != "" {
							args = parseStreamingJson(currentBlockPartialJson)
						} else {
							args = parseStreamingJson(item.Arguments)
						}

						var toolCall *ToolCall
						if currentBlockToolCall != nil {
							currentBlockToolCall.Arguments = args
							toolCall = currentBlockToolCall
						} else {
							id := fmt.Sprintf("%s|%s", item.CallID, item.ID)
							toolCall = &ToolCall{
								ID:        id,
								Name:      item.Name,
								Arguments: args,
							}
							output.Content = append(output.Content, toolCall)
						}

						idx := len(output.Content) - 1
						stream.Push(AssistantMessageEvent{
							Type:         EventToolCallEnd,
							ContentIndex: &idx,
							ToolCall:     toolCall,
							Partial:      &output,
						})
						currentBlockToolCall = nil
						currentBlockPartialJson = ""
					}
				}

			case "response.completed":
				if event.Response != nil {
					resp := event.Response
					if resp.ID != "" {
						output.ResponseID = resp.ID
					}
					if resp.Usage != nil {
						cachedTokens := 0
						if resp.Usage.InputTokensDetails != nil {
							cachedTokens = resp.Usage.InputTokensDetails.CachedTokens
						}
						output.Usage.Input = resp.Usage.InputTokens - cachedTokens
						output.Usage.Output = resp.Usage.OutputTokens
						output.Usage.CacheRead = cachedTokens
						output.Usage.CacheWrite = 0
						output.Usage.TotalTokens = resp.Usage.TotalTokens
					}

					serviceTier := ""
					if resp.ServiceTier != "" {
						serviceTier = resp.ServiceTier
					} else if opts != nil {
						serviceTier = opts.ServiceTier
					}

					multiplier := 1.0
					switch serviceTier {
					case "flex":
						multiplier = 0.5
					case "priority":
						if model.ID == "gpt-5.5" {
							multiplier = 2.5
						} else {
							multiplier = 2.0
						}
					}

					cost := CalculateCost(model, output.Usage)
					cost.Input *= multiplier
					cost.Output *= multiplier
					cost.CacheRead *= multiplier
					cost.CacheWrite *= multiplier
					cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
					output.Usage.Cost = cost

					output.StopReason = mapStopReason(resp.Status)
					if output.StopReason == StopReasonStop {
						hasToolCall := false
						for _, b := range output.Content {
							if _, ok := b.(*ToolCall); ok {
								hasToolCall = true
								break
							}
						}
						if hasToolCall {
							output.StopReason = StopReasonToolUse
						}
					}
				}

			case "error":
				errMsg := parseSafeError(0, event.Code, event.Message, "", 0)
				terminalErr = errors.New(errMsg)

			case "response.failed":
				code := ""
				msg := ""
				planType := ""
				var resetsAt int64 = 0

				if event.Response != nil {
					if event.Response.Error != nil {
						errPayload := event.Response.Error
						code = errPayload.Code
						if code == "" {
							code = errPayload.Type
						}
						msg = errPayload.Message
						planType = errPayload.PlanType
						resetsAt = errPayload.ResetsAt
					} else if event.Response.IncompleteDetails != nil {
						msg = fmt.Sprintf("incomplete: %s", event.Response.IncompleteDetails.Reason)
					}
				}
				errMsg := parseSafeError(0, code, msg, planType, resetsAt)
				terminalErr = errors.New(errMsg)
			}

			if terminalErr != nil {
				break
			}
		}

		if terminalErr != nil {
			sanitizedErr := sanitizeError(terminalErr, token)
			output.StopReason = StopReasonError
			if errors.Is(ctx.Err(), context.Canceled) {
				output.StopReason = StopReasonAborted
			}
			output.ErrorMessage = sanitizedErr.Error()
			output.Diagnostics = append(output.Diagnostics, AssistantMessageDiagnostic{
				Code:     "stream_processing_failure",
				Message:  sanitizedErr.Error(),
				Severity: "error",
			})
			stream.Error(sanitizedErr, &output)
			return
		}

		// Verify if context was cancelled during streaming
		if ctx.Err() != nil {
			sanitizedErr := sanitizeError(ctx.Err(), token)
			output.StopReason = StopReasonAborted
			output.ErrorMessage = sanitizedErr.Error()
			output.Diagnostics = append(output.Diagnostics, AssistantMessageDiagnostic{
				Code:     "context_cancelled",
				Message:  sanitizedErr.Error(),
				Severity: "error",
			})
			stream.Error(sanitizedErr, &output)
			return
		}

		stream.End(&output)
	}()

	return stream
}

// StreamSimpleOpenAICodexResponses streams responses using SimpleStreamOptions.
func StreamSimpleOpenAICodexResponses(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
	if model.Provider != ProviderIDOpenAICodex || model.API != APIIDOpenAICodexResponses {
		return newErrorStream(fmt.Errorf("invalid model provider %q or API %q", model.Provider, model.API))
	}

	codexOpts := mapSimpleOptionsToCodex(model, opts)
	return StreamOpenAICodexResponses(ctx, model, c, codexOpts)
}

// mapSimpleOptionsToCodex converts SimpleStreamOptions to CodexResponsesOptions.
func mapSimpleOptionsToCodex(model Model, opts *SimpleStreamOptions) *CodexResponsesOptions {
	var baseOpts StreamOptions
	if opts != nil {
		baseOpts = BuildBaseOptions(model, opts)
	}

	var reasoningEffort string
	if opts != nil && opts.Reasoning != "" {
		clamped := ClampThinkingLevel(model, opts.Reasoning)
		if clamped != ModelThinkingLevelOff {
			reasoningEffort = mapThinkingLevel(model, clamped)
		}
	}

	return &CodexResponsesOptions{
		StreamOptions:   baseOpts,
		ReasoningEffort: reasoningEffort,
	}
}

// RegisterBuiltinProviders registers the OpenAI Codex Responses provider.
func RegisterBuiltinProviders() error {
	return RegisterApiProvider(ApiProvider{
		API: APIIDOpenAICodexResponses,
		Stream: func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
			var codexOpts *CodexResponsesOptions
			if opts != nil {
				codexOpts = &CodexResponsesOptions{
					StreamOptions: *opts,
				}
			}
			return StreamOpenAICodexResponses(ctx, model, c, codexOpts)
		},
		StreamSimple: StreamSimpleOpenAICodexResponses,
	})
}

// mapThinkingLevel converts a ModelThinkingLevel to its Codex-compatible string representation.
func mapThinkingLevel(model Model, level ModelThinkingLevel) string {
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
			return *mapped
		}
	}
	switch level {
	case ModelThinkingLevelOff:
		return "none"
	case ModelThinkingLevelMinimal:
		return "minimal"
	case ModelThinkingLevelLow:
		return "low"
	case ModelThinkingLevelMedium:
		return "medium"
	case ModelThinkingLevelHigh:
		return "high"
	case ModelThinkingLevelXHigh:
		return "xhigh"
	default:
		return string(level)
	}
}

// ============================================================================
// Request Shaping & Helpers
// ============================================================================

// ExtractChatGPTAccountID extracts the ChatGPT account ID from the OAuth JWT access token.
// It checks root-level "chatgpt_account_id" and nested "https://api.openai.com/auth" -> "chatgpt_account_id".
func ExtractChatGPTAccountID(token string) (string, error) {
	if token == "" {
		return "", errors.New("empty token")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("invalid token segment count")
	}

	payloadSegment := parts[1]
	// Standardize base64 url encoding (add padding if needed)
	if l := len(payloadSegment) % 4; l > 0 {
		payloadSegment += strings.Repeat("=", 4-l)
	}

	data, err := base64.URLEncoding.DecodeString(payloadSegment)
	if err != nil {
		return "", errors.New("failed to decode token payload")
	}

	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", errors.New("failed to parse token JSON claims")
	}

	// 1. Check root-level "chatgpt_account_id"
	if val, ok := claims["chatgpt_account_id"]; ok {
		strVal, ok := val.(string)
		if !ok {
			return "", errors.New("chatgpt_account_id claim is not a string")
		}
		if trimmed := strings.TrimSpace(strVal); trimmed != "" {
			return trimmed, nil
		}
	}

	// 2. Check nested "https://api.openai.com/auth" -> "chatgpt_account_id"
	if authVal, ok := claims["https://api.openai.com/auth"]; ok {
		authMap, ok := authVal.(map[string]any)
		if !ok {
			return "", errors.New("auth claim is not an object")
		}
		if val, ok := authMap["chatgpt_account_id"]; ok {
			strVal, ok := val.(string)
			if !ok {
				return "", errors.New("nested chatgpt_account_id claim is not a string")
			}
			if trimmed := strings.TrimSpace(strVal); trimmed != "" {
				return trimmed, nil
			}
		}
	}

	return "", errors.New("chatgpt_account_id claim not found in token")
}

// ResolveCodexUrl normalizes the base URL for the Codex Responses API.
func ResolveCodexUrl(baseURL string) string {
	raw := baseURL
	if strings.TrimSpace(raw) == "" {
		raw = DefaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	if strings.HasSuffix(normalized, "/codex/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/codex") {
		return normalized + "/responses"
	}
	return normalized + "/codex/responses"
}

// ResolveCodexWebSocketUrl converts the base URL to a WebSocket URL.
func ResolveCodexWebSocketUrl(baseURL string) (string, error) {
	sseURL := ResolveCodexUrl(baseURL)
	u, err := url.Parse(sseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	return u.String(), nil
}

// BuildCodexHeaders constructs the required HTTP headers for a Codex request.
func BuildCodexHeaders(token, accountID, userAgent string, sse bool) map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"originator":    "pi",
		"User-Agent":    "pi-go/0.1.0",
		"Content-Type":  "application/json",
	}
	if userAgent != "" {
		headers["User-Agent"] = userAgent
	}
	if accountID != "" {
		headers["ChatGPT-Account-ID"] = accountID
	}
	if sse {
		headers["OpenAI-Beta"] = "responses=experimental"
		headers["Accept"] = "text/event-stream"
	}
	return headers
}

// DefaultCodexBaseURL is the default ChatGPT backend API URL.
const DefaultCodexBaseURL = "https://chatgpt.com/backend-api"

// clampOpenAIPromptCacheKey limits the key to 64 runes.
func clampOpenAIPromptCacheKey(key string) string {
	runes := []rune(key)
	if len(runes) <= 64 {
		return key
	}
	return string(runes[:64])
}

// sanitizeSurrogates removes unpaired surrogate characters from UTF-8 string (BMP surrogate range 0xD800-0xDFFF).
func sanitizeSurrogates(s string) string {
	var sb strings.Builder
	bytes := []byte(s)
	n := len(bytes)
	for i := 0; i < n; {
		if i+2 < n && bytes[i] == 0xed && bytes[i+1] >= 0xa0 && bytes[i+1] <= 0xbf && bytes[i+2] >= 0x80 && bytes[i+2] <= 0xbf {
			i += 3
			continue
		}
		sb.WriteByte(bytes[i])
		i++
	}
	return sb.String()
}

// shortHash computes a fast deterministic hash of the string, mimicking JS implementation.
func shortHash(str string) string {
	h1 := uint32(0xdeadbeef)
	h2 := uint32(0x41c6ce57)
	for _, r := range str {
		ch := uint32(r)
		h1 = (h1 ^ ch) * 2654435761
		h2 = (h2 ^ ch) * 1597334677
	}
	h1 = ((h1 ^ (h1 >> 16)) * 2246822507) ^ ((h2 ^ (h2 >> 13)) * 3266489909)
	h2 = ((h2 ^ (h2 >> 16)) * 2246822507) ^ ((h1 ^ (h1 >> 13)) * 3266489909)
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}

// TextSignature represents a parsed text signature.
type TextSignature struct {
	ID    string
	Phase string
}

func parseTextSignature(signature string) *TextSignature {
	if signature == "" {
		return nil
	}
	if strings.HasPrefix(signature, "{") {
		var parsed struct {
			V     int    `json:"v"`
			ID    string `json:"id"`
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(signature), &parsed); err == nil {
			if parsed.V == 1 && parsed.ID != "" {
				res := &TextSignature{ID: parsed.ID}
				if parsed.Phase == "commentary" || parsed.Phase == "final_answer" {
					res.Phase = parsed.Phase
				}
				return res
			}
		}
	}
	return &TextSignature{ID: signature}
}

func normalizeIdPart(part string) string {
	var sb strings.Builder
	for _, r := range part {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()

	runes := []rune(s)
	if len(runes) > 64 {
		s = string(runes[:64])
	}

	return strings.TrimRight(s, "_")
}

func buildForeignResponsesItemId(itemId string) string {
	normalized := "fc_" + shortHash(itemId)
	runes := []rune(normalized)
	if len(runes) > 64 {
		return string(runes[:64])
	}
	return normalized
}

func makeNormalizeToolCallId(model Model, allowedToolCallProviders map[string]bool) func(id string, targetModel Model, source *AssistantMessage) string {
	return func(id string, targetModel Model, source *AssistantMessage) string {
		if !allowedToolCallProviders[string(model.Provider)] {
			return normalizeIdPart(id)
		}
		if !strings.Contains(id, "|") {
			return normalizeIdPart(id)
		}
		parts := strings.SplitN(id, "|", 2)
		callId := parts[0]
		itemId := parts[1]

		normalizedCallId := normalizeIdPart(callId)
		isForeignToolCall := source.Provider != model.Provider || source.API != model.API
		var normalizedItemId string
		if isForeignToolCall {
			normalizedItemId = buildForeignResponsesItemId(itemId)
		} else {
			normalizedItemId = normalizeIdPart(itemId)
		}

		if !strings.HasPrefix(normalizedItemId, "fc_") {
			normalizedItemId = normalizeIdPart("fc_" + normalizedItemId)
		}
		return normalizedCallId + "|" + normalizedItemId
	}
}

func downgradeUnsupportedImages(messages []Message, model Model) []Message {
	hasImageSupport := false
	for _, k := range model.Input {
		if k == "image" {
			hasImageSupport = true
			break
		}
	}
	if hasImageSupport {
		return messages
	}

	result := make([]Message, len(messages))
	for i, msg := range messages {
		if userMsg, ok := msg.(UserMessage); ok {
			if contentList, ok := userMsg.Content.([]UserContent); ok {
				newContent := replaceImagesWithPlaceholder(contentList, "(image omitted: model does not support images)")
				newUserMsg := userMsg
				newUserMsg.Content = newContent
				result[i] = newUserMsg
				continue
			} else if anyList, ok := userMsg.Content.([]any); ok {
				newContent := replaceImagesWithPlaceholderAny(anyList, "(image omitted: model does not support images)")
				newUserMsg := userMsg
				newUserMsg.Content = newContent
				result[i] = newUserMsg
				continue
			}
		}
		result[i] = msg
	}
	return result
}

func replaceImagesWithPlaceholderAny(content []any, placeholder string) []any {
	var result []any
	previousWasPlaceholder := false

	for _, rawBlock := range content {
		if _, isImg := rawBlock.(ImageContent); isImg {
			if !previousWasPlaceholder {
				result = append(result, TextContent{Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}

		if mBlock, ok := rawBlock.(map[string]any); ok {
			typ, _ := mBlock["type"].(string)
			if typ == "image" {
				if !previousWasPlaceholder {
					result = append(result, map[string]any{
						"type": "text",
						"text": placeholder,
					})
				}
				previousWasPlaceholder = true
				continue
			}
		}

		if textBlock, isText := rawBlock.(TextContent); isText {
			result = append(result, textBlock)
			previousWasPlaceholder = textBlock.Text == placeholder
			continue
		}

		if mBlock, ok := rawBlock.(map[string]any); ok {
			typ, _ := mBlock["type"].(string)
			if typ == "text" {
				txt, _ := mBlock["text"].(string)
				result = append(result, mBlock)
				previousWasPlaceholder = txt == placeholder
				continue
			}
		}

		result = append(result, rawBlock)
		previousWasPlaceholder = false
	}

	return result
}

func replaceImagesWithPlaceholder(content []UserContent, placeholder string) []UserContent {
	var result []UserContent
	previousWasPlaceholder := false

	for _, block := range content {
		if _, isImg := block.(ImageContent); isImg {
			if !previousWasPlaceholder {
				result = append(result, TextContent{Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}

		if textBlock, isText := block.(TextContent); isText {
			result = append(result, textBlock)
			previousWasPlaceholder = textBlock.Text == placeholder
		} else {
			result = append(result, block)
			previousWasPlaceholder = false
		}
	}

	return result
}

func transformMessagesFirstPass(messages []Message, model Model, normalizeToolCallID func(id string, targetModel Model, source *AssistantMessage) string, toolCallIDMap map[string]string) []Message {
	transformed := make([]Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case UserMessage:
			transformed[i] = m

		case ToolResultMessage:
			if normalizedID, ok := toolCallIDMap[m.ToolCallID]; ok && normalizedID != m.ToolCallID {
				newMsg := m
				newMsg.ToolCallID = normalizedID
				transformed[i] = newMsg
			} else {
				transformed[i] = m
			}

		case AssistantMessage:
			isSameModel := m.Provider == model.Provider && m.API == model.API && m.Model == model.ID

			var transformedContent []AssistantContent
			for _, block := range m.Content {
				switch b := block.(type) {
				case ThinkingContent:
					if b.Redacted {
						if isSameModel {
							transformedContent = append(transformedContent, b)
						}
						continue
					}
					if isSameModel && b.ThinkingSignature != "" {
						transformedContent = append(transformedContent, b)
						continue
					}
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if isSameModel {
						transformedContent = append(transformedContent, b)
					} else {
						transformedContent = append(transformedContent, TextContent{Text: b.Thinking})
					}

				case TextContent:
					if isSameModel {
						transformedContent = append(transformedContent, b)
					} else {
						transformedContent = append(transformedContent, TextContent{Text: b.Text})
					}

				case ToolCall:
					normalizedToolCall := b
					if !isSameModel && b.ThoughtSignature != "" {
						normalizedToolCall.ThoughtSignature = ""
					}
					if !isSameModel && normalizeToolCallID != nil {
						normalizedID := normalizeToolCallID(b.ID, model, &m)
						if normalizedID != b.ID {
							toolCallIDMap[b.ID] = normalizedID
							normalizedToolCall.ID = normalizedID
						}
					}
					transformedContent = append(transformedContent, normalizedToolCall)

				default:
					transformedContent = append(transformedContent, block)
				}
			}

			newMsg := m
			newMsg.Content = transformedContent
			transformed[i] = newMsg

		default:
			transformed[i] = msg
		}
	}
	return transformed
}

func transformMessagesSecondPass(transformed []Message) []Message {
	var result []Message
	var pendingToolCalls []ToolCall
	existingToolResultIDs := make(map[string]bool)

	insertSyntheticToolResults := func() {
		if len(pendingToolCalls) > 0 {
			for _, tc := range pendingToolCalls {
				if !existingToolResultIDs[tc.ID] {
					result = append(result, ToolResultMessage{
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
						Content:    []ToolResultContent{TextContent{Text: "No result provided"}},
						IsError:    true,
						Timestamp:  time.Now().UnixNano() / int64(time.Millisecond),
					})
				}
			}
			pendingToolCalls = nil
			existingToolResultIDs = make(map[string]bool)
		}
	}

	for _, msg := range transformed {
		switch m := msg.(type) {
		case AssistantMessage:
			insertSyntheticToolResults()

			if m.StopReason == StopReasonError || m.StopReason == StopReasonAborted {
				continue
			}

			var toolCalls []ToolCall
			for _, b := range m.Content {
				if tc, ok := b.(ToolCall); ok {
					toolCalls = append(toolCalls, tc)
				}
			}
			if len(toolCalls) > 0 {
				pendingToolCalls = toolCalls
				existingToolResultIDs = make(map[string]bool)
			}

			result = append(result, m)

		case ToolResultMessage:
			existingToolResultIDs[m.ToolCallID] = true
			result = append(result, m)

		case UserMessage:
			insertSyntheticToolResults()
			result = append(result, m)

		default:
			result = append(result, msg)
		}
	}

	insertSyntheticToolResults()

	return result
}

func transformMessages(messages []Message, model Model, normalizeToolCallID func(id string, targetModel Model, source *AssistantMessage) string) []Message {
	imageAwareMessages := downgradeUnsupportedImages(messages, model)
	toolCallIDMap := make(map[string]string)
	transformed := transformMessagesFirstPass(imageAwareMessages, model, normalizeToolCallID, toolCallIDMap)
	return transformMessagesSecondPass(transformed)
}

func convertResponsesMessages(model Model, transformed []Message) ([]map[string]any, error) {
	var messages []map[string]any

	msgIndex := 0
	for _, msg := range transformed {
		switch m := msg.(type) {
		case UserMessage:
			if strVal, ok := m.Content.(string); ok {
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": sanitizeSurrogates(strVal),
						},
					},
				})
			} else if contentList, ok := m.Content.([]UserContent); ok {
				var content []map[string]any
				for _, block := range contentList {
					switch b := block.(type) {
					case TextContent:
						content = append(content, map[string]any{
							"type": "input_text",
							"text": sanitizeSurrogates(b.Text),
						})
					case ImageContent:
						content = append(content, map[string]any{
							"type":      "input_image",
							"detail":    "auto",
							"image_url": fmt.Sprintf("data:%s;base64,%s", b.MimeType, b.Data),
						})
					}
				}
				if len(content) > 0 {
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": content,
					})
				}
			} else if anyList, ok := m.Content.([]any); ok {
				var content []map[string]any
				for _, rawBlock := range anyList {
					if block, ok := rawBlock.(UserContent); ok {
						switch b := block.(type) {
						case TextContent:
							content = append(content, map[string]any{
								"type": "input_text",
								"text": sanitizeSurrogates(b.Text),
							})
						case ImageContent:
							content = append(content, map[string]any{
								"type":      "input_image",
								"detail":    "auto",
								"image_url": fmt.Sprintf("data:%s;base64,%s", b.MimeType, b.Data),
							})
						}
					} else if mBlock, ok := rawBlock.(map[string]any); ok {
						typ, _ := mBlock["type"].(string)
						if typ == "text" {
							txt, _ := mBlock["text"].(string)
							content = append(content, map[string]any{
								"type": "input_text",
								"text": sanitizeSurrogates(txt),
							})
						} else if typ == "image" {
							mime, _ := mBlock["mimeType"].(string)
							data, _ := mBlock["data"].(string)
							content = append(content, map[string]any{
								"type":      "input_image",
								"detail":    "auto",
								"image_url": fmt.Sprintf("data:%s;base64,%s", mime, data),
							})
						}
					}
				}
				if len(content) > 0 {
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": content,
					})
				}
			}

		case AssistantMessage:
			var output []map[string]any
			isDifferentModel := m.Model != model.ID || m.Provider != model.Provider || m.API != model.API

			processBlock := func(block any) error {
				switch b := block.(type) {
				case ThinkingContent:
					if b.ThinkingSignature != "" {
						var reasoningItem map[string]any
						if err := json.Unmarshal([]byte(b.ThinkingSignature), &reasoningItem); err == nil {
							output = append(output, reasoningItem)
						} else {
							return fmt.Errorf("failed to parse thinkingSignature JSON: %w", err)
						}
					} else if strings.TrimSpace(b.Thinking) != "" {
						item := map[string]any{
							"type": "message",
							"role": "assistant",
							"content": []map[string]any{
								{
									"type":        "output_text",
									"text":        sanitizeSurrogates(b.Thinking),
									"annotations": []any{},
								},
							},
							"status": "completed",
							"id":     fmt.Sprintf("msg_%d", msgIndex),
						}
						output = append(output, item)
						msgIndex++
					}

				case TextContent:
					parsedSignature := parseTextSignature(b.TextSignature)
					msgId := ""
					if parsedSignature != nil {
						msgId = parsedSignature.ID
					}
					if msgId == "" {
						msgId = fmt.Sprintf("msg_%d", msgIndex)
					} else {
						runes := []rune(msgId)
						if len(runes) > 64 {
							msgId = fmt.Sprintf("msg_%s", shortHash(msgId))
						}
					}

					item := map[string]any{
						"type": "message",
						"role": "assistant",
						"content": []map[string]any{
							{
								"type":        "output_text",
								"text":        sanitizeSurrogates(b.Text),
								"annotations": []any{},
							},
						},
						"status": "completed",
						"id":     msgId,
					}
					if parsedSignature != nil && parsedSignature.Phase != "" {
						item["phase"] = parsedSignature.Phase
					}
					output = append(output, item)
					msgIndex++

				case ToolCall:
					parts := strings.Split(b.ID, "|")
					callId := parts[0]
					var itemId *string
					if len(parts) > 1 {
						val := parts[1]
						itemId = &val
					}

					if isDifferentModel && itemId != nil && strings.HasPrefix(*itemId, "fc_") {
						itemId = nil
					}

					var argumentsJSON string
					if b.Arguments != nil {
						bytes, err := json.Marshal(b.Arguments)
						if err != nil {
							return fmt.Errorf("failed to marshal tool call arguments: %w", err)
						}
						argumentsJSON = string(bytes)
					} else {
						argumentsJSON = "{}"
					}

					item := map[string]any{
						"type":      "function_call",
						"call_id":   callId,
						"name":      b.Name,
						"arguments": argumentsJSON,
					}
					if itemId != nil {
						item["id"] = *itemId
					}
					output = append(output, item)
				}
				return nil
			}

			for _, block := range m.Content {
				if err := processBlock(block); err != nil {
					return nil, err
				}
			}
			if len(output) > 0 {
				messages = append(messages, output...)
			}

		case ToolResultMessage:
			var textResultParts []string
			var hasImages bool
			var imageBlocks []ImageContent

			for _, c := range m.Content {
				switch b := c.(type) {
				case TextContent:
					textResultParts = append(textResultParts, b.Text)
				case ImageContent:
					hasImages = true
					imageBlocks = append(imageBlocks, b)
				}
			}

			textResult := strings.Join(textResultParts, "\n")
			hasText := len(textResult) > 0
			callId := strings.Split(m.ToolCallID, "|")[0]

			hasImageSupport := false
			for _, k := range model.Input {
				if k == "image" {
					hasImageSupport = true
					break
				}
			}

			var output any
			if hasImages && hasImageSupport {
				var contentParts []map[string]any
				if hasText {
					contentParts = append(contentParts, map[string]any{
						"type": "input_text",
						"text": sanitizeSurrogates(textResult),
					})
				}
				for _, img := range imageBlocks {
					contentParts = append(contentParts, map[string]any{
						"type":      "input_image",
						"detail":    "auto",
						"image_url": fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data),
					})
				}
				output = contentParts
			} else {
				val := ""
				if hasText {
					val = textResult
				} else if hasImages {
					val = "(image omitted: model does not support images)"
				}
				output = sanitizeSurrogates(val)
			}

			messages = append(messages, map[string]any{
				"type":    "function_call_output",
				"call_id": callId,
				"output":  output,
			})
		}
	}

	return messages, nil
}

func convertResponsesTools(tools []ToolDefinition) []map[string]any {
	converted := make([]map[string]any, len(tools))
	for i, tool := range tools {
		converted[i] = map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      nil,
		}
	}
	return converted
}

// buildCodexRequestBody constructs the request body for the OpenAI Codex Responses API.
func buildCodexRequestBody(model Model, context Context, opts *CodexResponsesOptions) (map[string]any, error) {
	allowedToolCallProviders := map[string]bool{
		"openai-codex": true,
	}
	normalizeToolCallID := makeNormalizeToolCallId(model, allowedToolCallProviders)
	transformed := transformMessages(context.Messages, model, normalizeToolCallID)

	convertedMessages, err := convertResponsesMessages(model, transformed)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"model":               model.ID,
		"store":               false,
		"stream":              true,
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}

	instructions := "You are a helpful assistant."
	if context.SystemPrompt != "" {
		instructions = context.SystemPrompt
	}
	body["instructions"] = sanitizeSurrogates(instructions)
	body["input"] = convertedMessages

	textVerbosity := "low"
	if opts != nil && opts.TextVerbosity != "" {
		textVerbosity = opts.TextVerbosity
	}
	body["text"] = map[string]any{
		"verbosity": textVerbosity,
	}

	if opts != nil && opts.ServiceTier != "" {
		body["service_tier"] = opts.ServiceTier
	}

	if opts != nil && opts.SessionID != "" {
		body["prompt_cache_key"] = clampOpenAIPromptCacheKey(opts.SessionID)
	}

	if len(context.Tools) > 0 {
		body["tools"] = convertResponsesTools(context.Tools)
	}

	if opts != nil && opts.ReasoningEffort != "" {
		effort := opts.ReasoningEffort
		level := ModelThinkingLevel(opts.ReasoningEffort)
		if model.ThinkingLevelMap != nil {
			if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
				effort = *mapped
			} else if opts.ReasoningEffort == "none" {
				if mappedOff, ok := model.ThinkingLevelMap[ModelThinkingLevelOff]; ok && mappedOff != nil {
					effort = *mappedOff
				} else {
					effort = "none"
				}
			}
		}

		reasoningSummary := "auto"
		if opts.ReasoningSummary != "" {
			reasoningSummary = opts.ReasoningSummary
		}
		body["reasoning"] = map[string]any{
			"effort":  effort,
			"summary": reasoningSummary,
		}
	}

	return body, nil
}

func mapStopReason(status string) StopReason {
	switch status {
	case "completed":
		return StopReasonStop
	case "incomplete":
		return StopReasonLength
	case "failed", "cancelled":
		return StopReasonError
	case "in_progress", "queued":
		return StopReasonStop
	default:
		return StopReasonStop
	}
}

func encodeTextSignatureV1(id string, phase string) string {
	type TextSig struct {
		V     int    `json:"v"`
		ID    string `json:"id"`
		Phase string `json:"phase,omitempty"`
	}
	payload := TextSig{
		V:  1,
		ID: id,
	}
	if phase != "" {
		payload.Phase = phase
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes)
}

func parseSafeError(status int, code, msg, planType string, resetsAt int64) string {
	codeLower := strings.ToLower(code)
	isUsageLimit := status == 429 ||
		strings.Contains(codeLower, "usage_limit_reached") ||
		strings.Contains(codeLower, "usage_not_included") ||
		strings.Contains(codeLower, "rate_limit_exceeded") ||
		(code != "" && terminalErrorRx.MatchString(code)) ||
		(msg != "" && terminalErrorRx.MatchString(msg))

	if isUsageLimit {
		plan := ""
		if planType != "" {
			plan = " (" + strings.ToLower(planType) + " plan)"
		}
		when := ""
		if resetsAt > 0 {
			mins := (resetsAt*1000 - time.Now().UnixMilli()) / 60000
			if mins < 0 {
				mins = 0
			}
			when = fmt.Sprintf(" Try again in ~%d min.", mins)
		}
		return fmt.Sprintf("You have hit your ChatGPT usage limit%s.%s", plan, when)
	}

	// For non-friendly mapped provider errors, return a stable safe message.
	if code != "" {
		return fmt.Sprintf("provider error: %s", code)
	}
	if status > 0 {
		return fmt.Sprintf("HTTP error %d", status)
	}
	return "provider error"
}

func parseSSEStartError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "HTTP error ") {
		parts := strings.SplitN(strings.TrimPrefix(msg, "HTTP error "), ": ", 2)
		if len(parts) == 2 {
			statusStr := parts[0]
			bodyText := parts[1]
			var status int
			if _, scanErr := fmt.Sscanf(statusStr, "%d", &status); scanErr == nil {
				// 1. Try to parse as JSON first
				type ErrorDetails struct {
					Code     string `json:"code"`
					Type     string `json:"type"`
					Message  string `json:"message"`
					PlanType string `json:"plan_type"`
					ResetsAt int64  `json:"resets_at"`
				}
				type ErrorWrapper struct {
					Error *ErrorDetails `json:"error"`
				}
				var wrapper ErrorWrapper
				if jsonErr := json.Unmarshal([]byte(bodyText), &wrapper); jsonErr == nil && wrapper.Error != nil {
					e := wrapper.Error
					errCode := e.Code
					if errCode == "" {
						errCode = e.Type
					}
					friendly := parseSafeError(status, errCode, e.Message, e.PlanType, e.ResetsAt)
					return errors.New(friendly)
				}

				// 2. Not JSON, or unmarshal failed. Apply friendly parsing to raw bodyText.
				friendly := parseSafeError(status, "", bodyText, "", 0)
				return errors.New(friendly)
			}
		}
	}
	return err
}

func sanitizeError(err error, token string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	msg := err.Error()
	if token != "" && strings.Contains(msg, token) {
		msg = strings.ReplaceAll(msg, token, "[REDACTED]")
	}
	return errors.New(msg)
}

func parseStreamingJson(partialJson string) map[string]any {
	trimmed := strings.TrimSpace(partialJson)
	if trimmed == "" {
		return make(map[string]any)
	}

	tryUnmarshal := func(s string) (map[string]any, bool) {
		var res map[string]any
		if err := json.Unmarshal([]byte(s), &res); err == nil {
			if res == nil {
				return make(map[string]any), true
			}
			return res, true
		}
		return nil, false
	}

	if res, ok := tryUnmarshal(trimmed); ok {
		return res
	}

	type jsonStackNode struct {
		val    rune
		parent *jsonStackNode
	}

	runes := []rune(trimmed)
	inStringAt := make([]bool, len(runes))
	escapedAt := make([]bool, len(runes))
	stackAt := make([]*jsonStackNode, len(runes))

	inString := false
	escaped := false
	var currentStack *jsonStackNode

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inString {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				inString = false
			}
		} else {
			if r == '"' {
				inString = true
			} else if r == '{' {
				currentStack = &jsonStackNode{val: '{', parent: currentStack}
			} else if r == '[' {
				currentStack = &jsonStackNode{val: '[', parent: currentStack}
			} else if r == '}' {
				if currentStack != nil && currentStack.val == '{' {
					currentStack = currentStack.parent
				}
			} else if r == ']' {
				if currentStack != nil && currentStack.val == '[' {
					currentStack = currentStack.parent
				}
			}
		}
		inStringAt[i] = inString
		escapedAt[i] = escaped
		stackAt[i] = currentStack
	}

	buildCandidate := func(base string) string {
		candidate := base
		if inString {
			if escaped {
				if len(candidate) > 0 {
					candidate = candidate[:len(candidate)-1]
				}
			}
			candidate += `"`
		}
		curr := currentStack
		for curr != nil {
			if curr.val == '{' {
				candidate += "}"
			} else if curr.val == '[' {
				candidate += "]"
			}
			curr = curr.parent
		}
		return candidate
	}

	candidate := buildCandidate(trimmed)
	if res, ok := tryUnmarshal(candidate); ok {
		return res
	}

	maxBacktrack := 100
	if len(runes) < maxBacktrack {
		maxBacktrack = len(runes)
	}

	for k := 1; k <= maxBacktrack; k++ {
		lastNonSpaceIdx := len(runes) - k - 1
		for lastNonSpaceIdx >= 0 {
			r := runes[lastNonSpaceIdx]
			if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
				break
			}
			lastNonSpaceIdx--
		}

		if lastNonSpaceIdx < 0 {
			break
		}

		subInString := inStringAt[lastNonSpaceIdx]
		subEscaped := escapedAt[lastNonSpaceIdx]
		subStack := stackAt[lastNonSpaceIdx]

		subCandidate := string(runes[:lastNonSpaceIdx+1])
		if subInString {
			if subEscaped {
				if len(subCandidate) > 0 {
					subCandidate = subCandidate[:len(subCandidate)-1]
				}
			}
			subCandidate += `"`
		}
		curr := subStack
		for curr != nil {
			if curr.val == '{' {
				subCandidate += "}"
			} else if curr.val == '[' {
				subCandidate += "]"
			}
			curr = curr.parent
		}

		if res, ok := tryUnmarshal(subCandidate); ok {
			return res
		}
	}

	return make(map[string]any)
}

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"pi-go/pkg/ai"
)

type spyTool struct {
	name      string
	mode      ToolExecutionMode
	delay     time.Duration
	terminate bool
	called    chan int
	args      map[string]any
}

func (s *spyTool) Definition() ai.ToolDefinition {
	return ai.ToolDefinition{Name: s.name}
}

func (s *spyTool) Mode() ToolExecutionMode {
	return s.mode
}

func (s *spyTool) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, nil, false, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	s.args = args
	select {
	case s.called <- 1:
	default:
	}
	return []ai.ToolResultContent{ai.TextContent{Text: "Result of " + s.name}}, nil, s.terminate, nil
}

func setupMockProvider(t *testing.T, streamSimple func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream) {
	ai.ClearApiProviders()
	err := ai.RegisterApiProvider(ai.ApiProvider{
		API: ai.APIIDOpenAICodexResponses,
		Stream: func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.StreamOptions) *ai.AssistantStream {
			return nil
		},
		StreamSimple: streamSimple,
	})
	if err != nil {
		t.Fatalf("failed to register mock provider: %v", err)
	}
}

func TestAgentBasicLoop(t *testing.T) {
	// 1. Setup mock provider returning a basic text stream.
	setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		s := ai.NewAssistantStream(10)
		go func() {
			_ = s.Push(ai.AssistantMessageEvent{Type: ai.EventStart})
			_ = s.Push(ai.AssistantMessageEvent{
				Type:  ai.EventTextDelta,
				Delta: "Hello ",
			})
			_ = s.Push(ai.AssistantMessageEvent{
				Type:  ai.EventTextDelta,
				Delta: "world!",
			})
			s.End(&ai.AssistantMessage{
				Content: []ai.AssistantContent{
					ai.TextContent{Text: "Hello world!"},
				},
			})
		}()
		return s
	})

	model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
	reg := NewToolRegistry()
	agent := NewAgent(model, reg, Hooks{})

	ctx := context.Background()
	events := agent.Run(ctx, nil)

	var receivedEvents []AgentEvent
	for ev := range events {
		receivedEvents = append(receivedEvents, ev)
	}

	// Verify events sequence
	if len(receivedEvents) < 6 {
		t.Fatalf("expected at least 6 events, got %d", len(receivedEvents))
	}

	if receivedEvents[0].Type != EventTurnStart {
		t.Errorf("expected EventTurnStart first, got %s", receivedEvents[0].Type)
	}
	if receivedEvents[1].Type != EventStreamStart {
		t.Errorf("expected EventStreamStart second, got %s", receivedEvents[1].Type)
	}
	if receivedEvents[len(receivedEvents)-2].Type != EventStreamEnd {
		t.Errorf("expected EventStreamEnd, got %s", receivedEvents[len(receivedEvents)-2].Type)
	}
	if receivedEvents[len(receivedEvents)-1].Type != EventTurnEnd {
		t.Errorf("expected EventTurnEnd last, got %s", receivedEvents[len(receivedEvents)-1].Type)
	}

	// Verify History
	if len(agent.History) != 1 {
		t.Fatalf("expected history length 1, got %d", len(agent.History))
	}
	msg := agent.History[0]
	if msg.Role() != ai.RoleAssistant {
		t.Errorf("expected role assistant, got %s", msg.Role())
	}
}

func TestAgentParallelTools(t *testing.T) {
	// 1. Setup mock provider returning tool calls.
	setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		s := ai.NewAssistantStream(10)
		go func() {
			// First turn returns tool calls
			if len(c.Messages) == 0 {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.ToolCall{ID: "call-A", Name: "toolA"},
						ai.ToolCall{ID: "call-B", Name: "toolB"},
					},
				})
			} else {
				// Second turn completes
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Finished execution"},
					},
				})
			}
		}()
		return s
	})

	model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
	reg := NewToolRegistry()

	// toolA is slow (50ms)
	toolA := &spyTool{name: "toolA", mode: ToolExecutionModeParallel, delay: 50 * time.Millisecond, called: make(chan int, 1)}
	// toolB is fast (0ms)
	toolB := &spyTool{name: "toolB", mode: ToolExecutionModeParallel, called: make(chan int, 1)}

	reg.Register(toolA, toolB)

	agent := NewAgent(model, reg, Hooks{})
	ctx := context.Background()
	events := agent.Run(ctx, nil)

	var toolEndOrder []string
	var receivedEvents []AgentEvent
	for ev := range events {
		receivedEvents = append(receivedEvents, ev)
		if ev.Type == EventToolExecutionEnd {
			toolEndOrder = append(toolEndOrder, ev.ToolName)
		}
	}

	// Verify tool end order: toolB (fast) must complete BEFORE toolA (slow)
	if len(toolEndOrder) != 2 {
		t.Fatalf("expected 2 tool execution end events, got %v", toolEndOrder)
	}
	if toolEndOrder[0] != "toolB" || toolEndOrder[1] != "toolA" {
		t.Errorf("expected fast toolB to finish before slow toolA, got order: %v", toolEndOrder)
	}

	// Verify history order: must be in assistant source order (toolA's result before toolB's result)
	// History should have: [AssistantMsgWithToolCalls, ToolResultA, ToolResultB, AssistantMsgFinal]
	if len(agent.History) != 4 {
		t.Fatalf("expected history length 4, got %d", len(agent.History))
	}

	res1, ok1 := agent.History[1].(ToolResultMessage)
	res2, ok2 := agent.History[2].(ToolResultMessage)
	if !ok1 || !ok2 {
		t.Fatalf("expected messages to be ToolResultMessage, got %T and %T", agent.History[1], agent.History[2])
	}

	if res1.ToolCallID != "call-A" || res1.ToolName != "toolA" {
		t.Errorf("expected first tool result in history to be toolA, got %s", res1.ToolName)
	}
	if res2.ToolCallID != "call-B" || res2.ToolName != "toolB" {
		t.Errorf("expected second tool result in history to be toolB, got %s", res2.ToolName)
	}
}

func TestAgentSequentialTools(t *testing.T) {
	// 1. Setup mock provider returning tool calls.
	setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		s := ai.NewAssistantStream(10)
		go func() {
			if len(c.Messages) == 0 {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.ToolCall{ID: "call-A", Name: "toolA"},
						ai.ToolCall{ID: "call-B", Name: "toolB"},
					},
				})
			} else {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Finished"},
					},
				})
			}
		}()
		return s
	})

	model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
	reg := NewToolRegistry()

	var sequenceMu sync.Mutex
	var sequence []string

	toolA := &spyTool{name: "toolA", mode: ToolExecutionModeSequential, delay: 30 * time.Millisecond, called: make(chan int, 1)}
	toolB := &spyTool{name: "toolB", mode: ToolExecutionModeParallel, delay: 10 * time.Millisecond, called: make(chan int, 1)}

	// We wrap their Execute logic to record the order
	reg.Register(&seqSpyToolWrapper{spyTool: toolA, seq: &sequence, mu: &sequenceMu})
	reg.Register(&seqSpyToolWrapper{spyTool: toolB, seq: &sequence, mu: &sequenceMu})

	agent := NewAgent(model, reg, Hooks{})
	ctx := context.Background()
	events := agent.Run(ctx, nil)

	for range events {
	}

	sequenceMu.Lock()
	defer sequenceMu.Unlock()
	// Since toolA is sequential, it must start and finish before toolB is even called.
	// Therefore, the invocation sequence must be: toolA starts/executes, then toolB starts/executes.
	if len(sequence) != 2 {
		t.Fatalf("expected 2 invocations, got %v", sequence)
	}
	if sequence[0] != "toolA" || sequence[1] != "toolB" {
		t.Errorf("expected sequence toolA, toolB because toolA is sequential. got: %v", sequence)
	}
}

type seqSpyToolWrapper struct {
	*spyTool
	seq *[]string
	mu  *sync.Mutex
}

func (s *seqSpyToolWrapper) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	s.mu.Lock()
	*s.seq = append(*s.seq, s.name)
	s.mu.Unlock()
	return s.spyTool.Execute(ctx, args)
}

func TestAgentAllTerminateRule(t *testing.T) {
	t.Run("one terminates, one does not", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				if len(c.Messages) == 0 {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.ToolCall{ID: "call-A", Name: "toolA"},
							ai.ToolCall{ID: "call-B", Name: "toolB"},
						},
					})
				} else {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Finished after tools"},
						},
					})
				}
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()
		toolA := &spyTool{name: "toolA", terminate: true, called: make(chan int, 1)}
		toolB := &spyTool{name: "toolB", terminate: false, called: make(chan int, 1)}
		reg.Register(toolA, toolB)

		agent := NewAgent(model, reg, Hooks{})
		ctx := context.Background()
		events := agent.Run(ctx, nil)

		for range events {
		}

		// Since toolB did not terminate, loop should have run 2 turns and finished.
		// History should be: [AssistantToolCalls, ToolA, ToolB, AssistantFinal]
		if len(agent.History) != 4 {
			t.Errorf("expected history length 4, got %d", len(agent.History))
		}
	})

	t.Run("both terminate", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				if len(c.Messages) == 0 {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.ToolCall{ID: "call-A", Name: "toolA"},
							ai.ToolCall{ID: "call-B", Name: "toolB"},
						},
					})
				} else {
					t.Error("should not reach second turn because both tools set terminate=true")
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Unexpected turn"},
						},
					})
				}
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()
		toolA := &spyTool{name: "toolA", terminate: true, called: make(chan int, 1)}
		toolB := &spyTool{name: "toolB", terminate: true, called: make(chan int, 1)}
		reg.Register(toolA, toolB)

		agent := NewAgent(model, reg, Hooks{})
		ctx := context.Background()
		events := agent.Run(ctx, nil)

		for range events {
		}

		// Since both tools terminated, loop should have exited early.
		// History should be: [AssistantToolCalls, ToolA, ToolB]
		if len(agent.History) != 3 {
			t.Errorf("expected history length 3, got %d", len(agent.History))
		}
	})
}

func TestAgentSteeringQueue(t *testing.T) {
	setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		s := ai.NewAssistantStream(10)
		go func() {
			if len(c.Messages) == 1 {
				// verify steering message is present in history
				if _, ok := c.Messages[0].(ai.UserMessage); ok {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Acknowledged steering"},
						},
					})
				} else {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Missing steering"},
						},
					})
				}
			} else {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Empty messages"},
					},
				})
			}
		}()
		return s
	})

	model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
	reg := NewToolRegistry()
	agent := NewAgent(model, reg, Hooks{})

	// Add steering message
	agent.SteeringQueue.Push(UserMessage{ai.UserMessage{
		Content: "Steering prompt",
	}})

	ctx := context.Background()
	events := agent.Run(ctx, nil)
	for range events {
	}

	if len(agent.History) != 2 {
		t.Fatalf("expected history length 2, got %d", len(agent.History))
	}
	if agent.History[0].Role() != ai.RoleUser {
		t.Errorf("expected first message to be steering user message, got %v", agent.History[0].Role())
	}
}

func TestAgentFollowUpQueue(t *testing.T) {
	setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
		s := ai.NewAssistantStream(10)
		go func() {
			// We respond based on the turn
			if len(c.Messages) == 1 && c.Messages[0].(ai.UserMessage).Content == "Initial" {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Initial response"},
					},
				})
			} else if len(c.Messages) == 3 {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "FollowUp response"},
					},
				})
			} else {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Unexpected: " + fmt.Sprintf("%v", c.Messages)},
					},
				})
			}
		}()
		return s
	})

	model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
	reg := NewToolRegistry()

	// GetFollowUpMessages hook that returns a message only once
	hasFollowedUp := false
	hooks := Hooks{
		GetFollowUpMessages: func(ctx context.Context) ([]AgentMessage, error) {
			if !hasFollowedUp {
				hasFollowedUp = true
				return []AgentMessage{UserMessage{ai.UserMessage{Content: "FollowUp"}}}, nil
			}
			return nil, nil
		},
	}

	agent := NewAgent(model, reg, hooks)
	agent.History = append(agent.History, UserMessage{ai.UserMessage{Content: "Initial"}})

	ctx := context.Background()
	events := agent.Run(ctx, nil)
	for range events {
	}

	// Expecting: [UserMessage(Initial), AssistantMessage(Initial response), UserMessage(FollowUp), AssistantMessage(FollowUp response)]
	if len(agent.History) != 4 {
		t.Fatalf("expected history length 4, got %d", len(agent.History))
	}

	m2 := agent.History[2]
	m3 := agent.History[3]
	if m2.Role() != ai.RoleUser {
		t.Errorf("expected message 2 to be user follow-up, got %v", m2.Role())
	}
	if m3.Role() != ai.RoleAssistant {
		t.Errorf("expected message 3 to be assistant response, got %v", m3.Role())
	}
}

func TestAgentCancellationAndHookError(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			// Mock slow streaming
			go func() {
				select {
				case <-ctx.Done():
					s.Error(ctx.Err(), nil)
				}
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		agent := NewAgent(model, nil, Hooks{})

		ctx, cancel := context.WithCancel(context.Background())
		events := agent.Run(ctx, nil)

		// Cancel immediately after starting
		cancel()

		var hasError bool
		for ev := range events {
			if ev.Type == EventError {
				hasError = true
				if !errors.Is(ev.Error, context.Canceled) {
					t.Errorf("expected context.Canceled error, got %v", ev.Error)
				}
			}
		}

		if !hasError {
			t.Error("expected error event due to cancellation")
		}
	})

	t.Run("PrepareNextTurn hook error", func(t *testing.T) {
		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		testErr := errors.New("prepare hook failed")
		hooks := Hooks{
			PrepareNextTurn: func(ctx context.Context, history []AgentMessage) ([]AgentMessage, error) {
				return nil, testErr
			},
		}

		agent := NewAgent(model, nil, hooks)
		events := agent.Run(context.Background(), nil)

		var hasError bool
		for ev := range events {
			if ev.Type == EventError {
				hasError = true
				if ev.Error != testErr {
					t.Errorf("expected %v, got %v", testErr, ev.Error)
				}
			}
		}

		if !hasError {
			t.Error("expected error event from PrepareNextTurn hook")
		}
	})

	t.Run("BeforeToolCall skip", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.ToolCall{ID: "call-A", Name: "toolA"},
					},
				})
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()
		toolA := &spyTool{name: "toolA", called: make(chan int, 1)}
		reg.Register(toolA)

		// BeforeToolCall skips toolA
		hooks := Hooks{
			BeforeToolCall: func(ctx context.Context, toolCall *ai.ToolCall) (bool, error) {
				if toolCall.Name == "toolA" {
					return false, nil
				}
				return true, nil
			},
		}

		agent := NewAgent(model, reg, hooks)
		events := agent.Run(context.Background(), nil)
		for range events {
		}

		// verify toolA was never executed
		select {
		case <-toolA.called:
			t.Error("toolA should have been skipped")
		default:
		}
	})
}

func TestAgentAdditionalReviewFindings(t *testing.T) {
	t.Run("DrainModeOneAtATime", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.TextContent{Text: "Response"},
					},
				})
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		agent := NewAgent(model, nil, Hooks{})

		// Set steering queue to DrainModeOneAtATime
		agent.SteeringQueue.SetDrainMode(DrainModeOneAtATime)
		agent.SteeringQueue.Push(UserMessage{ai.UserMessage{Content: "Steering1"}})
		agent.SteeringQueue.Push(UserMessage{ai.UserMessage{Content: "Steering2"}})

		ctx := context.Background()
		events := agent.Run(ctx, nil)
		for range events {
		}

		// Should have drained exactly one steering message
		if len(agent.History) != 2 { // [Steering1, Response]
			t.Fatalf("expected history length 2, got %d", len(agent.History))
		}
		if agent.History[0].(UserMessage).UserMessage.Content != "Steering1" {
			t.Errorf("expected first history message to be Steering1, got %v", agent.History[0])
		}
		if agent.SteeringQueue.Len() != 1 {
			t.Errorf("expected steering queue to have 1 remaining message, got %d", agent.SteeringQueue.Len())
		}
	})

	t.Run("BeforeToolCall error aborts loop", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.ToolCall{ID: "call-A", Name: "toolA"},
					},
				})
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()
		toolA := &spyTool{name: "toolA", called: make(chan int, 1)}
		reg.Register(toolA)

		hookErr := errors.New("before tool call hook failed")
		hooks := Hooks{
			BeforeToolCall: func(ctx context.Context, toolCall *ai.ToolCall) (bool, error) {
				return false, hookErr
			},
		}

		agent := NewAgent(model, reg, hooks)
		events := agent.Run(context.Background(), nil)

		var receivedError error
		for ev := range events {
			if ev.Type == EventError {
				receivedError = ev.Error
			}
		}

		if receivedError != hookErr {
			t.Errorf("expected hook error %v, got %v", hookErr, receivedError)
		}
		select {
		case <-toolA.called:
			t.Error("toolA should not have been called due to hook error")
		default:
		}
	})

	t.Run("AfterToolCall error aborts loop", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				s.End(&ai.AssistantMessage{
					Content: []ai.AssistantContent{
						ai.ToolCall{ID: "call-A", Name: "toolA"},
					},
				})
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()
		toolA := &spyTool{name: "toolA", called: make(chan int, 1)}
		reg.Register(toolA)

		hookErr := errors.New("after tool call hook failed")
		hooks := Hooks{
			AfterToolCall: func(ctx context.Context, toolCall *ai.ToolCall, result *ai.ToolResultMessage) error {
				return hookErr
			},
		}

		agent := NewAgent(model, reg, hooks)
		events := agent.Run(context.Background(), nil)

		var receivedError error
		for ev := range events {
			if ev.Type == EventError {
				receivedError = ev.Error
			}
		}

		if receivedError != hookErr {
			t.Errorf("expected hook error %v, got %v", hookErr, receivedError)
		}
	})

	t.Run("Nil registry does not panic", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				if len(c.Messages) == 0 {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.ToolCall{ID: "call-A", Name: "nonexistent"},
						},
					})
				} else {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Done"},
						},
					})
				}
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		agent := NewAgent(model, nil, Hooks{})

		ctx := context.Background()
		events := agent.Run(ctx, nil)
		for range events {
		}

		if len(agent.History) != 3 {
			t.Fatalf("expected history length 3, got %d", len(agent.History))
		}
		resMsg, ok := agent.History[1].(ToolResultMessage)
		if !ok {
			t.Fatalf("expected ToolResultMessage, got %T", agent.History[1])
		}
		if !resMsg.IsError {
			t.Error("expected tool result to indicate error")
		}
	})

	t.Run("Sequential tool blocks until parallel tools finish", func(t *testing.T) {
		setupMockProvider(t, func(ctx context.Context, model ai.Model, c ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantStream {
			s := ai.NewAssistantStream(10)
			go func() {
				if len(c.Messages) == 0 {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.ToolCall{ID: "call-A", Name: "parallelTool"},
							ai.ToolCall{ID: "call-B", Name: "sequentialTool"},
						},
					})
				} else {
					s.End(&ai.AssistantMessage{
						Content: []ai.AssistantContent{
							ai.TextContent{Text: "Done"},
						},
					})
				}
			}()
			return s
		})

		model := ai.Model{API: ai.APIIDOpenAICodexResponses, Provider: ai.ProviderIDOpenAICodex}
		reg := NewToolRegistry()

		var mu sync.Mutex
		var sequence []string
		var parallelDone bool
		var testFailed bool

		parallelTool := &spyTool{name: "parallelTool", mode: ToolExecutionModeParallel, delay: 30 * time.Millisecond, called: make(chan int, 1)}
		sequentialTool := &spyTool{name: "sequentialTool", mode: ToolExecutionModeSequential, delay: 10 * time.Millisecond, called: make(chan int, 1)}

		reg.Register(&strictSeqSpyWrapper{spyTool: parallelTool, mu: &mu, sequence: &sequence, parallelDone: &parallelDone, testFailed: &testFailed})
		reg.Register(&strictSeqSpyWrapper{spyTool: sequentialTool, mu: &mu, sequence: &sequence, parallelDone: &parallelDone, testFailed: &testFailed})

		agent := NewAgent(model, reg, Hooks{})
		events := agent.Run(context.Background(), nil)
		for range events {
		}

		mu.Lock()
		defer mu.Unlock()

		if testFailed {
			t.Error("sequentialTool executed before parallelTool was finished")
		}

		expected := []string{
			"parallelTool_start",
			"parallelTool_end",
			"sequentialTool_start",
			"sequentialTool_end",
		}
		if len(sequence) != len(expected) {
			t.Fatalf("expected sequence length %d, got %d (%v)", len(expected), len(sequence), sequence)
		}
		for i, v := range expected {
			if sequence[i] != v {
				t.Errorf("expected sequence[%d] = %q, got %q", i, v, sequence[i])
			}
		}
	})
}

type strictSeqSpyWrapper struct {
	*spyTool
	mu           *sync.Mutex
	sequence     *[]string
	parallelDone *bool
	testFailed   *bool
}

func (s *strictSeqSpyWrapper) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	s.mu.Lock()
	if s.mode == ToolExecutionModeSequential && !*s.parallelDone {
		*s.testFailed = true
	}
	*s.sequence = append(*s.sequence, s.name+"_start")
	s.mu.Unlock()

	res, details, term, err := s.spyTool.Execute(ctx, args)

	s.mu.Lock()
	if s.mode == ToolExecutionModeParallel {
		*s.parallelDone = true
	}
	*s.sequence = append(*s.sequence, s.name+"_end")
	s.mu.Unlock()

	return res, details, term, err
}

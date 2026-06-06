// Package agent provides an execution agent that orchestrates multi-turn reasoning loops,
// managing message history, queues, models, and parallel or sequential tool execution.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pi-go/pkg/ai"
)

// Agent orchestrates the agent loop, managing history, steering/follow-up queues,
// model requests, and tool execution batches.
type Agent struct {
	Model           ai.Model
	Registry        *ToolRegistry
	Hooks           Hooks
	SteeringQueue   *MessageQueue
	FollowUpQueue   *MessageQueue
	History         []AgentMessage
	SystemPrompt    string
	MaxTurns        int
	SequentialTools bool
	SessionID       string
}

// NewAgent creates and configures a new Agent.
func NewAgent(model ai.Model, registry *ToolRegistry, hooks Hooks) *Agent {
	hooks.FillDefaults()
	return &Agent{
		Model:         model,
		Registry:      registry,
		Hooks:         hooks,
		SteeringQueue: NewMessageQueue(DrainModeAll),
		FollowUpQueue: NewMessageQueue(DrainModeAll),
		History:       make([]AgentMessage, 0),
		MaxTurns:      100,
	}
}

// Run executes the agent loop, streaming events back to the caller.
// It stops when there are no more tool calls or follow-up messages, or when terminated by a hook/tool.
func (a *Agent) Run(ctx context.Context, opts *ai.SimpleStreamOptions) <-chan AgentEvent {
	eventChan := make(chan AgentEvent, 100)

	go func() {
		defer close(eventChan)

		if opts == nil {
			opts = &ai.SimpleStreamOptions{}
		}

		turn := 0
		for {
			// Check context cancellation
			if err := ctx.Err(); err != nil {
				eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
				return
			}

			// Safety check: max turns
			turn++
			if turn > a.MaxTurns {
				eventChan <- AgentEvent{
					Type:    EventError,
					Error:   fmt.Errorf("max turns (%d) reached", a.MaxTurns),
					IsError: true,
				}
				return
			}

			// 1. Drain steering messages at turn start
			steeringMsgs := a.SteeringQueue.Drain()
			if len(steeringMsgs) > 0 {
				a.History = append(a.History, steeringMsgs...)
			}
			hookSteering, err := a.Hooks.GetSteeringMessages(ctx)
			if err != nil {
				eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
				return
			}
			if len(hookSteering) > 0 {
				a.History = append(a.History, hookSteering...)
			}

			// Emit turn start
			eventChan <- AgentEvent{Type: EventTurnStart}

			// 2. Run PrepareNextTurn hook
			turnHistory, err := a.Hooks.PrepareNextTurn(ctx, a.History)
			if err != nil {
				eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
				return
			}

			// Convert to LLM messages
			llmMessages := convertToLlm(turnHistory)

			// Get tool definitions from registry
			var toolDefs []ai.ToolDefinition
			if a.Registry != nil {
				toolDefs = a.Registry.Definitions()
			}

			// Resolve API key
			apiKey, err := a.Hooks.GetApiKey(ctx, a.Model.Provider)
			if err != nil {
				eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
				return
			}
			opts.APIKey = apiKey

			// Emit stream start
			eventChan <- AgentEvent{Type: EventStreamStart}

			// Start LLM stream
			stream := ai.StreamSimple(ctx, a.Model, ai.Context{
				SystemPrompt: a.SystemPrompt,
				Messages:     llmMessages,
				Tools:        toolDefs,
			}, opts)

			// Consume streaming events
			for event := range stream.Events() {
				if event.Type == ai.EventDone {
					continue
				}
				if event.Type == ai.EventError {
					var errVal error
					if event.Error != nil && event.Error.ErrorMessage != "" {
						errVal = fmt.Errorf("%s", event.Error.ErrorMessage)
					} else {
						errVal = fmt.Errorf("stream error")
					}
					eventChan <- AgentEvent{
						Type:    EventError,
						IsError: true,
						Error:   errVal,
					}
				} else {
					eventChan <- AgentEvent{
						Type:        EventStreamDelta,
						StreamEvent: &event,
					}
				}
			}

			// Retrieve the final result
			finalMsg, err := stream.Result()
			if err != nil {
				eventChan <- AgentEvent{
					Type:    EventError,
					IsError: true,
					Error:   err,
				}
				return
			}

			// Emit stream end
			eventChan <- AgentEvent{Type: EventStreamEnd}

			// Append assistant message to history
			a.History = append(a.History, AssistantMessage{finalMsg})

			// 3. Extract and execute tool calls
			toolCalls := extractToolCalls(finalMsg)
			var batchTerminated bool
			if len(toolCalls) > 0 {
				results := make([]*ai.ToolResultMessage, len(toolCalls))
				terminates := make([]bool, len(toolCalls))
				skipped := make([]bool, len(toolCalls))

				var jobErr error
				var jobErrMu sync.Mutex

				setJobError := func(err error) {
					jobErrMu.Lock()
					if jobErr == nil {
						jobErr = err
					}
					jobErrMu.Unlock()
				}

				var wg sync.WaitGroup

				executeJob := func(idx int, t AgentTool, call ai.ToolCall) {
					proceed, err := a.Hooks.BeforeToolCall(ctx, &call)
					if err != nil {
						setJobError(err)
						return
					}
					if !proceed {
						skipped[idx] = true
						return
					}

					// Emit ToolExecutionStart
					eventChan <- AgentEvent{
						Type:       EventToolExecutionStart,
						ToolName:   call.Name,
						ToolCallID: call.ID,
					}

					// Execute tool
					content, details, terminate, err := t.Execute(ctx, call.Arguments)
					isErr := false
					if err != nil {
						isErr = true
						if len(content) == 0 {
							content = []ai.ToolResultContent{ai.TextContent{Text: "Error: " + err.Error()}}
						}
					}

					res := &ai.ToolResultMessage{
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Content:    content,
						Details:    details,
						IsError:    isErr,
						Timestamp:  time.Now().UnixMilli(),
					}

					// Run AfterToolCall hook
					err = a.Hooks.AfterToolCall(ctx, &call, res)
					if err != nil {
						setJobError(err)
						return
					}

					// Emit ToolExecutionEnd
					eventChan <- AgentEvent{
						Type:       EventToolExecutionEnd,
						ToolName:   call.Name,
						ToolCallID: call.ID,
						ToolOutput: res,
						IsError:    isErr,
					}

					results[idx] = res
					terminates[idx] = terminate
				}

				for i, tc := range toolCalls {
					// Check if a previous job errored
					jobErrMu.Lock()
					hasErr := jobErr != nil
					jobErrMu.Unlock()
					if hasErr {
						break
					}

					var tool AgentTool
					var ok bool
					if a.Registry != nil {
						tool, ok = a.Registry.Lookup(tc.Name)
					}

					if !ok {
						res := &ai.ToolResultMessage{
							ToolCallID: tc.ID,
							ToolName:   tc.Name,
							Content:    []ai.ToolResultContent{ai.TextContent{Text: "Error: tool not found"}},
							IsError:    true,
							Timestamp:  time.Now().UnixMilli(),
						}
						results[i] = res
						eventChan <- AgentEvent{
							Type:       EventToolExecutionEnd,
							ToolName:   tc.Name,
							ToolCallID: tc.ID,
							ToolOutput: res,
							IsError:    true,
						}
						continue
					}

					isSequential := a.SequentialTools || tool.Mode() == ToolExecutionModeSequential

					if isSequential {
						// Wait for all outstanding parallel tools to finish
						wg.Wait()

						// Check again if any parallel tool errored
						jobErrMu.Lock()
						hasErr = jobErr != nil
						jobErrMu.Unlock()
						if hasErr {
							break
						}

						// Run sequential tool synchronously inline
						executeJob(i, tool, tc)
					} else {
						wg.Add(1)
						go func(idx int, t AgentTool, call ai.ToolCall) {
							defer wg.Done()
							executeJob(idx, t, call)
						}(i, tool, tc)
					}
				}

				// Wait for any remaining parallel tools to complete
				wg.Wait()

				// If a job failed with a hook error, abort the loop and emit EventError
				jobErrMu.Lock()
				finalJobErr := jobErr
				jobErrMu.Unlock()
				if finalJobErr != nil {
					eventChan <- AgentEvent{Type: EventError, Error: finalJobErr, IsError: true}
					return
				}

				// Append non-nil results in original source order
				executedCount := 0
				allTerminate := true
				for i, res := range results {
					if res == nil {
						continue
					}
					a.History = append(a.History, ToolResultMessage{*res})
					if !skipped[i] {
						executedCount++
						if !terminates[i] {
							allTerminate = false
						}
					}
				}

				if executedCount > 0 && allTerminate {
					batchTerminated = true
				}
			}

			// Emit turn end
			eventChan <- AgentEvent{Type: EventTurnEnd}

			// 4. Check ShouldStopAfterTurn hook
			stopTurn, err := a.Hooks.ShouldStopAfterTurn(ctx, a.History)
			if err != nil {
				eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
				return
			}

			if stopTurn || batchTerminated || len(toolCalls) == 0 {
				hasFollowUp := false

				// Drain follow-up queue
				followUpMsgs := a.FollowUpQueue.Drain()
				if len(followUpMsgs) > 0 {
					a.History = append(a.History, followUpMsgs...)
					hasFollowUp = true
				}

				// Get follow-up messages from hook
				hookFollowUp, err := a.Hooks.GetFollowUpMessages(ctx)
				if err != nil {
					eventChan <- AgentEvent{Type: EventError, Error: err, IsError: true}
					return
				}
				if len(hookFollowUp) > 0 {
					a.History = append(a.History, hookFollowUp...)
					hasFollowUp = true
				}

				if !hasFollowUp {
					return
				}
			}
		}
	}()

	return eventChan
}


func extractToolCalls(msg ai.AssistantMessage) []ai.ToolCall {
	var calls []ai.ToolCall
	for _, content := range msg.Content {
		if tc, ok := content.(ai.ToolCall); ok {
			calls = append(calls, tc)
		} else if tcPtr, ok := content.(*ai.ToolCall); ok && tcPtr != nil {
			calls = append(calls, *tcPtr)
		}
	}
	return calls
}

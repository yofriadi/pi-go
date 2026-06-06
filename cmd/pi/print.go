package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"pi-go/pkg/agent"
	"pi-go/pkg/ai"
	"pi-go/pkg/tools"
)

// builtInToolNames defines the set of recognized built-in tool names.
var builtInToolNames = map[string]bool{
	"read":  true,
	"write": true,
	"edit":  true,
	"bash":  true,
}

// buildToolRegistry filters and constructs the agent tool registry based on CLI options.
func buildToolRegistry(opts *CLIOptions) *agent.ToolRegistry {
	registry := agent.NewToolRegistry()
	if opts.NoTools || opts.NoBuiltinTools {
		return registry
	}
	builtIns := map[string]agent.AgentTool{
		"read":  tools.NewReadTool(tools.OSFileSystem{}),
		"write": tools.NewWriteTool(tools.OSFileSystem{}),
		"edit":  tools.NewEditTool(tools.OSFileSystem{}),
		"bash":  tools.NewBashTool(tools.OSShellExecutor{}),
	}
	// 1. Determine base tools
	var enabled []string
	if opts.Tools != "" {
		parts := strings.Split(opts.Tools, ",")
		for _, p := range parts {
			name := strings.TrimSpace(p)
			if name != "" {
				enabled = append(enabled, name)
			}
		}
	} else {
		for name := range builtIns {
			enabled = append(enabled, name)
		}
	}

	// 2. Filter exclusions
	excluded := make(map[string]bool)
	if opts.ExcludeTools != "" {
		parts := strings.Split(opts.ExcludeTools, ",")
		for _, p := range parts {
			name := strings.TrimSpace(p)
			if name != "" {
				excluded[name] = true
			}
		}
	}

	// 3. Register remaining
	for _, name := range enabled {
		if excluded[name] {
			continue
		}
		if t, ok := builtIns[name]; ok {
			registry.Register(t)
		}
	}

	return registry
}

// RunPrintMode runs the agent in non-interactive print mode.
func RunPrintMode(ctx context.Context, opts *CLIOptions) error {
	model, ok := ai.GetModel(opts.ModelID)
	if !ok {
		return fmt.Errorf("model %q not found in registry", opts.ModelID)
	}

	registry := buildToolRegistry(opts)

	hooks := agent.Hooks{
		GetApiKey: func(ctx context.Context, provider ai.ProviderID) (string, error) {
			if provider == ai.ProviderIDOpenAICodex {
				return ai.ResolveCodexToken(ctx)
			}
			return "", fmt.Errorf("unsupported provider: %s", provider)
		},
	}

	a := agent.NewAgent(model, registry, hooks)

	// Process system prompt override/append
	systemPrompt := ""
	if opts.SystemPrompt != "" {
		systemPrompt = opts.SystemPrompt
	}
	if opts.AppendSystemPrompt != "" {
		if systemPrompt != "" {
			systemPrompt += "\n" + opts.AppendSystemPrompt
		} else {
			systemPrompt = opts.AppendSystemPrompt
		}
	}
	if systemPrompt != "" {
		a.SystemPrompt = systemPrompt
	}

	// Set prompt
	if opts.PrintPrompt != "" {
		uMsg := agent.UserMessage{
			UserMessage: ai.UserMessage{
				Content:   opts.PrintPrompt,
				Timestamp: time.Now().UnixNano() / int64(time.Millisecond),
			},
		}
		a.History = append(a.History, uMsg)
	}

	simpleOpts := &ai.SimpleStreamOptions{
		Reasoning: ai.ModelThinkingLevel(opts.Thinking),
	}

	eventChan := a.Run(ctx, simpleOpts)

	for ev := range eventChan {
		switch ev.Type {
		case agent.EventTurnStart:
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, "\n--- Starting Turn ---")
			}
		case agent.EventTurnEnd:
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, "\n--- Turn Ended ---")
			}
		case agent.EventStreamStart:
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, "--- Stream Starting ---")
			}
		case agent.EventStreamDelta:
			if ev.StreamEvent != nil {
				switch ev.StreamEvent.Type {
				case ai.EventTextDelta:
					fmt.Fprint(os.Stdout, ev.StreamEvent.Delta)
				case ai.EventThinkingDelta:
					fmt.Fprint(os.Stderr, ev.StreamEvent.Delta)
				}
			}
		case agent.EventStreamEnd:
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, "\n--- Stream Ended ---")
			}
		case agent.EventToolExecutionStart:
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "\n[Executing tool %s (id: %s)]\n", ev.ToolName, ev.ToolCallID)
			}
		case agent.EventToolExecutionEnd:
			if opts.Verbose {
				if ev.IsError {
					fmt.Fprintf(os.Stderr, "\n[Tool %s failed: %v]\n", ev.ToolName, ev.Error)
				} else {
					fmt.Fprintf(os.Stderr, "\n[Tool %s completed]\n", ev.ToolName)
				}
			}
		case agent.EventError:
			fmt.Fprintf(os.Stderr, "\nError: %v\n", ev.Error)
			return ev.Error
		}
	}

	return nil
}

package agent

import (
	"context"
	"sort"

	"pi-go/pkg/ai"
)
// ToolExecutionMode controls whether a tool runs concurrently with other tools in a batch,
// or if it requires sequential execution.
type ToolExecutionMode string

const (
	// ToolExecutionModeParallel allows the tool to be executed concurrently with others in the batch.
	ToolExecutionModeParallel ToolExecutionMode = "parallel"

	// ToolExecutionModeSequential forces the tool to execute sequentially, blocking other tools in the batch.
	ToolExecutionModeSequential ToolExecutionMode = "sequential"
)

// AgentTool represents a tool that can be invoked by the agent.
type AgentTool interface {
	// Definition returns the schema definition of the tool for model consumption.
	Definition() ai.ToolDefinition

	// Execute runs the tool with the parsed JSON arguments.
	// It returns the result content, optional details, a termination flag (true if the agent
	// should stop execution early), and any execution error.
	Execute(ctx context.Context, args map[string]any) (content []ai.ToolResultContent, details any, terminate bool, err error)

	// Mode returns the execution mode (parallel or sequential).
	Mode() ToolExecutionMode
}

// ToolRegistry manages a collection of AgentTools available to the agent.
type ToolRegistry struct {
	tools map[string]AgentTool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]AgentTool),
	}
}

// Register adds one or more tools to the registry.
func (r *ToolRegistry) Register(ts ...AgentTool) {
	for _, t := range ts {
		if t != nil {
			r.tools[t.Definition().Name] = t
		}
	}
}

// Lookup returns the tool with the given name, and whether it was found.
func (r *ToolRegistry) Lookup(name string) (AgentTool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the ai.ToolDefinition of all registered tools.
func (r *ToolRegistry) Definitions() []ai.ToolDefinition {
	defs := make([]ai.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}

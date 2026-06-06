package tools

import (
	"context"
	"fmt"
	"path/filepath"

	"pi-go/pkg/agent"
	"pi-go/pkg/ai"
)

// WriteTool implements agent.AgentTool to write contents to a file.
type WriteTool struct {
	fs FileSystem
}

// NewWriteTool creates a new WriteTool with the given FileSystem.
func NewWriteTool(fs FileSystem) *WriteTool {
	return &WriteTool{fs: fs}
}

// Definition returns the tool schema definition.
func (t *WriteTool) Definition() ai.ToolDefinition {
	return WriteToolDefinition
}

// Mode returns the tool's execution mode (parallel).
func (t *WriteTool) Mode() agent.ToolExecutionMode {
	return agent.ToolExecutionModeParallel
}

// Execute runs the write tool.
func (t *WriteTool) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	pathVal, ok := args["path"].(string)
	if !ok || pathVal == "" {
		return nil, nil, false, fmt.Errorf("missing or invalid 'path' parameter")
	}

	contentVal, ok := args["content"].(string)
	if !ok {
		return nil, nil, false, fmt.Errorf("missing or invalid 'content' parameter")
	}

	// Recursively create parent directories
	dir := filepath.Dir(pathVal)
	if dir != "" && dir != "." {
		if err := t.fs.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, false, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write file content with 0644 permissions
	if err := t.fs.WriteFile(pathVal, []byte(contentVal), 0o644); err != nil {
		return nil, nil, false, fmt.Errorf("failed to write file %s: %w", pathVal, err)
	}

	successMsg := fmt.Sprintf("Successfully wrote %d bytes to %s", len(contentVal), pathVal)
	return []ai.ToolResultContent{ai.TextContent{Text: successMsg}}, map[string]any{
		"path":  pathVal,
		"bytes": len(contentVal),
	}, false, nil
}

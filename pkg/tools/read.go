package tools

import (
	"context"
	"fmt"
	"strings"

	"pi-go/pkg/agent"
	"pi-go/pkg/ai"
)

// ReadTool implements agent.AgentTool to read file contents.
type ReadTool struct {
	fs FileSystem
}

// NewReadTool creates a new ReadTool with the given FileSystem.
func NewReadTool(fs FileSystem) *ReadTool {
	return &ReadTool{fs: fs}
}

// Definition returns the tool schema definition.
func (t *ReadTool) Definition() ai.ToolDefinition {
	return ReadToolDefinition
}

// Mode returns the tool's execution mode (parallel).
func (t *ReadTool) Mode() agent.ToolExecutionMode {
	return agent.ToolExecutionModeParallel
}

// Execute runs the read tool.
func (t *ReadTool) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	pathVal, ok := args["path"].(string)
	if !ok || pathVal == "" {
		return nil, nil, false, fmt.Errorf("missing or invalid 'path' parameter")
	}

	fi, err := t.fs.Stat(pathVal)
	if err != nil {
		return nil, nil, false, err
	}

	if fi.IsDir() {
		entries, err := t.fs.ReadDir(pathVal)
		if err != nil {
			return nil, nil, false, err
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Directory listing for %s:\n", pathVal))
		for _, entry := range entries {
			info, err := entry.Info()
			size := int64(0)
			mode := ""
			modTime := ""
			if err == nil {
				size = info.Size()
				mode = info.Mode().String()
				modTime = info.ModTime().Format("2006-01-02 15:04:05")
			}
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			sb.WriteString(fmt.Sprintf("%s %10d %s %s\n", mode, size, modTime, name))
		}
		return []ai.ToolResultContent{ai.TextContent{Text: sb.String()}}, nil, false, nil
	}

	// Read file content
	data, err := t.fs.ReadFile(pathVal)
	if err != nil {
		return nil, nil, false, err
	}

	// Parse offset
	offset := 1
	if offsetVal, exists := args["offset"]; exists {
		switch v := offsetVal.(type) {
		case float64:
			offset = int(v)
		case int:
			offset = v
		}
	}
	if offset < 1 {
		offset = 1
	}

	// Parse limit
	limit := 2000
	hasLimit := false
	if limitVal, exists := args["limit"]; exists {
		switch v := limitVal.(type) {
		case float64:
			limit = int(v)
			hasLimit = true
		case int:
			limit = v
			hasLimit = true
		}
	}
	if limit <= 0 && hasLimit {
		limit = 2000
	}

	type fileLine struct {
		start int
		end   int
	}

	var lines []fileLine
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, fileLine{start: start, end: i + 1})
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, fileLine{start: start, end: len(data)})
	}

	startLineIdx := offset - 1
	if startLineIdx >= len(lines) {
		return []ai.ToolResultContent{ai.TextContent{Text: ""}}, map[string]any{
			"path":       pathVal,
			"linesRead":  0,
			"truncated":  false,
			"totalLines": len(lines),
		}, false, nil
	}

	endLineIdx := startLineIdx + limit
	if endLineIdx > len(lines) {
		endLineIdx = len(lines)
	}

	maxBytes := 50 * 1024 // 50 KiB
	totalBytes := 0
	actualEndLineIdx := startLineIdx
	singleLineTruncated := false

	for i := startLineIdx; i < endLineIdx; i++ {
		lineLen := lines[i].end - lines[i].start
		if i == startLineIdx && lineLen > maxBytes {
			totalBytes = maxBytes
			actualEndLineIdx = i + 1
			singleLineTruncated = true
			break
		}
		if totalBytes+lineLen > maxBytes {
			break
		}
		totalBytes += lineLen
		actualEndLineIdx = i + 1
	}

	var readBytes []byte
	if actualEndLineIdx > startLineIdx {
		endPos := lines[actualEndLineIdx-1].end
		if singleLineTruncated {
			endPos = lines[startLineIdx].start + maxBytes
		}
		readBytes = data[lines[startLineIdx].start:endPos]
	}

	truncated := (actualEndLineIdx < endLineIdx) || (endLineIdx < len(lines)) || singleLineTruncated

	return []ai.ToolResultContent{ai.TextContent{Text: string(readBytes)}}, map[string]any{
		"path":       pathVal,
		"linesRead":  actualEndLineIdx - startLineIdx,
		"truncated":  truncated,
		"totalLines": len(lines),
	}, false, nil
}

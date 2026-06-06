package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"pi-go/pkg/agent"
	"pi-go/pkg/ai"
)

// TailBuffer holds a bounded rolling tail of UTF-8 text.
type TailBuffer struct {
	mu       sync.Mutex
	maxBytes int
	buf      []byte
}

// NewTailBuffer creates a new TailBuffer with maxBytes limit.
func NewTailBuffer(maxBytes int) *TailBuffer {
	return &TailBuffer{
		maxBytes: maxBytes,
		buf:      make([]byte, 0, maxBytes*2),
	}
}

// Write appends data to the tail buffer and maintains the size limit.
func (tb *TailBuffer) Write(p []byte) (n int, err error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.buf = append(tb.buf, p...)
	if len(tb.buf) > tb.maxBytes*2 {
		keep := tb.buf[len(tb.buf)-tb.maxBytes:]
		newBuf := make([]byte, len(keep), tb.maxBytes*2)
		copy(newBuf, keep)
		tb.buf = newBuf
	}
	return len(p), nil
}

// String returns the tail buffer as a valid UTF-8 string.
func (tb *TailBuffer) String() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if len(tb.buf) <= tb.maxBytes {
		return string(tb.buf)
	}
	start := len(tb.buf) - tb.maxBytes
	// Scan forward to ensure we start at a valid UTF-8 rune boundary
	for start < len(tb.buf) {
		b := tb.buf[start]
		// Continuation bytes have the binary pattern 10xxxxxx
		if (b & 0xC0) != 0x80 {
			break
		}
		start++
	}
	return string(tb.buf[start:])
}

type trackingWriter struct {
	mu sync.Mutex
	w  io.Writer
	n  *int64
}

func (tw *trackingWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	n, err := tw.w.Write(p)
	*tw.n += int64(n)
	return n, err
}

// BashTool implements agent.AgentTool to run shell commands.
type BashTool struct {
	exec ShellExecutor
}

// NewBashTool creates a new BashTool with the given ShellExecutor.
func NewBashTool(exec ShellExecutor) *BashTool {
	return &BashTool{exec: exec}
}

// Definition returns the tool schema definition.
func (t *BashTool) Definition() ai.ToolDefinition {
	return BashToolDefinition
}

// Mode returns the tool's execution mode (parallel).
func (t *BashTool) Mode() agent.ToolExecutionMode {
	return agent.ToolExecutionModeParallel
}

// Execute runs the bash tool.
func (t *BashTool) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return nil, nil, false, fmt.Errorf("missing or invalid 'command' parameter")
	}

	timeoutSec := 0
	if tVal, exists := args["timeout"]; exists {
		switch v := tVal.(type) {
		case float64:
			timeoutSec = int(v)
		case int:
			timeoutSec = v
		}
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Create temp file for spooling the output
	tempFile, err := os.CreateTemp("", "pi-bash-spool-*.log")
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to create temp file: %w", err)
	}
	// Note: We keep the file on disk as returned in details.

	maxTailBytes := 50 * 1024 // 50 KiB
	tailBuf := NewTailBuffer(maxTailBytes)
	var totalBytes int64

	writer := &trackingWriter{
		w: io.MultiWriter(tempFile, tailBuf),
		n: &totalBytes,
	}
	runErr := t.exec.RunCommand(runCtx, command, writer, writer)

	// Make sure everything is written and sync file
	_ = tempFile.Sync()
	_ = tempFile.Close()

	exitCode := 0
	var execErr error

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			execErr = fmt.Errorf("command exited with code %d", exitCode)
		} else if runCtx.Err() == context.DeadlineExceeded {
			exitCode = -1
			execErr = fmt.Errorf("command timed out after %d seconds", timeoutSec)
		} else if runCtx.Err() == context.Canceled {
			exitCode = -1
			execErr = fmt.Errorf("command canceled")
		} else {
			exitCode = -1
			execErr = runErr
		}
	}

	// Read output for result
	var resultText string
	truncated := totalBytes > int64(maxTailBytes)

	if truncated {
		resultText = tailBuf.String()
		resultText += fmt.Sprintf("\n\n[Output truncated. Full output saved to %s]", tempFile.Name())
	} else {
		// Read entire spooled content
		spooledData, readErr := os.ReadFile(tempFile.Name())
		if readErr == nil {
			resultText = string(spooledData)
		} else {
			resultText = tailBuf.String() // fallback
		}
	}

	details := map[string]any{
		"tempFilePath": tempFile.Name(),
		"truncated":    truncated,
		"exitCode":     exitCode,
	}

	if execErr != nil {
		return []ai.ToolResultContent{ai.TextContent{Text: resultText}}, details, false, execErr
	}

	return []ai.ToolResultContent{ai.TextContent{Text: resultText}}, details, false, nil
}

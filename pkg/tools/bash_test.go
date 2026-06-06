package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"pi-go/pkg/ai"
)

type MockShellExecutor struct {
	outputs []string
	delay   time.Duration
	err     error
}

func (m *MockShellExecutor) RunCommand(ctx context.Context, cmdStr string, stdout io.Writer, stderr io.Writer) error {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}

	for _, s := range m.outputs {
		_, _ = stdout.Write([]byte(s))
	}

	return m.err
}

func TestBashTool_MockBasic(t *testing.T) {
	mockExec := &MockShellExecutor{
		outputs: []string{"hello", " world\nline 2\n"},
		err:     nil,
	}

	tool := NewBashTool(mockExec)
	args := map[string]any{"command": "echo hello"}

	content, details, term, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term {
		t.Errorf("expected terminate to be false")
	}

	txtBlock := content[0].(ai.TextContent)
	if txtBlock.Text != "hello world\nline 2\n" {
		t.Errorf("unexpected output: %q", txtBlock.Text)
	}

	detMap := details.(map[string]any)
	if detMap["truncated"].(bool) != false || detMap["exitCode"].(int) != 0 {
		t.Errorf("unexpected details: %+v", details)
	}

	// Verify temp file exists and has full content
	tempFilePath := detMap["tempFilePath"].(string)
	defer os.Remove(tempFilePath)

	spooled, err := os.ReadFile(tempFilePath)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if string(spooled) != "hello world\nline 2\n" {
		t.Errorf("spooled content mismatch: %q", string(spooled))
	}
}

func TestBashTool_MockTimeout(t *testing.T) {
	mockExec := &MockShellExecutor{
		outputs: []string{"partial output"},
		delay:   500 * time.Millisecond,
		err:     nil,
	}

	tool := NewBashTool(mockExec)
	// Set timeout parameter to 1 second, but we pass a context that will cancel in 100ms
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	args := map[string]any{"command": "sleep 5"}
	_, details, _, err := tool.Execute(ctx, args)
	if err == nil {
		t.Errorf("expected timeout/canceled error, got nil")
	}

	detMap := details.(map[string]any)
	if detMap["exitCode"].(int) != -1 {
		t.Errorf("expected exitCode -1 for cancellation, got %v", detMap["exitCode"])
	}

	// Clean up temp file
	if path, ok := detMap["tempFilePath"].(string); ok {
		os.Remove(path)
	}
}

func TestBashTool_TimeoutParameter(t *testing.T) {
	mockExec := &MockShellExecutor{
		outputs: []string{"partial output"},
		delay:   5 * time.Second,
		err:     nil,
	}

	tool := NewBashTool(mockExec)
	args := map[string]any{
		"command": "sleep 10",
		"timeout": float64(1), // 1 second timeout
	}

	_, details, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Errorf("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message, got %v", err)
	}

	detMap := details.(map[string]any)
	if detMap["exitCode"].(int) != -1 {
		t.Errorf("expected exitCode -1, got %v", detMap["exitCode"])
	}

	if path, ok := detMap["tempFilePath"].(string); ok {
		os.Remove(path)
	}
}

func TestBashTool_NonZeroExit(t *testing.T) {
	osExec := OSShellExecutor{}
	tool := NewBashTool(osExec)
	args := map[string]any{
		"command": "exit 3",
	}

	_, details, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("expected error from non-zero exit command, got nil")
	}

	detMap := details.(map[string]any)
	if detMap["exitCode"].(int) != 3 {
		t.Errorf("expected exitCode 3, got %d", detMap["exitCode"])
	}

	if path, ok := detMap["tempFilePath"].(string); ok {
		os.Remove(path)
	}
}

func TestBashTool_OSBasic(t *testing.T) {
	osExec := OSShellExecutor{}
	tool := NewBashTool(osExec)

	args := map[string]any{
		"command": "echo test_bash_output",
	}

	content, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}

	txtBlock := content[0].(ai.TextContent)
	if !strings.Contains(txtBlock.Text, "test_bash_output") {
		t.Errorf("expected command output to contain 'test_bash_output', got %q", txtBlock.Text)
	}

	detMap := details.(map[string]any)
	if detMap["exitCode"].(int) != 0 {
		t.Errorf("expected exitCode 0, got %d", detMap["exitCode"])
	}

	if path, ok := detMap["tempFilePath"].(string); ok {
		os.Remove(path)
	}
}

func TestBashTool_RollingTail(t *testing.T) {
	mockExec := &MockShellExecutor{
		outputs: []string{strings.Repeat("a", 60*1024)}, // 60 KiB
		err:     nil,
	}
	tool := NewBashTool(mockExec)
	args := map[string]any{"command": "large_output"}
	content, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock := content[0].(ai.TextContent)
	detMap := details.(map[string]any)
	if detMap["truncated"].(bool) != true {
		t.Errorf("expected truncated to be true")
	}
	expectedSuffix := "[Output truncated. Full output saved to"
	if !strings.Contains(txtBlock.Text, expectedSuffix) {
		t.Errorf("expected output to contain truncation message, got: %q", txtBlock.Text)
	}
	if len(txtBlock.Text) < 50*1024 {
		t.Errorf("expected output to be at least 50 KiB, got %d", len(txtBlock.Text))
	}
	maxAllowedLen := 50*1024 + 200
	if len(txtBlock.Text) > maxAllowedLen {
		t.Errorf("expected output length to be bounded to tail + notice (< %d), got %d", maxAllowedLen, len(txtBlock.Text))
	}
	tempPath := detMap["tempFilePath"].(string)
	defer os.Remove(tempPath)
	spooled, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatalf("failed to read spool file: %v", err)
	}
	if len(spooled) != 60*1024 {
		t.Errorf("expected spool file to contain full 60 KiB output, got %d bytes", len(spooled))
	}
}

func TestBashTool_OSCancellationProcessKill(t *testing.T) {
	osExec := OSShellExecutor{}
	tool := NewBashTool(osExec)
	traceFile, err := os.CreateTemp("", "pi-bash-kill-trace-*.txt")
	if err != nil {
		t.Fatalf("failed to create trace file: %v", err)
	}
	traceFilePath := traceFile.Name()
	traceFile.Close()
	defer os.Remove(traceFilePath)
	ctx, cancel := context.WithCancel(context.Background())
	cmdStr := fmt.Sprintf("sh -c 'while true; do echo tick >> %q; sleep 0.1; done'", traceFilePath)
	if runtime.GOOS == "windows" {
		cmdStr = fmt.Sprintf("cmd.exe /v:on /c \"for /L %%%%i in (1,0,2) do (echo tick >> %q & timeout /t 1 >nul)\"", traceFilePath)
	}
	args := map[string]any{
		"command": cmdStr,
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	_, details, _, err := tool.Execute(ctx, args)
	if err == nil {
		t.Fatal("expected error from cancelled command, got nil")
	}
	if detMap, ok := details.(map[string]any); ok {
		if path, ok := detMap["tempFilePath"].(string); ok {
			os.Remove(path)
		}
	}
	fi1, err := os.Stat(traceFilePath)
	if err != nil {
		t.Fatalf("failed to stat trace file: %v", err)
	}
	size1 := fi1.Size()
	time.Sleep(500 * time.Millisecond)
	fi2, err := os.Stat(traceFilePath)
	if err != nil {
		t.Fatalf("failed to stat trace file second time: %v", err)
	}
	size2 := fi2.Size()
	if size2 > size1 {
		t.Errorf("process group leak detected: file size increased from %d to %d after cancellation", size1, size2)
	}
}

type MockStderrShellExecutor struct {
	stdoutStr string
	stderrStr string
}

func (m *MockStderrShellExecutor) RunCommand(ctx context.Context, cmdStr string, stdout io.Writer, stderr io.Writer) error {
	_, _ = stdout.Write([]byte(m.stdoutStr))
	_, _ = stderr.Write([]byte(m.stderrStr))
	return nil
}

func TestBashTool_StderrMerge(t *testing.T) {
	mockExec := &MockStderrShellExecutor{
		stdoutStr: "stdout message\n",
		stderrStr: "stderr message\n",
	}
	tool := NewBashTool(mockExec)
	args := map[string]any{"command": "merge"}
	content, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock := content[0].(ai.TextContent)
	if !strings.Contains(txtBlock.Text, "stdout message") || !strings.Contains(txtBlock.Text, "stderr message") {
		t.Errorf("expected merged output, got %q", txtBlock.Text)
	}
	detMap := details.(map[string]any)
	tempPath := detMap["tempFilePath"].(string)
	defer os.Remove(tempPath)
	spooled, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatalf("failed to read spool file: %v", err)
	}
	if !strings.Contains(string(spooled), "stdout message") || !strings.Contains(string(spooled), "stderr message") {
		t.Errorf("expected merged output in spool file, got %q", string(spooled))
	}
}

package tools

import (
	"context"
	"strings"
	"testing"

	"pi-go/pkg/ai"
)

func TestReadTool_Basic(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("line 1\nline 2\nline 3\n"), 0o644)

	tool := NewReadTool(fs)

	// Test full read
	args := map[string]any{"path": "test.txt"}
	content, details, term, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term {
		t.Errorf("expected terminate to be false")
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 result block, got %d", len(content))
	}
	txtBlock, ok := content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", content[0])
	}
	if txtBlock.Text != "line 1\nline 2\nline 3\n" {
		t.Errorf("unexpected content: %q", txtBlock.Text)
	}

	detMap, ok := details.(map[string]any)
	if !ok {
		t.Fatalf("expected details to be map")
	}
	if detMap["linesRead"].(int) != 3 || detMap["truncated"].(bool) != false {
		t.Errorf("unexpected details: %+v", details)
	}
}

func TestReadTool_OffsetAndLimit(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("line 1\nline 2\nline 3\nline 4\nline 5"), 0o644)

	tool := NewReadTool(fs)

	// Read from offset 2, limit 2
	args := map[string]any{
		"path":   "test.txt",
		"offset": float64(2),
		"limit":  float64(2),
	}
	content, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock := content[0].(ai.TextContent)
	if txtBlock.Text != "line 2\nline 3\n" {
		t.Errorf("unexpected content: %q", txtBlock.Text)
	}

	detMap := details.(map[string]any)
	if detMap["linesRead"].(int) != 2 || detMap["truncated"].(bool) != true {
		t.Errorf("unexpected details: %+v", details)
	}
}

func TestReadTool_TruncationAndPartialLines(t *testing.T) {
	fs := NewMemFileSystem()

	// 1. Size-based truncation: total size exceeds 50 KiB
	// We want to verify that we do not output partial lines.
	// Let's create lines of 20 KiB each.
	line1 := strings.Repeat("a", 20*1024) + "\n" // 20KB + 1B
	line2 := strings.Repeat("b", 20*1024) + "\n" // 20KB + 1B
	line3 := strings.Repeat("c", 20*1024) + "\n" // 20KB + 1B
	fs.AddFile("large.txt", []byte(line1+line2+line3), 0o644)

	tool := NewReadTool(fs)
	args := map[string]any{"path": "large.txt"}
	content, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock := content[0].(ai.TextContent)
	// Output should contain line1 and line2 (40KB + 2B), but NOT line3 because adding line3 would be > 50KB.
	expected := line1 + line2
	if txtBlock.Text != expected {
		t.Errorf("expected output to be exactly line 1 and 2, length %d, got %d", len(expected), len(txtBlock.Text))
	}
	detMap := details.(map[string]any)
	if !detMap["truncated"].(bool) {
		t.Errorf("expected truncated to be true")
	}

	// 2. Single line truncation: if the first line itself is > 50 KiB
	hugeLine := strings.Repeat("x", 60*1024) + "\n"
	fs.AddFile("huge_single.txt", []byte(hugeLine), 0o644)
	args = map[string]any{"path": "huge_single.txt"}
	content, details, _, err = tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock = content[0].(ai.TextContent)
	if len(txtBlock.Text) != 50*1024 {
		t.Errorf("expected single huge line to be truncated to exactly 50 KiB, got %d bytes", len(txtBlock.Text))
	}
	if !strings.HasPrefix(txtBlock.Text, "x") {
		t.Errorf("invalid prefix")
	}
}

func TestReadTool_Directory(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddDir("src")
	fs.AddFile("src/main.go", []byte("package main"), 0o644)
	fs.AddFile("src/utils.go", []byte("package main"), 0o644)

	tool := NewReadTool(fs)
	args := map[string]any{"path": "src"}
	content, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txtBlock := content[0].(ai.TextContent)
	if !strings.Contains(txtBlock.Text, "Directory listing for src") {
		t.Errorf("expected directory listing header, got %q", txtBlock.Text)
	}
	if !strings.Contains(txtBlock.Text, "main.go") || !strings.Contains(txtBlock.Text, "utils.go") {
		t.Errorf("missing file entries in directory listing: %s", txtBlock.Text)
	}
}

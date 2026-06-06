package tools

import (
	"context"
	"testing"
)

func TestWriteTool_Basic(t *testing.T) {
	fs := NewMemFileSystem()
	tool := NewWriteTool(fs)

	args := map[string]any{
		"path":    "subdir/newfile.txt",
		"content": "hello world",
	}

	_, details, term, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term {
		t.Errorf("expected terminate to be false")
	}

	// Verify the directories created
	if len(fs.dirsCreated) != 1 || fs.dirsCreated[0] != "subdir" {
		t.Errorf("expected 'subdir' directory to be created, got %v", fs.dirsCreated)
	}

	// Verify the file was written
	data, err := fs.ReadFile("subdir/newfile.txt")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected content 'hello world', got %q", string(data))
	}

	// Verify details
	detMap, ok := details.(map[string]any)
	if !ok {
		t.Fatalf("expected details to be map")
	}
	if detMap["path"].(string) != "subdir/newfile.txt" || detMap["bytes"].(int) != 11 {
		t.Errorf("unexpected details: %+v", details)
	}
}

func TestWriteTool_InvalidParams(t *testing.T) {
	fs := NewMemFileSystem()
	tool := NewWriteTool(fs)

	// Missing path
	args := map[string]any{
		"content": "hello",
	}
	_, _, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Errorf("expected error for missing path")
	}

	// Missing content
	args = map[string]any{
		"path": "test.txt",
	}
	_, _, _, err = tool.Execute(context.Background(), args)
	if err == nil {
		t.Errorf("expected error for missing content")
	}
}

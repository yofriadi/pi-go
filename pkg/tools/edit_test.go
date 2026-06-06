package tools

import (
	"context"
	"strings"
	"testing"
)

func TestEditTool_BasicReplace(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("src/main.go", []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644)

	tool := NewEditTool(fs)

	// Test single edit using normal params
	args := map[string]any{
		"path": "src/main.go",
		"edits": []any{
			map[string]any{
				"oldText": "println(\"hello\")",
				"newText": "println(\"hello world\")",
			},
		},
	}

	_, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file content was updated
	data, _ := fs.ReadFile("src/main.go")
	expected := "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}\n"
	if string(data) != expected {
		t.Errorf("unexpected content:\nexpected: %q\ngot:      %q", expected, string(data))
	}

	// Verify details
	det := details.(map[string]any)
	if det["path"].(string) != "src/main.go" {
		t.Errorf("unexpected path in details: %s", det["path"])
	}
	diff := det["diff"].(string)
	if diff == "" {
		t.Errorf("expected non-empty diff")
	}
}

func TestEditTool_LegacyParams(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("hello apple pie\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "apple",
		"newText": "peach",
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	if string(data) != "hello peach pie\n" {
		t.Errorf("unexpected edited content: %q", string(data))
	}
}

func TestEditTool_MultipleEditsAndOverlaps(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("one\ntwo\nthree\n"), 0o644)

	tool := NewEditTool(fs)

	// Multiple non-overlapping edits
	args := map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{"oldText": "one", "newText": "1"},
			map[string]any{"oldText": "three", "newText": "3"},
		},
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	if string(data) != "1\ntwo\n3\n" {
		t.Errorf("unexpected content: %q", string(data))
	}

	// Reset content
	fs.AddFile("test.txt", []byte("one\ntwo\nthree\n"), 0o644)

	// Overlapping edits
	argsOverlapping := map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{"oldText": "one\ntwo", "newText": "1\n2"},
			map[string]any{"oldText": "two\nthree", "newText": "2\n3"},
		},
	}

	_, _, _, err = tool.Execute(context.Background(), argsOverlapping)
	if err == nil {
		t.Errorf("expected error for overlapping edits, got nil")
	}
}

func TestEditTool_Ambiguity(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("apple\nbanana\napple\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "apple",
		"newText": "orange",
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Errorf("expected error for ambiguous replacement matching multiple locations")
	}
}

func TestEditTool_BOMPreservation(t *testing.T) {
	fs := NewMemFileSystem()
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := []byte("hello world\n")
	fs.AddFile("test.txt", append(bom, content...), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "world",
		"newText": "friend",
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	// Verify that the BOM is still present at the beginning
	if len(data) < 3 || data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Errorf("BOM was not preserved: %v", data[:3])
	}
	// Verify content was updated
	if string(data[3:]) != "hello friend\n" {
		t.Errorf("unexpected content: %q", string(data[3:]))
	}
}

func TestEditTool_LineEndingPreservation(t *testing.T) {
	fs := NewMemFileSystem()
	// Dominantly CRLF
	fs.AddFile("crlf.txt", []byte("line 1\r\nline 2\r\nline 3\r\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "crlf.txt",
		"oldText": "line 2",
		"newText": "line 2 modified\nand line 2.5", // incoming uses \n
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("crlf.txt")
	expected := "line 1\r\nline 2 modified\r\nand line 2.5\r\nline 3\r\n"
	if string(data) != expected {
		t.Errorf("line endings were not preserved correctly:\nexpected: %q\ngot:      %q", expected, string(data))
	}
}

func TestEditTool_FirstChangedLineCRLF(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("crlf.txt", []byte("line 1\r\nline 2\r\nline 3\r\nline 4\r\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "crlf.txt",
		"oldText": "line 3",
		"newText": "line 3 updated",
	}

	_, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	det := details.(map[string]any)
	firstLine := det["firstChangedLine"].(*int)
	if *firstLine != 3 {
		t.Errorf("expected first changed line to be 3, got %d", *firstLine)
	}
}

func TestEditTool_UnicodeFuzzy(t *testing.T) {
	fs := NewMemFileSystem()
	// smart quotes, em-dash, non-breaking space
	original := "The quote is \u201Chashline\u201D \u2014 it\u2019s a smart one\u00A0space."
	fs.AddFile("unicode.txt", []byte(original), 0o644)

	tool := NewEditTool(fs)
	// We search with ASCII double quotes, hyphen/dash, single quote, normal space
	args := map[string]any{
		"path":    "unicode.txt",
		"oldText": `quote is "hashline" - it's a smart one space`,
		"newText": `quote is updated`,
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected fuzzy matching error: %v", err)
	}

	data, _ := fs.ReadFile("unicode.txt")
	expected := "The quote is updated."
	if string(data) != expected {
		t.Errorf("unexpected edited content: %q", string(data))
	}
}

func TestEditTool_DiffAndLineNumbers(t *testing.T) {
	fs := NewMemFileSystem()
	original := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	fs.AddFile("test.txt", []byte(original), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "line 3",
		"newText": "line 3 modified\nline 3.5",
	}

	_, details, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	det := details.(map[string]any)
	diff := det["diff"].(string)
	firstLine := det["firstChangedLine"].(*int)

	if *firstLine != 3 {
		t.Errorf("expected first changed line to be 3, got %d", *firstLine)
	}

	// Verify diff structure
	if !strings.Contains(diff, "@@ -1,5 +1,6 @@") {
		t.Errorf("diff did not contain expected hunk header: %q", diff)
	}
	if !strings.Contains(diff, "-line 3") || !strings.Contains(diff, "+line 3 modified") || !strings.Contains(diff, "+line 3.5") {
		t.Errorf("diff missing change lines: %q", diff)
	}
}

func TestEditTool_JSONStringEdits(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("banana split\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":  "test.txt",
		"edits": `[{"oldText": "banana", "newText": "cherry"}]`,
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	if string(data) != "cherry split\n" {
		t.Errorf("unexpected edited content: %q", string(data))
	}
}

func TestEditTool_JSONStringEditsValidation(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("banana split\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":  "test.txt",
		"edits": `[{"oldText":"banana"}]`,
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for malformed JSON-string edits, got nil")
	}
	if !strings.Contains(err.Error(), "must contain both 'oldText' and 'newText'") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEditTool_NonIncrementalMatching(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("apple\n"), 0o644)

	tool := NewEditTool(fs)
	// Edit 1: apple -> banana.
	// Edit 2: banana -> cherry (relies on Edit 1's outcome).
	// Since matching is performed against the original file, banana is not in the original file,
	// so this edit sequence MUST fail.
	args := map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{"oldText": "apple", "newText": "banana"},
			map[string]any{"oldText": "banana", "newText": "cherry"},
		},
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected non-incremental matching to fail because 'banana' is not in original content")
	}
	if !strings.Contains(err.Error(), "text block not found") {
		t.Errorf("expected text not found error, got: %v", err)
	}
}

func TestEditTool_UnicodeNFCEquivalence(t *testing.T) {
	fs := NewMemFileSystem()
	// Decomposed form: e + \u0301 (2 runes, 3 bytes)
	fs.AddFile("test.txt", []byte("cafe\u0301\n"), 0o644)

	tool := NewEditTool(fs)
	// We search using the composed form: é (1 rune, 2 bytes)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "café",
		"newText": "tea",
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	if string(data) != "tea\n" {
		t.Errorf("unexpected edited content: %q", string(data))
	}
}

func TestEditTool_EditsValidation(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("some text\n"), 0o644)

	tool := NewEditTool(fs)

	// 1. Missing newText
	args1 := map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{"oldText": "some"},
		},
	}
	_, _, _, err := tool.Execute(context.Background(), args1)
	if err == nil {
		t.Error("expected error for missing newText, got nil")
	}

	// 2. Non-string values
	args2 := map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{"oldText": "some", "newText": 123},
		},
	}
	_, _, _, err = tool.Execute(context.Background(), args2)
	if err == nil {
		t.Error("expected error for non-string newText, got nil")
	}
}

func TestEditTool_TrailingWhitespaceFuzzy(t *testing.T) {
	fs := NewMemFileSystem()
	fs.AddFile("test.txt", []byte("line 1   \nline 2\t\n"), 0o644)

	tool := NewEditTool(fs)
	args := map[string]any{
		"path":    "test.txt",
		"oldText": "line 1\nline 2",
		"newText": "line 1 updated\nline 2 updated",
	}

	_, _, _, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := fs.ReadFile("test.txt")
	if string(data) != "line 1 updated\nline 2 updated\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

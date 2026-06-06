package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"pi-go/pkg/agent"
	"pi-go/pkg/ai"
)

// SingleEdit represents a replacement in a file.
type SingleEdit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// EditTool implements agent.AgentTool to modify files.
type EditTool struct {
	fs FileSystem
}

// NewEditTool creates a new EditTool with the given FileSystem.
func NewEditTool(fs FileSystem) *EditTool {
	return &EditTool{fs: fs}
}

// Definition returns the tool schema definition.
func (t *EditTool) Definition() ai.ToolDefinition {
	return EditToolDefinition
}

// Mode returns the tool's execution mode (parallel).
func (t *EditTool) Mode() agent.ToolExecutionMode {
	return agent.ToolExecutionModeParallel
}

// Execute runs the edit tool.
func (t *EditTool) Execute(ctx context.Context, args map[string]any) ([]ai.ToolResultContent, any, bool, error) {
	pathVal, ok := args["path"].(string)
	if !ok || pathVal == "" {
		return nil, nil, false, fmt.Errorf("missing or invalid 'path' parameter")
	}

	var edits []SingleEdit

	if editsVal, exists := args["edits"]; exists {
		if editsStr, ok := editsVal.(string); ok {
			var rawEdits []map[string]any
			if err := json.Unmarshal([]byte(editsStr), &rawEdits); err != nil {
				return nil, nil, false, fmt.Errorf("failed to parse 'edits' JSON string: %w", err)
			}
			validatedEdits, err := validateEditMaps(rawEdits)
			if err != nil {
				return nil, nil, false, err
			}
			edits = append(edits, validatedEdits...)
		} else if editsSlice, ok := editsVal.([]any); ok {
			rawEdits := make([]map[string]any, 0, len(editsSlice))
			for _, e := range editsSlice {
				eMap, ok := e.(map[string]any)
				if !ok {
					return nil, nil, false, fmt.Errorf("invalid element in 'edits' array")
				}
				rawEdits = append(rawEdits, eMap)
			}
			validatedEdits, err := validateEditMaps(rawEdits)
			if err != nil {
				return nil, nil, false, err
			}
			edits = append(edits, validatedEdits...)
		} else {
			return nil, nil, false, fmt.Errorf("invalid 'edits' parameter type")
		}
	} else {
		oldText, hasOld := args["oldText"].(string)
		newText, hasNew := args["newText"].(string)
		if hasOld && hasNew {
			edits = append(edits, SingleEdit{OldText: oldText, NewText: newText})
		} else {
			return nil, nil, false, fmt.Errorf("missing 'edits' or legacy 'oldText'/'newText' parameters")
		}
	}

	if len(edits) == 0 {
		return []ai.ToolResultContent{ai.TextContent{Text: "No edits provided"}}, map[string]any{
			"path":  pathVal,
			"diff":  "",
			"edits": 0,
		}, false, nil
	}

	fi, err := t.fs.Stat(pathVal)
	if err != nil {
		return nil, nil, false, err
	}
	if fi.IsDir() {
		return nil, nil, false, fmt.Errorf("cannot edit directory: %s", pathVal)
	}

	originalBytes, err := t.fs.ReadFile(pathVal)
	if err != nil {
		return nil, nil, false, err
	}

	hasBOM := false
	original := string(originalBytes)
	if len(original) >= 3 && original[0] == 0xEF && original[1] == 0xBB && original[2] == 0xBF {
		hasBOM = true
		original = original[3:]
	}
	// Do not normalize original here so untouched content stays unchanged
	ending := detectLineEnding(original)
	originalLF := strings.ReplaceAll(original, "\r\n", "\n")
	originalLF = strings.ReplaceAll(originalLF, "\r", "\n")
	contentNorm, mapContent := normalizeWithMap(originalLF)
	type matchedEdit struct {
		start   int
		end     int
		newText string
	}
	var matched []matchedEdit
	for _, e := range edits {
		oldTextNFC := norm.NFC.String(e.OldText)
		newTextNFC := norm.NFC.String(e.NewText)
		targetNorm := normalizeTarget(oldTextNFC)
		matches := findOccurrences(contentNorm, targetNorm)
		if len(matches) == 0 {
			return nil, nil, false, fmt.Errorf("text block not found in file: %q", e.OldText)
		}
		if len(matches) > 1 {
			return nil, nil, false, fmt.Errorf("ambiguous replacement: %q matches %d times in the file", e.OldText, len(matches))
		}
		startOrig := mapContent[matches[0]]
		endOrig := mapContent[matches[0]+len(targetNorm)]
		// Normalize newText for originalLF (will use ending at the end)
		newTextLF := strings.ReplaceAll(newTextNFC, "\r\n", "\n")
		newTextLF = strings.ReplaceAll(newTextLF, "\r", "\n")
		matched = append(matched, matchedEdit{
			start:   startOrig,
			end:     endOrig,
			newText: newTextLF,
		})
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].start < matched[j].start
	})
	for i := 0; i < len(matched)-1; i++ {
		if matched[i].end > matched[i+1].start {
			return nil, nil, false, fmt.Errorf("overlapping/nested edits are not allowed")
		}
	}
	var newContentBuilder strings.Builder
	lastIdx := 0
	for _, m := range matched {
		newContentBuilder.WriteString(originalLF[lastIdx:m.start])
		newContentBuilder.WriteString(m.newText)
		lastIdx = m.end
	}
	newContentBuilder.WriteString(originalLF[lastIdx:])
	modifiedLF := newContentBuilder.String()
	modifiedContent := adjustLineEndings(modifiedLF, ending)
	finalBytes := []byte(modifiedContent)
	if hasBOM {
		finalBytes = append([]byte{0xEF, 0xBB, 0xBF}, finalBytes...)
	}
	dir := filepath.Dir(pathVal)
	if dir != "" && dir != "." {
		if err := t.fs.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, false, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	if err := t.fs.WriteFile(pathVal, finalBytes, fi.Mode()); err != nil {
		return nil, nil, false, err
	}
	// Line starts in originalLF
	var lineStarts []int
	lineStarts = append(lineStarts, 0)
	for i := 0; i < len(originalLF); i++ {
		if originalLF[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	findLineIdx := func(idx int) int {
		for i := 1; i < len(lineStarts); i++ {
			if lineStarts[i] > idx {
				return i - 1
			}
		}
		return len(lineStarts) - 1
	}
	origLines := strings.Split(originalLF, "\n")
	if len(origLines) > 0 && origLines[len(origLines)-1] == "" {
		origLines = origLines[:len(origLines)-1]
	}
	var diffBuilder strings.Builder
	cumulativeLineOffset := 0
	for _, m := range matched {
		L_start := findLineIdx(m.start)
		L_end := findLineIdx(m.end)
		if m.end > m.start && L_end > L_start && m.end == lineStarts[L_end] {
			L_end--
		}
		oldLines := origLines[L_start : L_end+1]
		prefix := originalLF[lineStarts[L_start]:m.start]
		suffix := ""
		if L_end+1 < len(lineStarts) {
			suffix = originalLF[m.end:lineStarts[L_end+1]]
		} else {
			suffix = originalLF[m.end:]
		}
		newBlock := prefix + m.newText + suffix
		newLines := strings.Split(newBlock, "\n")
		if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
			newLines = newLines[:len(newLines)-1]
		}
		ctxBeforeStart := L_start - 3
		if ctxBeforeStart < 0 {
			ctxBeforeStart = 0
		}
		beforeContext := origLines[ctxBeforeStart:L_start]
		ctxAfterEnd := L_end + 4
		if ctxAfterEnd > len(origLines) {
			ctxAfterEnd = len(origLines)
		}
		afterContext := origLines[L_end+1 : ctxAfterEnd]
		oldStartLine := ctxBeforeStart + 1
		oldCount := len(beforeContext) + len(oldLines) + len(afterContext)
		newStartLine := oldStartLine + cumulativeLineOffset
		newCount := len(beforeContext) + len(newLines) + len(afterContext)
		diffBuilder.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStartLine, oldCount, newStartLine, newCount))
		for _, l := range beforeContext {
			diffBuilder.WriteString("  " + l + "\n")
		}
		for _, l := range oldLines {
			diffBuilder.WriteString("-" + l + "\n")
		}
		for _, l := range newLines {
			diffBuilder.WriteString("+" + l + "\n")
		}
		for _, l := range afterContext {
			diffBuilder.WriteString("  " + l + "\n")
		}
		cumulativeLineOffset += len(newLines) - len(oldLines)
	}
	var firstChangedLine *int
	if len(matched) > 0 {
		lineNum := strings.Count(originalLF[:matched[0].start], "\n") + 1
		firstChangedLine = &lineNum
	}

	details := map[string]any{
		"path":             pathVal,
		"diff":             diffBuilder.String(),
		"firstChangedLine": firstChangedLine,
	}

	successMsg := fmt.Sprintf("Successfully edited %s", pathVal)
	return []ai.ToolResultContent{ai.TextContent{Text: successMsg}}, details, false, nil
}

func validateEditMaps(rawEdits []map[string]any) ([]SingleEdit, error) {
	edits := make([]SingleEdit, 0, len(rawEdits))
	for _, eMap := range rawEdits {
		oldTxtVal, hasOld := eMap["oldText"]
		newTxtVal, hasNew := eMap["newText"]
		if !hasOld || !hasNew {
			return nil, fmt.Errorf("edit element must contain both 'oldText' and 'newText'")
		}
		oldTxt, okOld := oldTxtVal.(string)
		newTxt, okNew := newTxtVal.(string)
		if !okOld || !okNew {
			return nil, fmt.Errorf("'oldText' and 'newText' must be strings")
		}
		edits = append(edits, SingleEdit{OldText: oldTxt, NewText: newTxt})
	}
	return edits, nil
}

func normalizeWithMap(s string) (string, []int) {
	nfcForm := norm.NFC.String(s)
	contentNorm := normalizeCompatibilityAndWhitespace(nfcForm)
	mapContent := alignStrings(s, contentNorm)
	return contentNorm, mapContent
}

func normalizeTarget(s string) string {
	nfcForm := norm.NFC.String(s)
	return normalizeCompatibilityAndWhitespace(nfcForm)
}

func normalizeCompatibilityAndWhitespace(s string) string {
	isTrailing := make([]bool, len(s))
	for i := len(s) - 1; i >= 0; i-- {
		ch := s[i]
		if ch == '\n' {
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' {
			if i == len(s)-1 || s[i+1] == '\n' || isTrailing[i+1] {
				isTrailing[i] = true
			}
		}
	}
	var sb strings.Builder
	runes := []rune(s)
	byteIdx := 0
	for _, r := range runes {
		rLen := len(string(r))
		if isTrailing[byteIdx] {
			byteIdx += rLen
			continue
		}
		if r == '\r' && byteIdx+rLen < len(s) && s[byteIdx+rLen] == '\n' {
			byteIdx += rLen
			continue
		}
		normRune := r
		if r == '\r' {
			normRune = '\n'
		}
		nfkcVal := string(normRune)
		if normRune != '\n' {
			nfkcVal = norm.NFKC.String(string(normRune))
		}
		for _, nr := range nfkcVal {
			var rep string
			switch {
			case nr >= 0x2010 && nr <= 0x2015 || nr == 0x2212:
				rep = "-"
			case nr >= 0x2018 && nr <= 0x201B:
				rep = "'"
			case nr >= 0x201C && nr <= 0x201F:
				rep = "\""
			case nr == 0x00A0 || (nr >= 0x2002 && nr <= 0x200A) || nr == 0x202F || nr == 0x205F || nr == 0x3000:
				rep = " "
			case nr == 0x2260:
				rep = "!="
			case nr == 0x00BD:
				rep = "1/2"
			case (nr >= 0x200B && nr <= 0x200D) || nr == 0xFEFF:
				rep = ""
			default:
				rep = string(nr)
			}
			sb.WriteString(rep)
		}
		byteIdx += rLen
	}
	return sb.String()
}

func alignStrings(originalLF, contentNorm string) []int {
	origRunes := []rune(originalLF)
	normRunes := []rune(contentNorm)
	origOffsets := make([]int, len(origRunes)+1)
	curr := 0
	for idx, r := range origRunes {
		origOffsets[idx] = curr
		curr += len(string(r))
	}
	origOffsets[len(origRunes)] = len(originalLF)
	normOffsets := make([]int, len(normRunes)+1)
	curr = 0
	for idx, r := range normRunes {
		normOffsets[idx] = curr
		curr += len(string(r))
	}
	normOffsets[len(normRunes)] = len(contentNorm)
	origRunesIdx := make([]int, len(normRunes)+1)
	pOrig := 0
	pNorm := 0
	for pNorm < len(normRunes) {
		if pOrig >= len(origRunes) {
			origRunesIdx[pNorm] = len(origRunes)
			pNorm++
			continue
		}
		if origRunes[pOrig] == normRunes[pNorm] {
			origRunesIdx[pNorm] = pOrig
			pOrig++
			pNorm++
			continue
		}
		found := false
		bestX, bestY := 0, 0
		minDist := 100
		for x := 0; x <= 5; x++ {
			for y := 0; y <= 5; y++ {
				if x == 0 && y == 0 {
					continue
				}
				if pOrig+x < len(origRunes) && pNorm+y < len(normRunes) {
					if origRunes[pOrig+x] == normRunes[pNorm+y] {
						dist := x + y
						if dist < minDist {
							minDist = dist
							bestX = x
							bestY = y
							found = true
						}
					}
				}
			}
		}
		if found {
			for y := 0; y < bestY; y++ {
				origRunesIdx[pNorm+y] = pOrig
			}
			pOrig += bestX
			pNorm += bestY
		} else {
			origRunesIdx[pNorm] = pOrig
			pOrig++
			pNorm++
		}
	}
	origRunesIdx[len(normRunes)] = len(origRunes)
	byteMap := make([]int, len(contentNorm)+1)
	for idx := 0; idx < len(normRunes); idx++ {
		origRuneIdx := origRunesIdx[idx]
		origByteOffset := origOffsets[origRuneIdx]
		startByte := normOffsets[idx]
		endByte := normOffsets[idx+1]
		for b := startByte; b < endByte; b++ {
			byteMap[b] = origByteOffset
		}
	}
	byteMap[len(contentNorm)] = len(originalLF)
	return byteMap
}

func findOccurrences(contentNorm, targetNorm string) []int {
	if targetNorm == "" {
		return nil
	}
	var indices []int
	start := 0
	for {
		idx := strings.Index(contentNorm[start:], targetNorm)
		if idx == -1 {
			break
		}
		indices = append(indices, start+idx)
		start += idx + 1
	}
	return indices
}

func detectLineEnding(content string) string {
	crlf := strings.Count(content, "\r\n")
	cr := strings.Count(content, "\r") - crlf
	lf := strings.Count(content, "\n") - crlf
	if crlf >= lf && crlf >= cr && crlf > 0 {
		return "\r\n"
	}
	if cr >= lf && cr >= crlf && cr > 0 {
		return "\r"
	}
	return "\n"
}

func adjustLineEndings(text string, ending string) string {
	if ending == "\r\n" {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		return strings.ReplaceAll(text, "\n", "\r\n")
	} else if ending == "\r" {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		return strings.ReplaceAll(text, "\n", "\r")
	}
	return text
}

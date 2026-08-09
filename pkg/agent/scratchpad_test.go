package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScratchpadCreationAndRetrieval(t *testing.T) {
	agentDir := t.TempDir()

	text := "Line 1: Hello\nLine 2: World\nLine 3: Go\nLine 4: ADK\nLine 5: Test"
	entry, err := CreateScratchpad(agentDir, text, "unit_test")
	if err != nil {
		t.Fatalf("CreateScratchpad failed: %v", err)
	}

	if len(entry.ID) != 4 {
		t.Errorf("expected 4-character ID, got %q (len %d)", entry.ID, len(entry.ID))
	}
	if entry.Size != len(text) {
		t.Errorf("expected Size %d, got %d", len(text), entry.Size)
	}
	if entry.CreatedBy != "unit_test" {
		t.Errorf("expected CreatedBy 'unit_test', got %q", entry.CreatedBy)
	}

	// Test full retrieval
	val, err := GetScratchpad(agentDir, entry.ID, nil, nil)
	if err != nil {
		t.Fatalf("GetScratchpad failed: %v", err)
	}
	if val != text {
		t.Errorf("expected %q, got %q", text, val)
	}

	// Test line pagination: skip 1 line, get 2 lines
	skip := 1
	num := 2
	paginated, err := GetScratchpad(agentDir, entry.ID, &skip, &num)
	if err != nil {
		t.Fatalf("GetScratchpad paginated failed: %v", err)
	}
	expected := "Line 2: World\nLine 3: Go"
	if paginated != expected {
		t.Errorf("expected paginated output %q, got %q", expected, paginated)
	}

	// Test missing ID error
	_, err = GetScratchpad(agentDir, "missing", nil, nil)
	if err == nil {
		t.Fatalf("expected error for missing ID, got nil")
	}
	if !strings.Contains(err.Error(), `scratchpad entry "missing" not found`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestScratchpadEvictionCap(t *testing.T) {
	agentDir := t.TempDir()

	var firstID string
	for i := 1; i <= MaxScratchpadEntries+1; i++ {
		entry, err := CreateScratchpad(agentDir, fmt.Sprintf("Entry %d", i), "test")
		if err != nil {
			t.Fatalf("CreateScratchpad failed at %d: %v", i, err)
		}
		if i == 1 {
			firstID = entry.ID
		}
		// Ensure mtime ticks forward for deterministic eviction order
		time.Sleep(1 * time.Millisecond)
	}

	items, count, capVal, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed: %v", err)
	}

	if count != MaxScratchpadEntries {
		t.Errorf("expected %d live entries, got %d", MaxScratchpadEntries, count)
	}
	if capVal != MaxScratchpadEntries {
		t.Errorf("expected cap %d, got %d", MaxScratchpadEntries, capVal)
	}
	if len(items) != MaxScratchpadEntries {
		t.Errorf("expected len %d, got %d", MaxScratchpadEntries, len(items))
	}

	// First entry (oldest mtime) should have been evicted
	_, err = GetScratchpad(agentDir, firstID, nil, nil)
	if err == nil {
		t.Fatalf("expected evicted first ID %q to return error, got nil", firstID)
	}
}

func TestExpandScratchpadMacros(t *testing.T) {
	agentDir := t.TempDir()

	text := "Header Line\nContent Line 1\nContent Line 2\nFooter Line"
	entry, err := CreateScratchpad(agentDir, text, "test")
	if err != nil {
		t.Fatalf("CreateScratchpad failed: %v", err)
	}

	rawArg := fmt.Sprintf("Data: <SCRATCHPAD_DATA id=%q skip_lines=\"1\" num_lines=\"2\" />", entry.ID)
	expanded, err := ExpandScratchpadMacros(agentDir, rawArg)
	if err != nil {
		t.Fatalf("ExpandScratchpadMacros failed: %v", err)
	}

	expected := "Data: Content Line 1\nContent Line 2"
	if expanded != expected {
		t.Errorf("expected %q, got %q", expected, expanded)
	}
}

func TestExecuteTool_ScratchpadMacroAndOutputRedirection(t *testing.T) {
	agentDir := t.TempDir()

	entry, err := CreateScratchpad(agentDir, "input data from scratchpad", "test")
	if err != nil {
		t.Fatalf("CreateScratchpad failed: %v", err)
	}

	toolPath := filepath.Join(agentDir, "echo_tool.sh")
	script := "#!/bin/sh\nread input\necho \"Processed: $input\"\n"
	if err := os.WriteFile(toolPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write tool script: %v", err)
	}

	args := ExecToolArgs{
		Stdin: fmt.Sprintf("<SCRATCHPAD_DATA id=%q />", entry.ID),
	}

	output, err := executeTool(context.Background(), agentDir, "echo_tool.sh", toolPath, args)
	if err != nil {
		t.Fatalf("executeTool failed: %v", err)
	}

	if !strings.Contains(output, "Processed: input data from scratchpad") || !strings.HasPrefix(output, "<STDOUT>") {
		t.Errorf("unexpected executeTool output: %s", output)
	}
}

func TestCreateScratchpad_ConcurrentCreations(t *testing.T) {
	agentDir := t.TempDir()

	const numGoroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := CreateScratchpad(agentDir, fmt.Sprintf("payload from goroutine %d", idx), "concurrent_test")
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent CreateScratchpad failed: %v", err)
	}

	items, count, capVal, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed after concurrent creation: %v", err)
	}

	if count != numGoroutines {
		t.Errorf("expected %d entries, got %d", numGoroutines, count)
	}
	if capVal != MaxScratchpadEntries {
		t.Errorf("expected cap %d, got %d", MaxScratchpadEntries, capVal)
	}
	if len(items) != numGoroutines {
		t.Errorf("expected %d items, got %d", numGoroutines, len(items))
	}
}

func TestSDK_ScratchpadOperations(t *testing.T) {
	wsDir := t.TempDir()
	sdk := NewSDK(wsDir)

	agentID := "test_agent"
	agentDir := sdk.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	// 1. CreateScratchpad via SDK
	text := "Line 1: Hello SDK\nLine 2: Search target\nLine 3: Goodbye SDK\n"
	entry, err := sdk.CreateScratchpad(agentID, text, "cli_test")
	if err != nil {
		t.Fatalf("sdk.CreateScratchpad failed: %v", err)
	}
	if entry.CreatedBy != "cli_test" {
		t.Errorf("expected CreatedBy 'cli_test', got %q", entry.CreatedBy)
	}

	// 2. GetScratchpad via SDK
	readBack, err := sdk.GetScratchpad(agentID, entry.ID, nil, nil)
	if err != nil {
		t.Fatalf("sdk.GetScratchpad failed: %v", err)
	}
	if readBack != text {
		t.Errorf("got %q, expected %q", readBack, text)
	}

	// 3. ListScratchpads via SDK
	items, count, capVal, err := sdk.ListScratchpads(agentID)
	if err != nil {
		t.Fatalf("sdk.ListScratchpads failed: %v", err)
	}
	if count != 1 || capVal != MaxScratchpadEntries || len(items) != 1 {
		t.Errorf("unexpected list output: count %d, cap %d, len %d", count, capVal, len(items))
	}

	// 4. SearchScratchpad via SDK
	searchRes, err := sdk.SearchScratchpad(agentID, entry.ID, "target", nil, false, 10)
	if err != nil {
		t.Fatalf("sdk.SearchScratchpad failed: %v", err)
	}
	if searchRes.TotalMatches != 1 {
		t.Errorf("expected 1 match, got %d", searchRes.TotalMatches)
	}
	if len(searchRes.Matches) > 0 && searchRes.Matches[0].Line != 2 {
		t.Errorf("expected line 2, got %d", searchRes.Matches[0].Line)
	}
}

func TestCreateScratchpad_MacroCombination(t *testing.T) {
	agentDir := t.TempDir()

	e1, err := CreateScratchpad(agentDir, "Part 1 Data", "test")
	if err != nil {
		t.Fatalf("failed to create e1: %v", err)
	}

	e2, err := CreateScratchpad(agentDir, "Part 2 Data", "test")
	if err != nil {
		t.Fatalf("failed to create e2: %v", err)
	}

	combinedPayload := fmt.Sprintf("Header:\n<SCRATCHPAD_DATA id=%q />\n<SCRATCHPAD_DATA id=%q />\nFooter", e1.ID, e2.ID)
	combinedEntry, err := CreateScratchpad(agentDir, combinedPayload, "test_combine")
	if err != nil {
		t.Fatalf("failed to create combined entry: %v", err)
	}

	expectedText := "Header:\nPart 1 Data\nPart 2 Data\nFooter"
	if combinedEntry.Text != expectedText {
		t.Errorf("got combined text %q, expected %q", combinedEntry.Text, expectedText)
	}
}

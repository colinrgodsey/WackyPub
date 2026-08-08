package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
	if entry.Seq != 1 {
		t.Errorf("expected Seq 1, got %d", entry.Seq)
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
	for i := 1; i <= 51; i++ {
		entry, err := CreateScratchpad(agentDir, fmt.Sprintf("Entry %d", i), "test")
		if err != nil {
			t.Fatalf("CreateScratchpad failed at %d: %v", i, err)
		}
		if i == 1 {
			firstID = entry.ID
		}
	}

	items, count, capVal, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed: %v", err)
	}

	if count != 50 {
		t.Errorf("expected 50 live entries, got %d", count)
	}
	if capVal != 50 {
		t.Errorf("expected cap 50, got %d", capVal)
	}
	if items[0].Seq != 2 {
		t.Errorf("expected lowest remaining seq to be 2, got %d", items[0].Seq)
	}

	// First entry (seq 1) should have been evicted
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

	// Verify scratchpad.json parses cleanly and has 20 entries
	items, count, capVal, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed after concurrent creation: %v", err)
	}

	if count != numGoroutines {
		t.Errorf("expected %d entries, got %d", numGoroutines, count)
	}
	if capVal != 50 {
		t.Errorf("expected cap 50, got %d", capVal)
	}
	if len(items) != numGoroutines {
		t.Errorf("expected %d items, got %d", numGoroutines, len(items))
	}
}

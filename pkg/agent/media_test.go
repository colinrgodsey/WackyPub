package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestAddMedia_GatingAndExecution(t *testing.T) {
	var err error
	wsDir := t.TempDir()
	t.Setenv("WACKYPUB_ALLOWED_AGENTS", "*")
	sdk := NewSDK(wsDir)

	agentID := "media_agent"
	agentDir := sdk.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatalf("failed to chdir to agentDir: %v", err)
	}
	defer os.Chdir(origCwd)

	// 1. Without maxImageDimension (or maxImageDimension <= 0) in runtime.json
	runtimeJSONDisabled := `{"model":"test-model","endpoint":"http://localhost:1234/v1"}`
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(runtimeJSONDisabled), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	if err := os.WriteFile(filepath.Join(agentDir, AllowedAgentsFile), []byte("media_agent\n"), 0644); err != nil {
		t.Fatalf("failed to write allowed agents file: %v", err)
	}

	testImgData := createTestImage(800, 600, false)
	_, err = sdk.AddMedia(agentID, bytes.NewReader(testImgData))
	if err == nil {
		t.Fatal("expected error when maxImageDimension is absent/disabled, got nil")
	}

	// 2. With maxImageDimension = 400
	runtimeJSONEnabled := `{"model":"test-model","endpoint":"http://localhost:1234/v1","maxImageDimension":400}`
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(runtimeJSONEnabled), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	var content *genai.Content
	content, err = sdk.AddMedia(agentID, bytes.NewReader(testImgData))
	if err != nil {
		t.Fatalf("AddMedia failed: %v", err)
	}

	if content.Role != "user" || len(content.Parts) != 1 || content.Parts[0].InlineData == nil {
		t.Fatalf("unexpected Content structure: %+v", content)
	}

	blob := content.Parts[0].InlineData
	if blob.MIMEType != "image/jpeg" {
		t.Errorf("expected MIMEType image/jpeg, got %s", blob.MIMEType)
	}

	turns, err := sdk.ReadSession(agentID)
	if err != nil {
		t.Fatalf("ReadSession failed: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn in session.jsonl, got %d", len(turns))
	}

	// Verify EstimateTokens includes image token count
	tokens := EstimateTokens(turns, false)
	if tokens <= 0 {
		t.Errorf("expected non-zero token estimate for image turn, got %d", tokens)
	}
}

func TestBinaryScratchpad_DetectionAndRejection(t *testing.T) {
	agentDir := t.TempDir()

	binaryData := []byte{0x00, 0x01, 0x02, 0x03, 'P', 'N', 'G', 0x0d, 0x0a}
	entry, err := CreateBinaryScratchpad(agentDir, binaryData, "test", "image/png")
	if err != nil {
		t.Fatalf("CreateBinaryScratchpad failed: %v", err)
	}

	if !entry.IsBinary {
		t.Errorf("expected entry.IsBinary true, got false")
	}

	items, count, _, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed: %v", err)
	}
	if count != 1 || !items[0].IsBinary {
		t.Errorf("expected 1 binary item in ListScratchpads, got %v", items)
	}

	// GetScratchpad should reject .dat entry outright
	_, err = GetScratchpad(agentDir, entry.ID, nil, nil)
	if err == nil {
		t.Fatal("expected GetScratchpad to reject binary entry, got nil error")
	}

	// SearchScratchpad should reject .dat entry outright
	_, err = SearchScratchpad(agentDir, entry.ID, "PNG", nil, false, 10)
	if err == nil {
		t.Fatal("expected SearchScratchpad to reject binary entry, got nil error")
	}

	// DeleteScratchpad should succeed
	err = DeleteScratchpad(agentDir, entry.ID)
	if err != nil {
		t.Fatalf("DeleteScratchpad failed: %v", err)
	}

	itemsAfter, countAfter, _, _ := ListScratchpads(agentDir)
	if countAfter != 0 {
		t.Errorf("expected 0 items after deletion, got %d (%v)", countAfter, itemsAfter)
	}
}

func TestExecuteTool_BinaryScratchpadPipingAndRestrictions(t *testing.T) {
	agentDir := t.TempDir()
	toolsDir := filepath.Join(agentDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}

	// Script cat_stdin.sh: copies stdin to stdout
	catScript := "#!/bin/sh\ncat"
	if err := os.WriteFile(filepath.Join(toolsDir, "cat_stdin.sh"), []byte(catScript), 0755); err != nil {
		t.Fatalf("failed to write cat_stdin.sh: %v", err)
	}

	realPNGPayload := createTestImage(20, 20, true)
	// Prepend a control byte to force binary classification if PNG encoder produced text-safe byte prefix
	if !IsBinaryContent(realPNGPayload) {
		realPNGPayload = append([]byte{0x00}, realPNGPayload...)
	}

	spEntry, err := CreateBinaryScratchpad(agentDir, realPNGPayload, "test", "image/png")
	if err != nil {
		t.Fatalf("CreateBinaryScratchpad failed: %v", err)
	}

	ctx := context.Background()

	// 1. Reject binary entry in command args
	badArgs := ExecToolArgs{
		Args: []string{"<SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" />"},
	}
	_, err = executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), badArgs)
	if err == nil {
		t.Fatal("expected error passing binary scratchpad in args, got nil")
	}

	// 2. Reject binary entry mixed with text in stdin
	mixedStdin := ExecToolArgs{
		Stdin: "prefix <SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" />",
	}
	_, err = executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), mixedStdin)
	if err == nil {
		t.Fatal("expected error mixing binary scratchpad with text in stdin, got nil")
	}

	// 3. Exact binary stdin piping -> streams file directly and output gets auto-captured to new .dat scratchpad
	exactStdin := ExecToolArgs{
		Stdin: "<SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" />",
	}
	out, err := executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), exactStdin)
	if err != nil {
		t.Fatalf("exact binary stdin piping failed: %v", err)
	}

	if !strings.Contains(out, "mime=\"image/png\"") && !strings.Contains(out, "mime=\"application/octet-stream\"") {
		t.Errorf("expected stdout to auto-capture to binary scratchpad, got: %s", out)
	}

	// 4. Reject pagination attributes (skip_lines, num_lines, json_escape) on binary stdin references per D48
	skipStdin := ExecToolArgs{
		Stdin: "<SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" skip_lines=\"2\" />",
	}
	_, err = executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), skipStdin)
	if err == nil {
		t.Fatal("expected error using skip_lines on binary scratchpad in stdin, got nil")
	}

	numStdin := ExecToolArgs{
		Stdin: "<SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" num_lines=\"5\" />",
	}
	_, err = executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), numStdin)
	if err == nil {
		t.Fatal("expected error using num_lines on binary scratchpad in stdin, got nil")
	}

	jsonStdin := ExecToolArgs{
		Stdin: "<SCRATCHPAD_DATA id=\"" + spEntry.ID + "\" json_escape=\"true\" />",
	}
	_, err = executeTool(ctx, agentDir, "cat_stdin.sh", filepath.Join(toolsDir, "cat_stdin.sh"), jsonStdin)
	if err == nil {
		t.Fatal("expected error using json_escape on binary scratchpad in stdin, got nil")
	}
}

func TestScratchpad_UnifiedIDNamespace(t *testing.T) {
	agentDir := t.TempDir()
	spDir := filepath.Join(agentDir, ScratchpadDirName)
	if err := os.MkdirAll(spDir, 0755); err != nil {
		t.Fatalf("failed to create scratchpad dir: %v", err)
	}

	// Create 10 text and 10 binary entries, verify all 20 have unique IDs
	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		txtEntry, err := CreateScratchpad(agentDir, "text payload", "test")
		if err != nil {
			t.Fatalf("CreateScratchpad failed: %v", err)
		}
		if ids[txtEntry.ID] {
			t.Fatalf("duplicate ID generated: %q", txtEntry.ID)
		}
		ids[txtEntry.ID] = true

		binEntry, err := CreateBinaryScratchpad(agentDir, []byte{0x00, byte(i)}, "test", "application/octet-stream")
		if err != nil {
			t.Fatalf("CreateBinaryScratchpad failed: %v", err)
		}
		if ids[binEntry.ID] {
			t.Fatalf("duplicate ID generated across text/binary namespace: %q", binEntry.ID)
		}
		ids[binEntry.ID] = true
	}

	// Simulate collision recovery: if a .txt and .dat happen to share ID "abcd", DeleteScratchpad cleans all
	const sharedID = "abcd"
	if err := os.WriteFile(filepath.Join(spDir, sharedID+"-1-agentA.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write .txt collision file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spDir, sharedID+"-0-agentB.dat"), []byte{0x00, 0x01}, 0644); err != nil {
		t.Fatalf("failed to write .dat collision file: %v", err)
	}

	if err := DeleteScratchpad(agentDir, sharedID); err != nil {
		t.Fatalf("DeleteScratchpad failed: %v", err)
	}

	// Verify both were removed and neither was orphaned
	items, _, _, err := ListScratchpads(agentDir)
	if err != nil {
		t.Fatalf("ListScratchpads failed: %v", err)
	}
	for _, it := range items {
		if it.ID == sharedID {
			t.Fatalf("found orphaned entry with ID %q after DeleteScratchpad", sharedID)
		}
	}
}

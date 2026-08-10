package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseA2AMetadata_FallbackAndParsing(t *testing.T) {
	origA2A := os.Getenv(Agent2AgentEnvVar)
	origChain := os.Getenv(CallChainEnvVar)
	defer func() {
		os.Setenv(Agent2AgentEnvVar, origA2A)
		os.Setenv(CallChainEnvVar, origChain)
	}()

	t.Run("empty environment fallback to legacy WACKYPUB_CALL_CHAIN", func(t *testing.T) {
		os.Setenv(Agent2AgentEnvVar, "")
		os.Setenv(CallChainEnvVar, "bob,jax")

		meta, err := ParseA2AMetadata()
		if err != nil {
			t.Fatalf("ParseA2AMetadata failed: %v", err)
		}

		if meta.CallerID != "jax" {
			t.Errorf("expected CallerID 'jax', got %q", meta.CallerID)
		}
		if len(meta.CallChain) != 2 || meta.CallChain[0] != "bob" || meta.CallChain[1] != "jax" {
			t.Errorf("unexpected call chain: %v", meta.CallChain)
		}
		if !strings.HasPrefix(meta.TraceID, "a2a-") {
			t.Errorf("expected traceID starting with 'a2a-', got %q", meta.TraceID)
		}
	})

	t.Run("parsing AGENT2AGENT dense JSON", func(t *testing.T) {
		payload := `{"caller_id":"alice","call_chain":["bob","alice"],"trace_id":"a2a-test1234"}`
		os.Setenv(Agent2AgentEnvVar, payload)

		meta, err := ParseA2AMetadata()
		if err != nil {
			t.Fatalf("ParseA2AMetadata failed: %v", err)
		}

		if meta.CallerID != "alice" {
			t.Errorf("expected CallerID 'alice', got %q", meta.CallerID)
		}
		if len(meta.CallChain) != 2 || meta.CallChain[1] != "alice" {
			t.Errorf("unexpected CallChain: %v", meta.CallChain)
		}
		if meta.TraceID != "a2a-test1234" {
			t.Errorf("expected TraceID 'a2a-test1234', got %q", meta.TraceID)
		}
	})
}

func TestValidateAgentTarget_A2AMetadataPropagation(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "bob")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, AllowedAgentsFile), []byte("jax\n"), 0644); err != nil {
		t.Fatalf("failed to write allowed agents: %v", err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatalf("failed to chdir to agentDir: %v", err)
	}
	defer os.Chdir(origCwd)

	origA2A := os.Getenv(Agent2AgentEnvVar)
	origChain := os.Getenv(CallChainEnvVar)
	defer func() {
		os.Setenv(Agent2AgentEnvVar, origA2A)
		os.Setenv(CallChainEnvVar, origChain)
	}()

	os.Setenv(Agent2AgentEnvVar, `{"caller_id":"bob","call_chain":["bob"],"trace_id":"a2a-flow1"}`)

	// Target 'jax' should succeed
	cleanup, err := ValidateAgentTarget("jax")
	if err != nil {
		t.Fatalf("ValidateAgentTarget failed: %v", err)
	}

	// Verify AGENT2AGENT env var during call
	activeA2A := os.Getenv(Agent2AgentEnvVar)
	var activeMeta A2AMetadata
	if err := json.Unmarshal([]byte(activeA2A), &activeMeta); err != nil {
		t.Fatalf("failed to unmarshal active AGENT2AGENT env: %v", err)
	}

	if activeMeta.CallerID != "bob" {
		t.Errorf("expected CallerID 'bob', got %q", activeMeta.CallerID)
	}
	if len(activeMeta.CallChain) != 2 || activeMeta.CallChain[0] != "bob" || activeMeta.CallChain[1] != "jax" {
		t.Errorf("unexpected active CallChain: %v", activeMeta.CallChain)
	}
	if activeMeta.TraceID != "a2a-flow1" {
		t.Errorf("expected TraceID 'a2a-flow1', got %q", activeMeta.TraceID)
	}

	// Legacy WACKYPUB_CALL_CHAIN should also be updated
	if os.Getenv(CallChainEnvVar) != "bob,jax" {
		t.Errorf("expected WACKYPUB_CALL_CHAIN 'bob,jax', got %q", os.Getenv(CallChainEnvVar))
	}

	cleanup()

	// Verify environment restored after cleanup
	if os.Getenv(CallChainEnvVar) != "" {
		t.Errorf("expected empty CallChainEnvVar after cleanup, got %q", os.Getenv(CallChainEnvVar))
	}
}

func TestValidateAgentTarget_A2ACycleRejection(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "jax")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, AllowedAgentsFile), []byte("bob\n"), 0644); err != nil {
		t.Fatalf("failed to write allowed agents: %v", err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatalf("failed to chdir to agentDir: %v", err)
	}
	defer os.Chdir(origCwd)

	origA2A := os.Getenv(Agent2AgentEnvVar)
	origChain := os.Getenv(CallChainEnvVar)
	defer func() {
		os.Setenv(Agent2AgentEnvVar, origA2A)
		os.Setenv(CallChainEnvVar, origChain)
	}()

	os.Setenv(Agent2AgentEnvVar, `{"caller_id":"jax","call_chain":["bob","jax"],"trace_id":"a2a-cycle"}`)

	// Target 'bob' should fail because 'bob' is already in call_chain
	_, err = ValidateAgentTarget("bob")
	if err == nil {
		t.Fatalf("expected error for cycle targeting 'bob', got nil")
	}
	if !strings.Contains(err.Error(), "already in call chain") {
		t.Errorf("unexpected error message: %v", err)
	}
}

package agent

import (
	"net/http"
	"strings"
	"time"

	adkopenai "github.com/achetronic/adk-utils-go/genai/openai"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// NewOpenAIModel instantiates an OpenAI-compatible model adapter for ADK,
// backed by the official OpenAI Go SDK via achetronic/adk-utils-go. Works
// against OpenAI itself and OpenAI-compatible providers (Ollama, vLLM,
// llama.cpp, DeepSeek, OpenRouter, etc.), including ones that emit the
// non-standard reasoning_content field for chain-of-thought, which the
// adapter surfaces as a Thought-marked genai.Part.
//
// go.mod currently replaces achetronic/adk-utils-go with
// github.com/colinrgodsey/adk-utils-go (master, currently commit ee4a5294,
// "fix: treat `reasoning_content` appropriately" — this commit hash
// supersedes earlier ones referenced in prior history/commit messages here,
// since the fork's master was squashed/rewritten upstream). The fork
// re-emits Thought parts as a proper reasoning_content field on egress
// instead of merging them into plain "content" — required by DeepSeek V4
// thinking mode and Kimi K2 Thinking, which 400 without it — preserves
// OpenRouter's structured reasoning_details blocks (needed for
// encrypted/signed reasoning), fixes streamed turns losing reasoning on the
// terminal response, and adds Config.ExtraBody for provider-specific request
// extensions Chat Completions doesn't define (e.g. OpenRouter's
// `{"reasoning": {"effort": "high"}}` to request extended thinking from
// models like Claude that don't emit it by default). See
// ADK_UTILS_GO_REASONING_EGRESS_BUG.md at the repo root for the original bug
// writeup. Once the fix lands upstream and is tagged, drop the replace
// directive and go back to depending on achetronic/adk-utils-go directly.
func NewOpenAIModel(runtimeCfg *RuntimeConfig) model.LLM {
	effort := runtimeCfg.ReasoningEffort
	if effort == "" {
		effort = runtimeCfg.ThinkingEffort
	}

	extraBody := runtimeCfg.ExtraBody
	if effort != "" {
		if extraBody == nil {
			extraBody = make(map[string]any)
		} else {
			clone := make(map[string]any, len(extraBody)+1)
			for k, v := range extraBody {
				clone[k] = v
			}
			extraBody = clone
		}
		isOpenRouter := strings.Contains(strings.ToLower(runtimeCfg.Endpoint), "openrouter.ai")
		if isOpenRouter {
			if _, ok := extraBody["reasoning"]; !ok {
				extraBody["reasoning"] = map[string]any{"effort": effort}
			}
		} else {
			if _, ok := extraBody["reasoning_effort"]; !ok {
				extraBody["reasoning_effort"] = effort
			}
		}
	}

	return adkopenai.New(adkopenai.Config{
		APIKey:    runtimeCfg.APIKey,
		BaseURL:   strings.TrimSuffix(runtimeCfg.Endpoint, "/"),
		ModelName: runtimeCfg.Model,
		HTTPOptions: adkopenai.HTTPOptions{
			Client: &http.Client{Timeout: 120 * time.Second},
		},
		ReasoningEgress:          adkopenai.ReasoningEgressMode(runtimeCfg.ReasoningEgress),
		ReasoningField:           runtimeCfg.ReasoningField,
		SupportsReasoningDetails: runtimeCfg.SupportsReasoningDetails,
		ExtraBody:                extraBody,
	})
}

// StripReasoningDetails returns a copy of c with adk-utils-go's OpenRouter
// reasoning_details block metadata removed from every part.
//
// Ingest captures reasoning_details blocks unconditionally, regardless of
// SupportsReasoningDetails — so a block (including an opaque encrypted one
// from e.g. an OpenAI model routed through OpenRouter) ends up in
// session.jsonl even when the runtime config has egress disabled. Left in
// place, it's dead weight at best; at worst, it becomes a stale, unreplayable
// blob if SupportsReasoningDetails is later toggled back on for the same
// session but the routed endpoint has changed (see
// ADK_UTILS_GO_REASONING_EGRESS_BUG.md for the underlying encrypted-payload
// endpoint-pinning issue). Callers should apply this before persisting a
// turn when RuntimeConfig.SupportsReasoningDetails is false.
//
// A thought Part that carries nothing but a block (no readable text) is
// dropped entirely once its metadata is stripped, since nothing would remain
// to preserve.
func StripReasoningDetails(c *genai.Content) *genai.Content {
	if c == nil || !contentHasReasoningDetails(c) {
		return c
	}

	parts := make([]*genai.Part, 0, len(c.Parts))
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if !partHasReasoningDetail(p) {
			parts = append(parts, p)
			continue
		}

		clone := *p
		clone.PartMetadata = nil
		for k, v := range p.PartMetadata {
			if k == adkopenai.ReasoningDetailMetadataKey {
				continue
			}
			if clone.PartMetadata == nil {
				clone.PartMetadata = make(map[string]any, len(p.PartMetadata)-1)
			}
			clone.PartMetadata[k] = v
		}

		if clone.Text == "" && clone.PartMetadata == nil {
			continue // nothing left worth keeping
		}
		parts = append(parts, &clone)
	}
	return &genai.Content{Role: c.Role, Parts: parts}
}

// partHasReasoningDetail reports whether p carries adk-utils-go's OpenRouter
// reasoning_details block metadata.
func partHasReasoningDetail(p *genai.Part) bool {
	if p == nil || p.PartMetadata == nil {
		return false
	}
	_, ok := p.PartMetadata[adkopenai.ReasoningDetailMetadataKey]
	return ok
}

// contentHasReasoningDetails reports whether any part of c carries
// reasoning_details block metadata.
func contentHasReasoningDetails(c *genai.Content) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Parts {
		if partHasReasoningDetail(p) {
			return true
		}
	}
	return false
}

// StripSessionReasoningDetails rewrites <agentDir>/session.jsonl in place,
// removing OpenRouter reasoning_details block metadata (including
// encrypted/signed reasoning tied to a specific backend endpoint — see
// StripReasoningDetails) from every turn. Readable plain-text reasoning
// (Thought parts with text) is left untouched.
//
// Useful when permanently moving an agent off a model/endpoint that emitted
// encrypted reasoning blocks: those blocks would otherwise sit in
// session.jsonl as a landmine if SupportsReasoningDetails is ever turned back
// on for a different backend that can't decrypt them (see
// ADK_UTILS_GO_REASONING_EGRESS_BUG.md).
//
// Returns the number of turns that were actually modified. Does not acquire
// the session lock; callers must hold it (see AgentSDK.StripReasoningDetails).
func StripSessionReasoningDetails(agentDir string) (int, error) {
	turns, err := ReadSessionTurns(agentDir)
	if err != nil {
		return 0, err
	}

	modified := 0
	stripped := make([]*genai.Content, len(turns))
	for i, t := range turns {
		if contentHasReasoningDetails(t) {
			modified++
		}
		stripped[i] = StripReasoningDetails(t)
	}

	if modified == 0 {
		return 0, nil
	}

	if err := WriteSessionTurns(agentDir, stripped); err != nil {
		return 0, err
	}
	return modified, nil
}

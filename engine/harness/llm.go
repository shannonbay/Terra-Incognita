package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// LLMClient calls the Claude API and extracts structured actions via tool use.
type LLMClient struct {
	client anthropic.Client
	config HarnessConfig
}

// DecideResult is the parsed output of one LLM decision call.
type DecideResult struct {
	Actions       []Action          // nil if take_actions was not called
	ReasoningText string            // text content blocks before/between tool calls
	FindingText   string            // text passed to record_finding, if called
	LatencyMs     int64             // end-to-end wall time
	RawResponse   *anthropic.Message // the final raw API response
}

// NewLLMClient creates a client using the API key from config.
func NewLLMClient(cfg HarnessConfig) (*LLMClient, error) {
	key := cfg.APIKey()
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set (env var: %s)", cfg.APIKeyEnv)
	}
	return &LLMClient{client: anthropic.NewClient(option.WithAPIKey(key)), config: cfg}, nil
}

// NewLLMClientWithBaseURL creates a client pointing at a custom base URL (for testing).
func NewLLMClientWithBaseURL(cfg HarnessConfig, baseURL string) *LLMClient {
	return &LLMClient{
		client: anthropic.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(baseURL)),
		config: cfg,
	}
}

// Decide calls Claude for one tick and returns the parsed result plus the raw response.
// systemPrompt is the world-provided system prompt.
// messages is the full conversation history including the current user message.
func (c *LLMClient) Decide(ctx context.Context, systemPrompt string, messages []anthropic.MessageParam) (*DecideResult, error) {
	start := time.Now()

	resp, err := c.call(ctx, systemPrompt, messages)
	if err != nil {
		return nil, err
	}

	result := parseResponse(resp)
	result.LatencyMs = time.Since(start).Milliseconds()
	result.RawResponse = resp

	// If take_actions was not called, retry once with a reminder.
	if result.Actions == nil {
		retryMessages := append(messages,
			resp.ToParam(),
			anthropic.NewUserMessage(
				anthropic.NewTextBlock("Please call take_actions to submit your actions for this tick. You may submit an empty list or include do_nothing if you choose not to act."),
			),
		)
		resp2, err2 := c.call(ctx, systemPrompt, retryMessages)
		if err2 == nil {
			retry := parseResponse(resp2)
			// Keep original reasoning text; take retry's actions
			if result.ReasoningText != "" && retry.ReasoningText != "" {
				retry.ReasoningText = result.ReasoningText + "\n" + retry.ReasoningText
			} else if result.ReasoningText != "" {
				retry.ReasoningText = result.ReasoningText
			}
			retry.LatencyMs = time.Since(start).Milliseconds()
			retry.RawResponse = resp2
			// Combine findings
			if result.FindingText != "" && retry.FindingText == "" {
				retry.FindingText = result.FindingText
			}
			return &retry, nil //nolint:returningAddressOfLoopVariable
		}
	}

	return &result, nil
}

func (c *LLMClient) call(ctx context.Context, systemPrompt string, messages []anthropic.MessageParam) (*anthropic.Message, error) {
	params := anthropic.MessageNewParams{
		Model:     c.config.Model,
		MaxTokens: int64(c.config.MaxTokens),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: messages,
		Tools:    buildTools(),
	}
	if c.config.Temperature > 0 {
		params.Temperature = param.NewOpt(c.config.Temperature)
	}
	return c.client.Messages.New(ctx, params)
}

// parseResponse extracts reasoning text, finding text, and actions from a message.
func parseResponse(resp *anthropic.Message) DecideResult {
	var result DecideResult

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if result.ReasoningText != "" {
				result.ReasoningText += "\n" + block.Text
			} else {
				result.ReasoningText = block.Text
			}

		case "tool_use":
			switch block.Name {
			case "record_finding":
				var args struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(block.Input, &args); err == nil {
					result.FindingText = args.Text
				}

			case "take_actions":
				var args struct {
					Actions []struct {
						Name   string         `json:"name"`
						Params map[string]any `json:"params"`
					} `json:"actions"`
				}
				if err := json.Unmarshal(block.Input, &args); err == nil {
					for _, a := range args.Actions {
						result.Actions = append(result.Actions, Action{
							Name:   a.Name,
							Params: a.Params,
						})
					}
					if result.Actions == nil {
						result.Actions = []Action{} // empty but called = intentional
					}
				}
			}
		}
	}

	return result
}

// BuildToolResults creates the tool_result user message blocks for a response.
// Call this after parseResponse to close the conversation turn.
func BuildToolResults(resp *anthropic.Message) []anthropic.ContentBlockParamUnion {
	var results []anthropic.ContentBlockParamUnion
	for _, block := range resp.Content {
		if block.Type != "tool_use" {
			continue
		}
		switch block.Name {
		case "record_finding":
			results = append(results, anthropic.NewToolResultBlock(block.ID, "Note saved.", false))
		case "take_actions":
			results = append(results, anthropic.NewToolResultBlock(block.ID, "Actions queued.", false))
		}
	}
	return results
}

// --- tool definitions ---

func buildTools() []anthropic.ToolUnionParam {
	recordFinding := anthropic.ToolParam{
		Name:        "record_finding",
		Description: param.NewOpt("Save a note, observation, or hypothesis to your persistent notes. These notes are prepended to every future tick. Use this to accumulate knowledge across time."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "The note or observation to retain.",
				},
			},
			Required: []string{"text"},
		},
	}

	takeActions := anthropic.ToolParam{
		Name:        "take_actions",
		Description: param.NewOpt("Submit the actions you want to take this tick. You must call this tool every tick. Pass an empty actions array or include do_nothing if you choose not to act."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"actions": map[string]any{
					"type":        "array",
					"description": "Actions to take this tick.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "Action name.",
							},
							"params": map[string]any{
								"type":        "object",
								"description": "Action parameters.",
							},
						},
						"required": []string{"name"},
					},
				},
			},
			Required: []string{"actions"},
		},
	}

	return []anthropic.ToolUnionParam{
		{OfTool: &recordFinding},
		{OfTool: &takeActions},
	}
}

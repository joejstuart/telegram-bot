// Package agent provides the agentic loop that connects the LLM to tools.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"telegram-bot/tools"
)

const maxToolCalls = 20 // Allow enough iterations for test-fix cycles

const systemPrompt = `You are a helpful AI assistant with access to tools.

TOOLS:
- python: For Python code (simple scripts or code with tests)
- bash: For shell commands and file operations  
- oci: For container registry operations (inspect images, manifests, copy, annotate, etc.)
- scrape: Fetch and summarize web pages
- get_current_time: Get current time
- get_calendar_events: Check calendar

OCI TOOL (for container images):
Use the oci tool for Docker/OCI image operations:
- oci(operation="inspect", image="alpine:latest") - examine image metadata
- oci(operation="manifest", image="ghcr.io/org/app:v1") - get raw manifest
- oci(operation="list-tags", image="docker.io/library/nginx") - list all tags
- oci(operation="copy", source="src:tag", dest="dst:tag") - copy between registries
- oci(operation="annotate", image="myimage:v1", annotations='{"key":"value"}')

PYTHON TOOL OPERATIONS:
1. run: Quick scripts - provide 'code' param, prints result immediately
2. develop: Code with tests - provide name, implementation, tests. Runs tests automatically.

SIMPLE TASKS (use python run):
For "format as JSON", "calculate X":
  python(operation="run", code="import json; print(json.dumps({'key': 'value'}))")
Return the output to user immediately.

CODE WITH TESTS (use python develop):
For proper implementations:
  python(operation="develop", name="mymodule", implementation="def...", tests="def test_...")
  
If tests fail, you get errors. Fix with:
  python(operation="develop", name="mymodule", fix_implementation="def... # fixed")

CRITICAL:
- Use 'oci' tool for container/Docker image operations - NOT bash
- Use 'scrape' for summarizing web pages
- Use 'run' for simple one-off scripts
- Use 'develop' when tests are needed
- When you get output, STOP and respond to user`

// Agent handles conversations with the LLM and executes tool calls.
type Agent struct {
	model    string
	url      string
	registry *tools.Registry
	client   *http.Client
}

// Message represents a chat message in the conversation.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments.
type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []map[string]any `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type chatResponse struct {
	Message Message `json:"message"`
}

// New creates a new Agent with the given model, URL, and tool registry.
func New(model, url string, registry *tools.Registry) *Agent {
	return &Agent{
		model:    model,
		url:      url,
		registry: registry,
		client: &http.Client{
			Timeout: 120 * time.Second, // LLM responses can be slow
		},
	}
}

// Chat sends a message and handles any tool calls in a loop.
// The context is used for cancellation and passed to tool executions.
func (a *Agent) Chat(ctx context.Context, userMessage string) (string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}

	for i := 0; i < maxToolCalls; i++ {
		resp, err := a.sendRequest(ctx, messages)
		if err != nil {
			return "", err
		}

		// If no tool calls, check if model output XML-style tool call as text
		if len(resp.Message.ToolCalls) == 0 {
			// Try to parse XML-style tool calls
			if toolName, args, ok := parseXMLToolCall(resp.Message.Content); ok {
				// Execute the parsed tool call
				tool, exists := a.registry.Get(toolName)
				if exists {
					log.Printf("[agent] executing parsed tool: %s", toolName)
					result, err := tool.Execute(ctx, args)
					if err != nil {
						result = fmt.Sprintf("Error: %v", err)
					}

					// Add this exchange to messages and continue the loop
					messages = append(messages, Message{Role: "assistant", Content: resp.Message.Content})
					messages = append(messages, Message{Role: "tool", Content: result, ToolCallID: "parsed"})
					continue
				}
			}

			// No tool calls and no parseable XML - return the response
			content := cleanResponse(resp.Message.Content)
			return content, nil
		}

		// Add assistant message with tool calls
		messages = append(messages, resp.Message)

		// Execute each tool call and add results
		for _, tc := range resp.Message.ToolCalls {
			result, err := a.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			messages = append(messages, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("exceeded maximum tool calls (%d)", maxToolCalls)
}

func (a *Agent) sendRequest(ctx context.Context, messages []Message) (*chatResponse, error) {
	reqBody := chatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    a.registry.ToOllamaFormat(),
		Stream:   false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling Ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	// Debug logging
	log.Printf("[agent] response: role=%s content_len=%d tool_calls=%d",
		chatResp.Message.Role,
		len(chatResp.Message.Content),
		len(chatResp.Message.ToolCalls))
	if len(chatResp.Message.Content) > 0 && len(chatResp.Message.Content) < 500 {
		log.Printf("[agent] content: %s", chatResp.Message.Content)
	} else if len(chatResp.Message.Content) >= 500 {
		log.Printf("[agent] content (truncated): %s...", chatResp.Message.Content[:500])
	}
	for i, tc := range chatResp.Message.ToolCalls {
		log.Printf("[agent] tool_call[%d]: %s(%s)", i, tc.Function.Name, string(tc.Function.Arguments))
	}

	return &chatResp, nil
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall) (string, error) {
	tool, ok := a.registry.Get(tc.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}

	var args map[string]any
	if len(tc.Function.Arguments) > 0 {
		if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
			return "", fmt.Errorf("parsing tool arguments: %w", err)
		}
	}

	return tool.Execute(ctx, args)
}

// parseXMLToolCall attempts to parse XML-style tool calls that some models output as text
// Returns the tool name, arguments map, and whether parsing succeeded
func parseXMLToolCall(content string) (string, map[string]any, bool) {
	// Look for <function=toolname> pattern
	if !strings.Contains(content, "<function=") {
		return "", nil, false
	}

	// Extract tool name
	start := strings.Index(content, "<function=")
	if start == -1 {
		return "", nil, false
	}

	nameStart := start + len("<function=")
	nameEnd := strings.Index(content[nameStart:], ">")
	if nameEnd == -1 {
		return "", nil, false
	}
	toolName := content[nameStart : nameStart+nameEnd]

	// Extract parameters
	args := make(map[string]any)
	paramPattern := "<parameter="
	remaining := content[nameStart+nameEnd:]

	for {
		paramStart := strings.Index(remaining, paramPattern)
		if paramStart == -1 {
			break
		}

		// Get parameter name
		nameStart := paramStart + len(paramPattern)
		nameEnd := strings.Index(remaining[nameStart:], ">")
		if nameEnd == -1 {
			break
		}
		paramName := remaining[nameStart : nameStart+nameEnd]

		// Get parameter value (content until </parameter>)
		valueStart := nameStart + nameEnd + 1
		valueEnd := strings.Index(remaining[valueStart:], "</parameter>")
		if valueEnd == -1 {
			break
		}
		paramValue := strings.TrimSpace(remaining[valueStart : valueStart+valueEnd])

		args[paramName] = paramValue
		remaining = remaining[valueStart+valueEnd+len("</parameter>"):]
	}

	if len(args) == 0 {
		return "", nil, false
	}

	log.Printf("[agent] parsed XML tool call: %s with %d args", toolName, len(args))
	return toolName, args, true
}

// cleanResponse removes any tool call syntax that the model incorrectly included in its text response
func cleanResponse(content string) string {
	// If there's content before the function call, return that
	if idx := strings.Index(content, "<function="); idx > 0 {
		before := strings.TrimSpace(content[:idx])
		if before != "" {
			return before
		}
	}

	// Otherwise indicate the issue
	if strings.Contains(content, "<function=") {
		return "I tried to run code but encountered an issue. Please try rephrasing your request."
	}

	return content
}

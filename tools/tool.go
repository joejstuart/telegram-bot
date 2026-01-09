// Package tools provides the tool interface and implementations for the agent.
package tools

import "context"

// Tool defines the interface that all tools must implement.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// Parameters returns the JSON schema for the tool's parameters.
	Parameters() map[string]any

	// Execute runs the tool with the given arguments and returns the result.
	// The context should be used for cancellation and timeouts.
	Execute(ctx context.Context, args map[string]any) (string, error)
}

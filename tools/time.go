package tools

import (
	"context"
	"time"
)

// TimeTool returns the current date and time.
type TimeTool struct{}

func (t *TimeTool) Name() string {
	return "get_current_time"
}

func (t *TimeTool) Description() string {
	return "Get the current date and time"
}

func (t *TimeTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

func (t *TimeTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return time.Now().Format("Monday, January 2, 2006 at 3:04 PM MST"), nil
}

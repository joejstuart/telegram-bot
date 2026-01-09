package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const bashTimeout = 60 * time.Second

// BashTool executes bash commands and scripts.
type BashTool struct {
	workspaceDir string
}

// NewBashTool creates a new Bash tool that runs commands in the given workspace.
func NewBashTool(workspaceDir string) *BashTool {
	if workspaceDir == "" {
		workspaceDir = defaultWorkspace
	}
	return &BashTool{workspaceDir: workspaceDir}
}

func (b *BashTool) Name() string {
	return "bash"
}

func (b *BashTool) Description() string {
	return `Execute bash commands or scripts.

Use bash for:
- File operations (ls, cat, mv, cp, rm, find, grep)
- System info (df, du, ps, top, uname)
- Running CLI tools (curl, jq, git, docker)
- Quick one-liners and pipelines
- Directory navigation and file management

Use python instead for:
- Data analysis and processing
- Complex logic or algorithms
- Working with APIs that need parsing
- Anything requiring libraries (pandas, requests, etc.)

Commands run in the workspace directory. The workspace persists between runs.`
}

func (b *BashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command or script to execute",
			},
		},
		"required": []string{"command"},
	}
}

func (b *BashTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Ensure workspace exists
	if err := os.MkdirAll(b.workspaceDir, 0755); err != nil {
		return "", fmt.Errorf("creating workspace: %w", err)
	}

	// Get absolute path for workspace
	absWorkspace, err := filepath.Abs(b.workspaceDir)
	if err != nil {
		return "", fmt.Errorf("resolving workspace path: %w", err)
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = absWorkspace

	// Set a clean environment with essential variables
	cmd.Env = append(os.Environ(),
		"WORKSPACE="+absWorkspace,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	// Build output
	var result strings.Builder

	if stdout.Len() > 0 {
		output := stdout.String()
		if len(output) > maxOutputBytes {
			output = output[:maxOutputBytes] + "\n... (output truncated)"
		}
		result.WriteString(output)
	}

	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("STDERR:\n")
		errOutput := stderr.String()
		if len(errOutput) > maxOutputBytes {
			errOutput = errOutput[:maxOutputBytes] + "\n... (output truncated)"
		}
		result.WriteString(errOutput)
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result.String() + "\n\nCommand timed out after " + bashTimeout.String(), nil
		}
		if result.Len() == 0 {
			return "", fmt.Errorf("command failed: %w", err)
		}
		// Include exit code info
		result.WriteString(fmt.Sprintf("\n\nExit code: %v", err))
		return result.String(), nil
	}

	if result.Len() == 0 {
		return "(no output)", nil
	}

	return strings.TrimSpace(result.String()), nil
}

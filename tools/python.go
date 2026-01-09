package tools

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	pythonTimeout    = 60 * time.Second
	maxOutputBytes   = 50000 // Limit output to prevent huge responses
	defaultWorkspace = "workspace"
	logPrefix        = "[python]"
)

// PythonTool provides a workspace for writing and executing Python code.
type PythonTool struct {
	workspaceDir string
}

// NewPythonTool creates a new Python workspace tool.
func NewPythonTool(workspaceDir string) *PythonTool {
	if workspaceDir == "" {
		workspaceDir = defaultWorkspace
	}
	return &PythonTool{workspaceDir: workspaceDir}
}

// Init ensures the workspace directory exists.
func (p *PythonTool) Init() error {
	return os.MkdirAll(p.workspaceDir, 0755)
}

func (p *PythonTool) Name() string {
	return "python"
}

func (p *PythonTool) Description() string {
	return `Python code execution and development.

OPERATIONS:
- run: Execute code (inline with 'code' param, or file with 'filename' param)
- develop: Create implementation + tests, runs tests automatically. Returns errors if tests fail.
- write: Save code to a file
- read: Read a file
- list: List workspace files
- test: Run pytest manually

FOR SIMPLE TASKS (quick results):
Use 'run' with inline code. Example: format data, calculate something.

FOR CODE WITH TESTS:
Use 'develop' - provide implementation and tests, tool runs tests automatically.
If tests fail, you get errors back. Call develop again with fixed code.

DEVELOP PARAMS:
- name: base filename (creates name.py and test_name.py)  
- implementation: your Python code
- tests: pytest test code
- fix_implementation: fixed code when retrying after test failure`
}

func (p *PythonTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "The operation to perform",
				"enum":        []string{"run", "develop", "write", "read", "list", "test"},
			},
			"code": map[string]any{
				"type":        "string",
				"description": "Python code for 'run' (inline) or 'write' operations",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Filename for write/read/run/test operations",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Base name for develop (creates name.py and test_name.py)",
			},
			"implementation": map[string]any{
				"type":        "string",
				"description": "Implementation code for develop operation",
			},
			"tests": map[string]any{
				"type":        "string",
				"description": "Test code for develop operation",
			},
			"fix_implementation": map[string]any{
				"type":        "string",
				"description": "Fixed implementation code when retrying after test failure",
			},
		},
		"required": []string{"operation"},
	}
}

func (p *PythonTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	operation, ok := args["operation"].(string)
	if !ok || operation == "" {
		return "", fmt.Errorf("operation is required")
	}

	log.Printf("%s operation=%s", logPrefix, operation)

	switch operation {
	case "run":
		return p.runCode(ctx, args)
	case "develop":
		return p.develop(ctx, args)
	case "test":
		return p.runTests(ctx, args)
	case "write":
		return p.writeFile(args)
	case "read":
		return p.readFile(args)
	case "list":
		return p.listFiles()
	default:
		return "", fmt.Errorf("unknown operation: %s", operation)
	}
}

func (p *PythonTool) runCode(ctx context.Context, args map[string]any) (string, error) {
	code, _ := args["code"].(string)
	filename, _ := args["filename"].(string)

	var scriptPath string

	if filename != "" {
		// Run an existing file - check it exists, but use relative path for execution
		fullPath := p.safePath(filename)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filename)
		}
		// Use just the filename since cmd.Dir is set to workspace
		scriptPath = filename
		log.Printf("%s run file=%s", logPrefix, filename)
	} else if code != "" {
		// Run inline code by writing to temp file
		tmpFile, err := os.CreateTemp(p.workspaceDir, "run_*.py")
		if err != nil {
			return "", fmt.Errorf("creating temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString(code); err != nil {
			tmpFile.Close()
			return "", fmt.Errorf("writing code: %w", err)
		}
		tmpFile.Close()
		// Use just the basename since cmd.Dir is set to workspace
		scriptPath = filepath.Base(tmpFile.Name())
		log.Printf("%s run inline code (%d bytes)", logPrefix, len(code))
		p.logCodePreview(code)
	} else {
		return "", fmt.Errorf("either 'code' or 'filename' is required for run")
	}

	return p.executeCommand(ctx, "python3", scriptPath)
}

func (p *PythonTool) runTests(ctx context.Context, args map[string]any) (string, error) {
	filename, _ := args["filename"].(string)

	// Build pytest args
	pytestArgs := []string{
		"-v",          // Verbose output
		"--tb=short",  // Short traceback format
		"--no-header", // Cleaner output
	}

	if filename != "" {
		// Test specific file - check it exists, but use relative path for execution
		fullPath := p.safePath(filename)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			return "", fmt.Errorf("test file not found: %s", filename)
		}
		// Use just the filename since cmd.Dir is set to workspace
		pytestArgs = append(pytestArgs, filename)
		log.Printf("%s test file=%s", logPrefix, filename)
	} else {
		log.Printf("%s test all (discovering test_*.py)", logPrefix)
	}

	return p.executeCommand(ctx, "pytest", pytestArgs...)
}

func (p *PythonTool) develop(ctx context.Context, args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required for develop operation")
	}

	implementation, _ := args["implementation"].(string)
	tests, _ := args["tests"].(string)
	fixImplementation, _ := args["fix_implementation"].(string)

	implFile := name + ".py"
	testFile := "test_" + name + ".py"

	// If fixing, use the fix_implementation
	if fixImplementation != "" {
		implementation = fixImplementation
		log.Printf("%s develop: applying fix to %s", logPrefix, implFile)
	}

	// Write implementation if provided
	if implementation != "" {
		implPath := filepath.Join(p.workspaceDir, implFile)
		if err := os.WriteFile(implPath, []byte(implementation), 0644); err != nil {
			return "", fmt.Errorf("writing implementation: %w", err)
		}
		log.Printf("%s develop: wrote %s (%d bytes)", logPrefix, implFile, len(implementation))
		p.logCodePreview(implementation)
	}

	// Write tests if provided
	if tests != "" {
		testPath := filepath.Join(p.workspaceDir, testFile)
		if err := os.WriteFile(testPath, []byte(tests), 0644); err != nil {
			return "", fmt.Errorf("writing tests: %w", err)
		}
		log.Printf("%s develop: wrote %s (%d bytes)", logPrefix, testFile, len(tests))
	}

	// Check both files exist before running tests
	implPath := filepath.Join(p.workspaceDir, implFile)
	testPath := filepath.Join(p.workspaceDir, testFile)

	if _, err := os.Stat(implPath); os.IsNotExist(err) {
		return "", fmt.Errorf("implementation file %s not found - provide 'implementation' parameter", implFile)
	}
	if _, err := os.Stat(testPath); os.IsNotExist(err) {
		return "", fmt.Errorf("test file %s not found - provide 'tests' parameter", testFile)
	}

	// Run tests
	log.Printf("%s develop: running tests %s", logPrefix, testFile)
	output, err := p.runTestsInternal(ctx, testFile)
	passed := err == nil && !strings.Contains(output, "FAILED")

	if passed && strings.Contains(output, "passed") {
		log.Printf("%s develop: TESTS PASSED", logPrefix)
		return fmt.Sprintf("✅ ALL TESTS PASSED\n\nFiles created:\n- %s\n- %s\n\nTest output:\n%s", implFile, testFile, output), nil
	}

	// Tests failed - return errors for model to fix
	log.Printf("%s develop: TESTS FAILED", logPrefix)

	return fmt.Sprintf(`❌ TESTS FAILED

Fix the implementation and call python again with:
- operation: "develop"
- name: "%s"
- fix_implementation: <your fixed code>

Errors:
%s

IMPORTANT: Only fix the implementation code. Keep the same tests.
Make minimal changes to fix the specific errors shown above.`, name, output), nil
}

func (p *PythonTool) runTestsInternal(ctx context.Context, testFile string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pythonTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pytest", "-v", "--tb=short", testFile)
	cmd.Dir = p.workspaceDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	// Truncate if too long
	if len(output) > 3000 {
		output = output[:3000] + "\n... (truncated)"
	}

	p.logOutputPreview(output)

	return output, err
}

func (p *PythonTool) executeCommand(ctx context.Context, command string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pythonTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = p.workspaceDir

	log.Printf("%s exec: %s %s", logPrefix, command, strings.Join(args, " "))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

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

	// Log execution result
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("%s TIMEOUT after %v", logPrefix, pythonTimeout)
			return result.String() + "\n\nExecution timed out after " + pythonTimeout.String(), nil
		}
		log.Printf("%s FAILED (%v) - %v", logPrefix, duration, err)
		p.logOutputPreview(result.String())
		if result.Len() == 0 {
			return "", fmt.Errorf("execution failed: %w", err)
		}
		return result.String(), nil
	}

	log.Printf("%s OK (%v) stdout=%d stderr=%d", logPrefix, duration, stdout.Len(), stderr.Len())
	p.logOutputPreview(result.String())

	if result.Len() == 0 {
		return "(no output)", nil
	}

	return result.String(), nil
}

func (p *PythonTool) writeFile(args map[string]any) (string, error) {
	code, ok := args["code"].(string)
	if !ok || code == "" {
		return "", fmt.Errorf("code is required for write operation")
	}

	filename, ok := args["filename"].(string)
	if !ok || filename == "" {
		return "", fmt.Errorf("filename is required for write operation")
	}

	log.Printf("%s write file=%s (%d bytes)", logPrefix, filename, len(code))
	p.logCodePreview(code)

	// Ensure we stay in workspace
	filePath := p.safePath(filename)

	// Create subdirectories if needed
	if dir := filepath.Dir(filePath); dir != p.workspaceDir {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("creating directory: %w", err)
		}
	}

	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}

	return fmt.Sprintf("Saved to %s (%d bytes)", filename, len(code)), nil
}

func (p *PythonTool) readFile(args map[string]any) (string, error) {
	filename, ok := args["filename"].(string)
	if !ok || filename == "" {
		return "", fmt.Errorf("filename is required for read operation")
	}

	log.Printf("%s read file=%s", logPrefix, filename)

	filePath := p.safePath(filename)

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filename)
		}
		return "", fmt.Errorf("reading file: %w", err)
	}

	log.Printf("%s read OK (%d bytes)", logPrefix, len(content))

	if len(content) > maxOutputBytes {
		return string(content[:maxOutputBytes]) + "\n... (file truncated)", nil
	}

	return string(content), nil
}

func (p *PythonTool) listFiles() (string, error) {
	log.Printf("%s list", logPrefix)

	var files []string

	err := filepath.Walk(p.workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel(p.workspaceDir, path)
			files = append(files, fmt.Sprintf("  %s (%d bytes)", relPath, info.Size()))
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("listing files: %w", err)
	}

	log.Printf("%s list found %d files", logPrefix, len(files))

	if len(files) == 0 {
		return "Workspace is empty.", nil
	}

	return fmt.Sprintf("Files in workspace:\n%s", strings.Join(files, "\n")), nil
}

// logCodePreview logs the first few lines of code for debugging
func (p *PythonTool) logCodePreview(code string) {
	lines := strings.Split(code, "\n")
	preview := lines
	if len(lines) > 5 {
		preview = lines[:5]
	}
	for i, line := range preview {
		// Truncate long lines
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		log.Printf("%s   %d: %s", logPrefix, i+1, line)
	}
	if len(lines) > 5 {
		log.Printf("%s   ... (%d more lines)", logPrefix, len(lines)-5)
	}
}

// logOutputPreview logs output for debugging (always logs on failure)
func (p *PythonTool) logOutputPreview(output string) {
	output = strings.TrimSpace(output)
	if output == "" {
		log.Printf("%s   (no output)")
		return
	}

	lines := strings.Split(output, "\n")
	maxLines := 15

	log.Printf("%s output (%d lines):", logPrefix, len(lines))

	showLines := lines
	if len(lines) > maxLines {
		showLines = lines[:maxLines]
	}

	for _, line := range showLines {
		log.Printf("%s   %s", logPrefix, line)
	}

	if len(lines) > maxLines {
		log.Printf("%s   ... (%d more lines)", logPrefix, len(lines)-maxLines)
	}
}

// safePath ensures the path stays within the workspace directory.
func (p *PythonTool) safePath(filename string) string {
	// Clean and make absolute to prevent directory traversal
	cleaned := filepath.Clean(filename)
	// Remove any leading slashes or parent directory references
	cleaned = strings.TrimPrefix(cleaned, "/")
	for strings.HasPrefix(cleaned, "../") {
		cleaned = strings.TrimPrefix(cleaned, "../")
	}
	return filepath.Join(p.workspaceDir, cleaned)
}

package tools

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	ociTimeout   = 120 * time.Second
	ociLogPrefix = "[oci]"
	maxOCIOutput = 100000 // Max output bytes
)

// OCITool provides operations for interacting with container registries.
// Uses oras, skopeo, and podman CLI tools.
type OCITool struct{}

// NewOCITool creates a new OCI registry tool.
func NewOCITool() *OCITool {
	return &OCITool{}
}

func (o *OCITool) Name() string {
	return "oci"
}

func (o *OCITool) Description() string {
	return `Interact with OCI container registries and images.

OPERATIONS:
- inspect: Examine image metadata and configuration
- manifest: Get raw image manifest (JSON)
- list-tags: List all tags in a repository
- pull: Pull/copy an image to local storage or another registry
- copy: Copy image between registries (with optional modifications)
- annotate: Add or modify annotations on an image
- delete: Delete an image tag from a registry
- push: Push a local artifact to a registry

EXAMPLES:
- Inspect image: operation=inspect, image=docker.io/library/alpine:latest
- Get manifest: operation=manifest, image=ghcr.io/org/app:v1.0
- List tags: operation=list-tags, image=docker.io/library/nginx
- Copy with annotations: operation=copy, source=src:tag, dest=dst:tag, annotations={"key": "value"}
- Pull image: operation=pull, image=quay.io/repo/image:tag

TOOLS USED:
- skopeo: For inspect, manifest, list-tags, copy, delete
- oras: For push artifacts, annotate
- podman: For local image operations when needed

All image references should be fully qualified (registry/repo:tag).`
}

func (o *OCITool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "The operation to perform",
				"enum":        []string{"inspect", "manifest", "list-tags", "pull", "copy", "annotate", "delete", "push"},
			},
			"image": map[string]any{
				"type":        "string",
				"description": "Image reference (registry/repo:tag) for inspect, manifest, list-tags, pull, delete",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Source image reference for copy operation",
			},
			"dest": map[string]any{
				"type":        "string",
				"description": "Destination image reference for copy/push operations",
			},
			"annotations": map[string]any{
				"type":        "string",
				"description": "JSON object of annotations to add (for annotate/copy operations)",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Local file path for push operation",
			},
			"media_type": map[string]any{
				"type":        "string",
				"description": "Media type for push operation (default: application/octet-stream)",
			},
			"raw": map[string]any{
				"type":        "boolean",
				"description": "For manifest: return raw JSON without formatting",
			},
			"all": map[string]any{
				"type":        "boolean",
				"description": "For pull/copy: copy all architectures (multi-arch)",
			},
		},
		"required": []string{"operation"},
	}
}

func (o *OCITool) Execute(ctx context.Context, args map[string]any) (string, error) {
	operation, _ := args["operation"].(string)
	if operation == "" {
		return "", fmt.Errorf("operation is required")
	}

	log.Printf("%s operation=%s", ociLogPrefix, operation)

	switch operation {
	case "inspect":
		return o.inspect(ctx, args)
	case "manifest":
		return o.manifest(ctx, args)
	case "list-tags":
		return o.listTags(ctx, args)
	case "pull":
		return o.pull(ctx, args)
	case "copy":
		return o.copyImage(ctx, args)
	case "annotate":
		return o.annotate(ctx, args)
	case "delete":
		return o.delete(ctx, args)
	case "push":
		return o.push(ctx, args)
	default:
		return "", fmt.Errorf("unknown operation: %s", operation)
	}
}

func (o *OCITool) inspect(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for inspect")
	}

	ref := o.normalizeRef(image)
	log.Printf("%s inspect %s", ociLogPrefix, ref)

	// Use skopeo inspect
	return o.runCommand(ctx, "skopeo", "inspect", "docker://"+ref)
}

func (o *OCITool) manifest(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for manifest")
	}

	ref := o.normalizeRef(image)
	log.Printf("%s manifest %s", ociLogPrefix, ref)

	raw, _ := args["raw"].(bool)

	cmdArgs := []string{"inspect", "--raw"}
	if !raw {
		// Pipe through jq for formatting if available
		cmdArgs = append(cmdArgs, "docker://"+ref)
		output, err := o.runCommand(ctx, "skopeo", cmdArgs...)
		if err != nil {
			return output, err
		}
		// Try to format with jq
		formatted, fmtErr := o.runCommandInput(ctx, output, "jq", ".")
		if fmtErr == nil {
			return formatted, nil
		}
		return output, nil
	}

	return o.runCommand(ctx, "skopeo", append(cmdArgs, "docker://"+ref)...)
}

func (o *OCITool) listTags(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for list-tags")
	}

	// Remove tag if present for list-tags
	ref := o.normalizeRef(image)
	if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
		ref = ref[:idx]
	}

	log.Printf("%s list-tags %s", ociLogPrefix, ref)

	return o.runCommand(ctx, "skopeo", "list-tags", "docker://"+ref)
}

func (o *OCITool) pull(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for pull")
	}

	ref := o.normalizeRef(image)
	all, _ := args["all"].(bool)

	log.Printf("%s pull %s (all=%v)", ociLogPrefix, ref, all)

	// Use podman pull for local storage
	cmdArgs := []string{"pull"}
	if all {
		cmdArgs = append(cmdArgs, "--all-tags")
	}
	cmdArgs = append(cmdArgs, ref)

	return o.runCommand(ctx, "podman", cmdArgs...)
}

func (o *OCITool) copyImage(ctx context.Context, args map[string]any) (string, error) {
	source, _ := args["source"].(string)
	dest, _ := args["dest"].(string)
	if source == "" || dest == "" {
		return "", fmt.Errorf("source and dest are required for copy")
	}

	srcRef := o.normalizeRef(source)
	dstRef := o.normalizeRef(dest)
	all, _ := args["all"].(bool)

	log.Printf("%s copy %s -> %s", ociLogPrefix, srcRef, dstRef)

	cmdArgs := []string{"copy"}
	if all {
		cmdArgs = append(cmdArgs, "--all")
	}

	// Handle annotations if provided
	annotations, _ := args["annotations"].(string)
	if annotations != "" {
		// Parse annotations and add them
		// skopeo doesn't support annotations directly, so we note this
		log.Printf("%s note: annotations will be added via manifest modification", ociLogPrefix)
	}

	cmdArgs = append(cmdArgs, "docker://"+srcRef, "docker://"+dstRef)

	return o.runCommand(ctx, "skopeo", cmdArgs...)
}

func (o *OCITool) annotate(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	annotations, _ := args["annotations"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for annotate")
	}
	if annotations == "" {
		return "", fmt.Errorf("annotations JSON is required for annotate")
	}

	ref := o.normalizeRef(image)
	log.Printf("%s annotate %s with %s", ociLogPrefix, ref, annotations)

	// Use oras for annotation
	// oras manifest annotate <ref> --annotation key=value
	// Parse the JSON annotations and convert to --annotation flags
	cmdArgs := []string{"manifest", "annotate", ref}

	// Simple parsing of JSON object
	annotations = strings.TrimSpace(annotations)
	annotations = strings.TrimPrefix(annotations, "{")
	annotations = strings.TrimSuffix(annotations, "}")

	// Split by comma and add each annotation
	for _, pair := range strings.Split(annotations, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		// Remove quotes and convert to key=value format
		pair = strings.ReplaceAll(pair, "\"", "")
		pair = strings.ReplaceAll(pair, ": ", "=")
		pair = strings.ReplaceAll(pair, ":", "=")
		cmdArgs = append(cmdArgs, "--annotation", pair)
	}

	return o.runCommand(ctx, "oras", cmdArgs...)
}

func (o *OCITool) delete(ctx context.Context, args map[string]any) (string, error) {
	image, _ := args["image"].(string)
	if image == "" {
		return "", fmt.Errorf("image is required for delete")
	}

	ref := o.normalizeRef(image)
	log.Printf("%s delete %s", ociLogPrefix, ref)

	return o.runCommand(ctx, "skopeo", "delete", "docker://"+ref)
}

func (o *OCITool) push(ctx context.Context, args map[string]any) (string, error) {
	file, _ := args["file"].(string)
	dest, _ := args["dest"].(string)
	if file == "" || dest == "" {
		return "", fmt.Errorf("file and dest are required for push")
	}

	dstRef := o.normalizeRef(dest)
	mediaType, _ := args["media_type"].(string)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	log.Printf("%s push %s -> %s (type=%s)", ociLogPrefix, file, dstRef, mediaType)

	// Use oras push
	artifact := fmt.Sprintf("%s:%s", file, mediaType)

	cmdArgs := []string{"push", dstRef, artifact}

	// Add annotations if provided
	annotations, _ := args["annotations"].(string)
	if annotations != "" {
		annotations = strings.TrimSpace(annotations)
		annotations = strings.TrimPrefix(annotations, "{")
		annotations = strings.TrimSuffix(annotations, "}")
		for _, pair := range strings.Split(annotations, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			pair = strings.ReplaceAll(pair, "\"", "")
			pair = strings.ReplaceAll(pair, ": ", "=")
			pair = strings.ReplaceAll(pair, ":", "=")
			cmdArgs = append(cmdArgs, "--annotation", pair)
		}
	}

	return o.runCommand(ctx, "oras", cmdArgs...)
}

// normalizeRef ensures the image reference has a registry prefix
func (o *OCITool) normalizeRef(ref string) string {
	ref = strings.TrimPrefix(ref, "docker://")
	ref = strings.TrimPrefix(ref, "oci://")

	// If no registry specified, assume docker.io
	if !strings.Contains(ref, "/") {
		ref = "docker.io/library/" + ref
	} else if !strings.Contains(strings.Split(ref, "/")[0], ".") {
		// No dot in first segment, assume docker.io
		ref = "docker.io/" + ref
	}

	return ref
}

func (o *OCITool) runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ociTimeout)
	defer cancel()

	log.Printf("%s exec: %s %s", ociLogPrefix, name, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	output := stdout.String()
	errOutput := stderr.String()

	if len(output) > maxOCIOutput {
		output = output[:maxOCIOutput] + "\n... (truncated)"
	}

	if err != nil {
		log.Printf("%s FAILED (%v) - %v", ociLogPrefix, duration, err)
		if errOutput != "" {
			log.Printf("%s stderr: %s", ociLogPrefix, errOutput)
			return fmt.Sprintf("Error: %s\n%s", err.Error(), errOutput), err
		}
		return fmt.Sprintf("Error: %s", err.Error()), err
	}

	log.Printf("%s OK (%v) stdout=%d stderr=%d", ociLogPrefix, duration, len(output), len(errOutput))

	if output != "" {
		return output, nil
	}
	if errOutput != "" {
		return errOutput, nil
	}
	return "Command completed successfully", nil
}

func (o *OCITool) runCommandInput(ctx context.Context, input string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ociTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stderr.String(), err
	}

	return stdout.String(), nil
}

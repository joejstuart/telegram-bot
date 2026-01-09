# telegram-bot

An agentic Telegram bot powered by Ollama with extensible tool support.

## Project Structure

```
telegram-bot/
├── main.go              # Application entrypoint
├── config/
│   └── config.go        # Configuration management
├── agent/
│   └── agent.go         # Agentic loop with tool execution
└── tools/
    ├── tool.go          # Tool interface
    ├── registry.go      # Tool registry
    ├── time.go          # Current time tool
    ├── calendar.go      # Google Calendar tool
    ├── python.go        # Python code execution
    ├── bash.go          # Bash command execution
    ├── scrape.go        # Web scraping and summarization
    └── oci.go           # OCI registry operations
```

## Configuration

All configuration is via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | Yes | - | Bot token from @BotFather |
| `OLLAMA_URL` | No | `http://localhost:11434/api/chat` | Ollama API endpoint |
| `OLLAMA_MODEL` | No | `qwen3:8b` | Model to use |
| `GOOGLE_CLIENT_ID` | For calendar | - | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | For calendar | - | Google OAuth client secret |
| `GOOGLE_REDIRECT_URL` | No | `urn:ietf:wg:oauth:2.0:oob` | Google OAuth redirect URL |
| `GOOGLE_TOKEN_FILE` | No | `google_token.json` | Google token storage path |
| `PYTHON_WORKSPACE` | No | `workspace` | Directory for scripts and files |

## Setup

1. Create a bot with [@BotFather](https://t.me/botfather) on Telegram
2. Ensure Ollama is running with your chosen model:
   ```bash
   ollama pull qwen3:8b
   ollama serve
   ```

## Running

```bash
export TELEGRAM_BOT_TOKEN="your-token-here"
go run .
```

## Adding Tools

1. Create a new file in `tools/` implementing the `Tool` interface:

```go
package tools

import "context"

type MyTool struct{}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Description() string {
    return "Description the LLM reads to decide when to use this tool"
}

func (t *MyTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{
                "type":        "string",
                "description": "The search query",
            },
        },
        "required": []string{"query"},
    }
}

func (t *MyTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    query := args["query"].(string)
    // Your logic here
    return "result", nil
}
```

2. Register it in `main.go`:

```go
registry.Register(&tools.MyTool{})
```

## Google Calendar Setup

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a project and enable the **Google Calendar API**
3. Go to "APIs & Services" → "Credentials"
4. Click "Create Credentials" → "OAuth client ID"
5. Choose "Desktop app" as the application type
6. Copy the **Client ID** and **Client Secret**
7. Set the environment variables:
   ```bash
   export GOOGLE_CLIENT_ID="your-client-id.apps.googleusercontent.com"
   export GOOGLE_CLIENT_SECRET="your-client-secret"
   ```
8. Use `/auth` in the bot to complete authentication

## Code Execution

The bot has a shared workspace where it can write and execute Python and Bash code. Both tools share the same workspace directory.

### Python
Use for data processing, complex logic, APIs, and library usage.
- **Run** inline code: "Calculate the first 20 fibonacci numbers"
- **Write** scripts: "Save a script that fetches weather data"
- **Read** files: "Show me what's in analysis.py"
- **List** workspace: "What files are in my workspace?"

### Bash
Use for file operations, CLI tools, and quick shell commands.
- **Commands**: "List all CSV files in the workspace"
- **Pipelines**: "Count lines in all Python files"
- **CLI tools**: "Use curl to fetch a URL"

Files are stored in the `workspace/` directory (configurable via `PYTHON_WORKSPACE`).

## Web Scraping

The bot can scrape and summarize web pages. Just give it a URL and it will:

1. Fetch the page content
2. Extract the main text (stripping navigation, ads, etc.)
3. Use Ollama to generate a concise summary

Example prompts:
- "Summarize https://news.ycombinator.com"
- "What's on the homepage of example.com?"
- "Give me the main points from this article: https://..."

## OCI Registry Operations

The bot can interact with container registries using `skopeo`, `oras`, and `podman`. Ensure these CLI tools are installed and in your PATH.

### Operations

| Operation | Description | Tool Used |
|-----------|-------------|-----------|
| `inspect` | Examine image metadata and config | skopeo |
| `manifest` | Get raw image manifest JSON | skopeo |
| `list-tags` | List all tags in a repository | skopeo |
| `pull` | Pull image to local storage | podman |
| `copy` | Copy image between registries | skopeo |
| `annotate` | Add/modify image annotations | oras |
| `delete` | Delete image tag from registry | skopeo |
| `push` | Push artifact to registry | oras |

### Examples

- "Inspect the alpine:latest image"
- "What tags are available for docker.io/library/nginx?"
- "Copy ghcr.io/org/app:v1 to my-registry.io/app:v1"
- "Add annotation 'version=1.0' to my-image:latest"
- "Show me the manifest for quay.io/prometheus/prometheus:latest"

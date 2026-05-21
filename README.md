# Kiro Proxy

An OpenAI-compatible API proxy for [Amazon Kiro](https://kiro.dev). Use any OpenAI-compatible client, SDK, or tool with Kiro's AI models — no code changes required.

## Features

- **OpenAI-compatible API** — exposes `/v1/chat/completions` and `/v1/models`
- **Streaming & non-streaming** — full SSE support for real-time responses
- **Tool/function calling** — translates OpenAI tool calls to Kiro format and back
- **Fake reasoning** — injects `<thinking>` tags and surfaces them as `reasoning_content`
- **Daemon mode** — run as a background service with start/stop/restart/logs
- **Automatic token refresh** — handles AWS SSO OIDC and Kiro Desktop Auth
- **Retry with backoff** — automatic retries on transient errors (403/429/5xx)
- **Minimal dependencies** — built on Go's standard library with only 3 external packages

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap githendrik/tap
brew install kiro-proxy
```

### From Source

```bash
go install github.com/githendrik/kiro-proxy-go@latest
```

### Download Binary

Pre-built binaries for macOS and Linux (amd64/arm64) are available on [GitHub Releases](https://github.com/githendrik/kiro-proxy-go/releases).

## Quick Start

1. **Authenticate with Kiro:**

   ```bash
   kiro-cli login
   ```

2. **Start the proxy:**

   ```bash
   kiro-proxy start
   ```

3. **Use it like any OpenAI API:**

   ```bash
   curl http://localhost:8000/v1/chat/completions \
     -H "Authorization: Bearer my-super-secret-password-123" \
     -H "Content-Type: application/json" \
     -d '{
       "model": "kiro-code-interleave",
       "messages": [{"role": "user", "content": "Hello!"}]
     }'
   ```

## Usage

### Daemon Commands

| Command | Description |
|---------|-------------|
| `kiro-proxy start` | Start as a background daemon |
| `kiro-proxy stop` | Stop the running daemon |
| `kiro-proxy restart` | Restart the daemon |
| `kiro-proxy logs` | Tail daemon log output |
| `kiro-proxy run` | Run in foreground (default) |
| `kiro-proxy run -port 9000` | Run on a custom port |
| `kiro-proxy help` | Show help message |

### With the OpenAI Python SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8000/v1",
    api_key="my-super-secret-password-123"
)

response = client.chat.completions.create(
    model="kiro-code-interleave",
    messages=[
        {"role": "user", "content": "Explain the builder pattern in Go"}
    ],
    stream=True
)

for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### With OpenCode / Other Tools

Point any OpenAI-compatible tool at `http://localhost:8000/v1` with your configured API key.

## Configuration

Configuration is loaded with this priority: **environment variables > .env file > YAML config > defaults**.

### Config File (Recommended)

```bash
mkdir -p ~/.config/kiro-proxy
cp config.example.yaml ~/.config/kiro-proxy/config.yaml
```

```yaml
# Server
host: 0.0.0.0
port: 8000

# Proxy authentication
proxy_api_key: your-secret-key

# Kiro credentials (use one method)
creds_file: ~/.aws/sso/cache/kiro-auth-token-cli.json
# refresh_token: your-refresh-token-here

# Region
region: us-east-1
# api_region: us-east-1  # defaults to region

# Timeouts & retries
streaming_read_timeout: 300
max_retries: 3

# Features
fake_reasoning: true
fake_reasoning_max_tokens: 4000

# Logging
log_level: info
```

Config file search order:
1. `./kiro-proxy.yaml`
2. `~/.config/kiro-proxy/config.yaml`
3. `~/.kiro-proxy.yaml`
4. `/etc/kiro-proxy/config.yaml`

### Environment Variables

Environment variables override all other configuration sources.

| Variable | Default | Description |
|----------|---------|-------------|
| `KIRO_CREDS_FILE` | — | Path to kiro-cli credentials JSON |
| `REFRESH_TOKEN` | — | Direct refresh token (alternative to file) |
| `KIRO_REGION` | `us-east-1` | AWS SSO auth region |
| `KIRO_API_REGION` | *(from region)* | API endpoint region override |
| `PROXY_API_KEY` | `my-super-secret-password-123` | API key clients must provide |
| `SERVER_HOST` | `0.0.0.0` | Listen host |
| `SERVER_PORT` | `8000` | Listen port |
| `STREAMING_READ_TIMEOUT` | `300` | Stream read timeout in seconds |
| `MAX_RETRIES` | `3` | Retry attempts on transient errors |
| `FAKE_REASONING` | `true` | Enable thinking tag injection |
| `FAKE_REASONING_MAX_TOKENS` | `4000` | Max thinking token budget |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

### Authentication

**Method 1 — Credentials file (recommended):**

Run `kiro-cli login` and the proxy will automatically read the token from `~/.aws/sso/cache/kiro-auth-token-cli.json`.

**Method 2 — Direct refresh token:**

Set `REFRESH_TOKEN` or `refresh_token` in your config file.

The auth manager auto-detects whether to use OIDC or Desktop Auth based on the credentials format.

## Architecture

```
OpenAI Client (SDK, curl, OpenCode, etc.)
    │
    ▼  POST /v1/chat/completions
┌─────────────────────────────────────────┐
│  kiro-proxy                             │
│                                         │
│  middleware/auth  → API key validation  │
│  handler/openai   → Parse request       │
│  converter/openai → Convert payload     │
│  client           → HTTP + retry + auth │
│  auth             → OIDC token refresh  │
│  parser/eventstream → Parse AWS stream  │
│  streaming/openai → Convert to SSE      │
└─────────────────────────────────────────┘
    │
    ▼  POST /generateAssistantResponse
  Kiro API (q.{region}.amazonaws.com)
```

## Development

### Prerequisites

- Go 1.24+

### Build

```bash
make build
```

### Run

```bash
make run
# or
go run . run
```

### Test

```bash
make test
```

### Release

Tag a version and push — GitHub Actions handles the rest:

```bash
git tag v0.x.x
git push --tags
```

GoReleaser builds binaries for all platforms and updates the Homebrew tap automatically.

## License

[AGPL-3.0](LICENSE)

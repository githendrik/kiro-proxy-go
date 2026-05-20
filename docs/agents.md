# Kiro Proxy Go - Development Context

## Project Goal

Port Python `kiro-gateway` to Go for OpenCode integration. Provides OpenAI-compatible API proxy to Kiro (AWS CodeWhisperer) with:
- OIDC auth via kiro-cli credentials (`~/.aws/sso/cache/kiro-auth-token-cli.json`)
- Streaming and non-streaming completions
- Tool/function calling support
- Fake reasoning (thinking tag injection)
- No extra configuration vs. Python gateway

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐     ┌─────────────┐
│   OpenAI    │────▶│  Kiro Proxy  │────▶│   Kiro API      │     │   Auth      │
│   Client    │     │    (Go)      │     │ q.{region}.     │────▶│   Manager   │
│             │◀────│              │◀────│ amazonaws.com    │     │  (OIDC)     │
└─────────────┘     └──────────────┘     └─────────────────┘     └─────────────┘
                         │
                         ├─ internal/auth/        - OIDC/Desktop auth
                         ├─ internal/client/      - HTTP client with retries
                         ├─ internal/config/      - Configuration loading
                         ├─ internal/converter/   - OpenAI ↔ Kiro conversion
                         ├─ internal/handler/     - HTTP handlers (/v1/*)
                         ├─ internal/middleware/  - Auth, CORS
                         ├─ internal/models/      - Model resolution
                         ├─ internal/parser/      - AWS Event Stream parser
                         ├─ internal/streaming/   - SSE stream conversion
                         └─ main.go               - Entry point
```

## Key Decisions

### Auth Type
- **OIDC (AWS SSO)** via kiro-cli credentials file
- Auto-detects from presence of `clientId`/`clientSecret` in device registration
- Refreshes tokens automatically on 403

### API Endpoint
- Uses `https://q.{region}.amazonaws.com` (not `runtime.{region}.kiro.dev`)
- Matches working mirror (rathesan.iyadurai/kiro-gateway v2.3)
- OIDC auth doesn't return `profileArn` (required for newer endpoint)

### Headers
```go
Content-Type: application/json  // NOT application/x-amz-json-1.0
x-amz-target: (NOT sent)        // Only for runtime.* endpoint
User-Agent: aws-sdk-js/1.0.27 ... KiroIDE-0.7.45-{fingerprint}
```

### AWS Event Stream Parsing
- Kiro API returns **binary AWS Event Stream** format (not plain SSE)
- Parser extracts JSON by:
  1. Decoding bytes as UTF-8 (ignoring invalid binary framing)
  2. Finding JSON objects by matching braces
  3. Parsing into events (content, tool_use, usage, error)

### Tool Calling Format
**Request with tool results:**
```json
{
  "conversationState": {
    "currentMessage": {
      "userInputMessage": {
        "content": "Continue",
        "userInputMessageContext": {
          "tools": [...],        // Tool definitions
          "toolResults": [...]   // Results from tool execution
        }
      }
    },
    "history": [
      {"userInputMessage": {...}},           // Original user query
      {"assistantResponseMessage": {         // Assistant tool call
        "content": "(empty)",
        "toolUses": [...]
      }}
    ]
  }
}
```

**Key points:**
- Tool messages don't create separate history entries
- Tool results go in `currentMessage.userInputMessageContext.toolResults`
- Tool definitions also in `currentMessage.userInputMessageContext.tools`
- Current content = `"Continue"` (signals model to continue after tool results)
- Assistant messages with tool calls but no content use `"(empty)"` placeholder

## Configuration

All config via environment variables (or `.env` file):

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_HOST` | `0.0.0.0` | Listen host |
| `SERVER_PORT` | `8000` | Listen port |
| `PROXY_API_KEY` | (required) | Client auth key |
| `KIRO_CREDS_FILE` | (required) | Path to kiro-cli credentials |
| `KIRO_REGION` | `us-east-1` | AWS SSO region |
| `KIRO_API_REGION` | (auto) | Q API region override |
| `STREAMING_READ_TIMEOUT` | `300` | Stream read timeout (seconds) |
| `MAX_RETRIES` | `3` | Retry attempts |
| `FAKE_REASONING` | `true` | Enable thinking tag injection |
| `FAKE_REASONING_MAX_TOKENS` | `4000` | Max thinking tokens |
| `LOG_LEVEL` | `info` | Log level (debug/info/warn/error) |

## Testing

### Basic Chat
```bash
API_KEY="your-key"
curl -X POST "http://localhost:8000/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hi"}], "stream": true}'
```

### Tool Calling
```bash
# Step 1: Get tool call
RESP=$(curl -s -X POST "http://localhost:8000/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4",
    "messages": [{"role": "user", "content": "What is 15 * 8?"}],
    "tools": [{"type": "function", "function": {"name": "calc", "description": "Calculate", "parameters": {"type": "object", "properties": {"expr": {"type": "string"}}, "required": ["expr"]}}}]
  }')

# Extract tool call
TOOL_ID=$(echo "$RESP" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['choices'][0]['message']['tool_calls'][0]['id'])")

# Step 2: Send tool result
curl -X POST "http://localhost:8000/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"claude-sonnet-4\",
    \"messages\": [
      {\"role\": \"user\", \"content\": \"What is 15 * 8?\"},
      {\"role\": \"assistant\", \"tool_calls\": [{\"id\": \"$TOOL_ID\", \"type\": \"function\", \"function\": {\"name\": \"calc\", \"arguments\": \"{\\\"expr\\\": \\\"15 * 8\\\"}\"}}]},
      {\"role\": \"tool\", \"tool_call_id\": \"$TOOL_ID\", \"content\": \"120\"}
    ],
    \"tools\": [{\"type\": \"function\", \"function\": {\"name\": \"calc\", \"description\": \"Calculate\", \"parameters\": {\"type\": \"object\", \"properties\": {\"expr\": {\"type\": \"string\"}}, \"required\": [\"expr\"]}}}]
  }"
```

### Run Tests
```bash
go test ./... -v
# 31 tests: converter (11), parser (11), streaming (6)
```

## Credentials Flow

### OIDC (kiro-cli)
1. User runs `kiro-cli login` → credentials in `~/.aws/sso/cache/kiro-auth-token-cli.json`
2. Device registration in `~/.aws/sso/cache/{clientIdHash}.json`
3. Proxy reads `accessToken` and auto-refreshes via OIDC endpoint
4. Refresh request:
   ```json
   POST https://oidc.{region}.amazonaws.com/token
   {
     "grantType": "refresh_token",
     "clientId": "...",
     "clientSecret": "...",
     "refreshToken": "..."
   }
   ```

### Desktop Auth (not used)
- Uses `~/.aws/sso/cache/kiro-auth-token-desktop.json`
- Refresh via `https://prod.{region}.auth.desktop.kiro.dev/refreshToken`

## Fake Reasoning

When `FAKE_REASONING=true`, injects thinking tags into system prompt:
```
<thinking_mode>enabled</thinking_mode>
<max_thinking_length>4000</max_thinking_length>
<thinking_instruction>...</thinking_instruction>
```

Model responds with:
```xml
<thinking>
Let me think about this...
</thinking>
Actual response content
```

Parser extracts thinking as `reasoning_content` field in response.

## Known Issues

### Token Usage
- Not accurately tracked (requires tiktoken integration)
- Currently returns zeros in response

### Model Listing
- Fetches from Kiro API on startup
- Cached in memory (no refresh during runtime)

### Error Handling
- Retry logic implemented for 403/429/5xx
- First token timeout not implemented (config field removed)

## Files Reference

| File | Purpose |
|------|---------|
| `main.go` | Entry point, server setup |
| `internal/config/config.go` | Configuration loading |
| `internal/auth/auth.go` | OIDC/Desktop auth manager |
| `internal/client/client.go` | HTTP client with retry logic |
| `internal/converter/openai.go` | OpenAI ↔ Kiro payload conversion |
| `internal/handler/openai.go` | HTTP handlers for /v1/* endpoints |
| `internal/parser/eventstream.go` | AWS Event Stream binary parser |
| `internal/streaming/openai.go` | SSE stream conversion |
| `internal/middleware/auth.go` | API key auth, CORS |
| `internal/models/resolver.go` | Model name resolution |

## Comparison: Python vs Go

| Feature | Python Mirror | Go Implementation |
|---------|---------------|-------------------|
| Auth | OIDC + Desktop | OIDC + Desktop |
| Endpoint | q.{region}.amazonaws.com | q.{region}.amazonaws.com |
| Stream Format | AWS Event Stream | AWS Event Stream |
| Parser | Regex JSON extraction | Brace-matching JSON extraction |
| Tool Results | ✓ | ✓ |
| Fake Reasoning | ✓ | ✓ |
| Token Counting | tiktoken | Not implemented |
| Model Cache | Runtime refresh | Startup only |

## Development Notes

### Build
```bash
go build ./...
./kiro-proxy-go
```

### Docker
```bash
docker build -t kiro-proxy-go .
docker-compose up
```

### Debug Logging
```bash
LOG_LEVEL=debug go run .
# Shows Kiro API requests/responses
```

### Git History
```
77d6f88 Remove unused FirstTokenTimeout config
302863c Add comprehensive tests (31 tests)
301c4e7 Fix tool calling: proper history and tool results handling
3946415 Port kiro-gateway to Go with OpenAI-compatible API
03b391b Initial commit
```

## Working Mirror Reference

- **Repo**: github.com/rathesan.iyadurai/kiro-gateway (v2.3)
- **Location**: `/Users/taarihe1/Repos/rathesan.iyadurai/kiro-gateway/`
- **Debug logs**: `/Users/taarihe1/Repos/rathesan.iyadurai/kiro-gateway/debug_logs/`
- **Used for**: Comparing request/response formats during development

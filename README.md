# Kiro Proxy Go

OpenAI-compatible API proxy for Amazon Kiro.

## Installation

### Homebrew (macOS)

```bash
brew tap githendrik/tap
brew install kiro-proxy-go
```

### From Source

```bash
go install github.com/githendrik/kiro-proxy-go@latest
```

### Download Binary

Download from [GitHub Releases](https://github.com/githendrik/kiro-proxy-go/releases).

## Quick Start

1. **Login with kiro-cli:**
   ```bash
   kiro-cli login
   ```

2. **Start the proxy:**
   ```bash
   kiro-proxy-go start
   ```

3. **View logs:**
   ```bash
   kiro-proxy-go logs
   ```

4. **Stop the proxy:**
   ```bash
   kiro-proxy-go stop
   ```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `KIRO_CREDS_FILE` | Path to kiro-cli credentials file | `~/.aws/sso/cache/kiro-auth-token-cli.json` |
| `REFRESH_TOKEN` | Direct refresh token (alternative to file) | - |
| `KIRO_REGION` | Auth region | `us-east-1` |
| `KIRO_API_REGION` | API region override | - |
| `PROXY_API_KEY` | API key for proxy authentication | `my-super-secret-password-123` |
| `SERVER_HOST` | Listen host | `0.0.0.0` |
| `SERVER_PORT` | Listen port | `8000` |
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |

### Example .env File

```bash
KIRO_CREDS_FILE=~/.aws/sso/cache/kiro-auth-token-cli.json
KIRO_REGION=us-east-1
PROXY_API_KEY=your-secret-key
SERVER_PORT=8000
```

## Daemon Commands

| Command | Description |
|---------|-------------|
| `start` | Start as background daemon |
| `stop` | Stop the running daemon |
| `restart` | Restart the daemon |
| `logs` | View daemon logs |
| `run` | Run in foreground (default) |
| `help` | Show help message |

## Usage with OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8000/v1",
    api_key="my-super-secret-password-123"
)

response = client.chat.completions.create(
    model="kiro-code-interleave",
    messages=[
        {"role": "user", "content": "Hello!"}
    ]
)

print(response.choices[0].message.content)
```

## Development

### Build

```bash
go build -o kiro-proxy-go .
```

### Run in Foreground

```bash
./kiro-proxy-go run
```

### Release

```bash
git tag v0.2.0
git push --tags
```

GitHub Actions will automatically build binaries and create a release.

## License

MIT

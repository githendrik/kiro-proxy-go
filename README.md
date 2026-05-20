# Kiro Proxy

OpenAI-compatible API proxy for Amazon Kiro.

## Installation

### Homebrew (macOS)

```bash
brew tap githendrik/tap
brew install kiro-proxy
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
   kiro-proxy start
   ```

3. **View logs:**
   ```bash
   kiro-proxy logs
   ```

4. **Stop the proxy:**
   ```bash
   kiro-proxy stop
   ```

## Configuration

### Config File (Recommended)

Create `~/.config/kiro-proxy/config.yaml`:

```bash
mkdir -p ~/.config/kiro-proxy
cp config.example.yaml ~/.config/kiro-proxy/config.yaml
# Edit the file with your settings
```

**Example config:**
```yaml
# Credentials (use one method)
creds_file: ~/.aws/sso/cache/kiro-auth-token-cli.json
# refresh_token: your-refresh-token-here

# Server
host: 0.0.0.0
port: 8000

# Proxy auth
proxy_api_key: your-secret-key

# Region
region: us-east-1

# Logging
log_level: info
```

### Environment Variables

Environment variables override config file settings.

| Variable | Description | Default |
|----------|-------------|---------|
| `KIRO_CREDS_FILE` | Path to kiro-cli credentials file | - |
| `REFRESH_TOKEN` | Direct refresh token (alternative to file) | - |
| `KIRO_REGION` | Auth region | `us-east-1` |
| `KIRO_API_REGION` | API region override | - |
| `PROXY_API_KEY` | API key for proxy authentication | `my-super-secret-password-123` |
| `SERVER_HOST` | Listen host | `0.0.0.0` |
| `SERVER_PORT` | Listen port | `8000` |
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |

### Config File Locations

The proxy searches for config files in this order:
1. `./kiro-proxy.yaml` (current directory)
2. `~/.config/kiro-proxy/config.yaml` (recommended)
3. `~/.kiro-proxy.yaml`
4. `/etc/kiro-proxy/config.yaml`

Environment variables always take precedence over config file values.

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
go build -o kiro-proxy .
```

### Run in Foreground

```bash
./kiro-proxy run
```

### Release

```bash
git tag v0.2.0
git push --tags
```

GitHub Actions will automatically build binaries and create a release.

## License

MIT

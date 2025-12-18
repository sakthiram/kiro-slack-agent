# Kiro Slack Agent Web UI

Real-time web interface for monitoring and viewing active Kiro Slack Agent sessions with terminal output streaming.

## Features

- **Session List**: View all active sessions with status, user, and channel information
- **Terminal Viewer**: Watch live terminal output from any session using xterm.js
- **Real-time Updates**: Auto-refresh session list every 5 seconds
- **WebSocket Streaming**: Real-time terminal output via WebSocket connections
- **Observer Count**: See how many people are watching each session
- **Responsive Design**: Dark theme optimized for terminal viewing

## Quick Start

### 1. Enable Web UI

Edit your `config.yaml` to enable the web interface:

```yaml
web:
  enabled: true
  listen_addr: ":8080"
  static_path: "./web/static"
  max_observers_per_session: 10
```

### 2. Start the Server

```bash
./kiro-slack-agent -config config.yaml
```

### 3. Open in Browser

Navigate to: http://localhost:8080

## Usage

### Viewing Sessions

1. The left panel shows all active sessions
2. Each session displays:
   - Session ID (truncated for readability)
   - User ID and Channel ID
   - Status (active, processing, inactive)
   - Time since creation

### Watching Terminal Output

1. Click on any session in the list
2. The terminal panel will connect via WebSocket
3. You'll see:
   - Scrollback history (last 64KB of output)
   - Real-time streaming output
   - Connection status messages

### Controls

- **Refresh** button: Manually refresh the session list
- **Detach** button: Disconnect from current session
- **Clear** button: Clear the terminal display (doesn't affect session)

### Connection Management

The terminal viewer automatically:
- Connects to sessions via WebSocket
- Receives scrollback history on connect
- Attempts to reconnect up to 5 times if disconnected
- Shows connection status in terminal

## Architecture

### Components

```
web/
└── static/
    ├── index.html          # Main page layout
    ├── css/
    │   └── terminal.css    # Styles and dark theme
    └── js/
        ├── app.js          # Application logic
        ├── api.js          # REST API client
        └── terminal.js     # xterm.js wrapper
```

### API Endpoints

**REST API:**
- `GET /api/sessions` - List all sessions
- `GET /api/sessions/{id}` - Get session details
- `GET /api/health` - Health check

**WebSocket:**
- `WS /ws/sessions/{id}/stream` - Stream terminal output

### Message Format

**Control Messages (JSON):**
```json
{
  "type": "init|status|error",
  "session": "session-id",
  "status": "active",
  "timestamp": 1234567890
}
```

**Terminal Output:**
- Raw bytes sent directly to xterm.js
- Binary data from PTY output
- Base64 decoded automatically

## Terminal Features

Built on [xterm.js](https://xtermjs.org/) with:
- **10,000 line scrollback buffer**
- **ANSI color support** (16 colors)
- **VT100 escape sequences**
- **Auto-resize** on window resize
- **Dark theme** matching VS Code

## Configuration

### Web Config Options

```yaml
web:
  # Enable/disable web UI
  enabled: true

  # HTTP server listen address
  listen_addr: ":8080"

  # Path to static files (relative to binary)
  static_path: "./web/static"

  # Maximum concurrent observers per session
  max_observers_per_session: 10
```

### Environment Variables

Override config via environment variables:
```bash
export KIRO_AGENT_WEB_ENABLED=true
export KIRO_AGENT_WEB_LISTEN_ADDR=:8080
```

## Development

### Running Locally

```bash
# Start server with debug logging
./kiro-slack-agent -config config.yaml

# Open browser
open http://localhost:8080
```

### Testing WebSocket Connection

```javascript
// Browser console
const ws = new WebSocket('ws://localhost:8080/ws/sessions/{session-id}/stream');
ws.onmessage = (e) => console.log('Received:', e.data);
```

### Modifying Styles

Edit `web/static/css/terminal.css` for theme customization:
- CSS variables at `:root` for colors
- Dark theme optimized for terminal viewing
- Responsive layout with flexbox

## Browser Support

Tested on:
- Chrome 90+
- Firefox 88+
- Safari 14+
- Edge 90+

## Dependencies

**CDN Resources (no build step required):**
- xterm.js 5.3.0 - Terminal emulator
- xterm-addon-fit 0.8.0 - Terminal resizing

## Troubleshooting

### Connection Issues

**Problem:** "Disconnected" status or connection fails
- Verify web server is running
- Check `web.enabled: true` in config
- Ensure no firewall blocking port 8080

**Problem:** Session not found
- Session may have expired (check `idle_timeout`)
- Session ID may be incorrect
- Use "Refresh" button to update session list

### Terminal Display Issues

**Problem:** Text doesn't fit terminal
- Resize browser window to trigger auto-fit
- Click "Clear" and reconnect to session

**Problem:** No scrollback history
- Observable process buffer is 64KB
- Very active sessions may overflow buffer
- Connect earlier to see more history

### Performance

**Problem:** Slow updates or lag
- Check network latency
- Reduce `max_observers_per_session` if server overloaded
- Consider increasing WebSocket buffer size

## Security Notes

⚠️ **Important Security Considerations:**

1. **No Authentication**: Current implementation has no auth
   - Use firewall/VPN to restrict access
   - Or add authentication middleware

2. **Open CORS**: WebSocket accepts all origins
   - For development only
   - Add origin checking for production

3. **No Encryption**: HTTP only (no TLS)
   - Use reverse proxy (nginx) for HTTPS
   - Or add TLS configuration to server

## Future Enhancements

Potential improvements:
- Session filtering and search
- Terminal input (send commands to session)
- Download terminal output as text
- Session recording and playback
- Multi-session grid view
- Authentication and authorization
- Metrics and analytics dashboard

## License

Same as parent project.

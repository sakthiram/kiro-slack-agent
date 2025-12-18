/**
 * TerminalViewer - Wrapper around xterm.js for displaying Kiro session output
 */

class TerminalViewer {
    constructor(containerElement) {
        this.container = containerElement;
        this.terminal = null;
        this.fitAddon = null;
        this.ws = null;
        this.sessionId = null;
        this.onDisconnect = null;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 5;
        this.reconnectDelay = 2000;
        this.reconnectTimer = null;

        this.initialize();
    }

    /**
     * Initialize the xterm.js terminal
     */
    initialize() {
        // Create terminal with dark theme
        this.terminal = new Terminal({
            cursorBlink: false,
            fontSize: 14,
            fontFamily: 'Consolas, Monaco, "Courier New", monospace',
            theme: {
                background: '#1e1e1e',
                foreground: '#cccccc',
                cursor: '#cccccc',
                black: '#000000',
                red: '#cd3131',
                green: '#0dbc79',
                yellow: '#e5e510',
                blue: '#2472c8',
                magenta: '#bc3fbc',
                cyan: '#11a8cd',
                white: '#e5e5e5',
                brightBlack: '#666666',
                brightRed: '#f14c4c',
                brightGreen: '#23d18b',
                brightYellow: '#f5f543',
                brightBlue: '#3b8eea',
                brightMagenta: '#d670d6',
                brightCyan: '#29b8db',
                brightWhite: '#e5e5e5'
            },
            scrollback: 10000,
            convertEol: true
        });

        // Create and load fit addon
        this.fitAddon = new FitAddon.FitAddon();
        this.terminal.loadAddon(this.fitAddon);

        // Open terminal in container
        this.terminal.open(this.container);
        this.fitAddon.fit();

        // Handle window resize
        this.resizeHandler = () => {
            if (this.fitAddon) {
                this.fitAddon.fit();
            }
        };
        window.addEventListener('resize', this.resizeHandler);

        console.log('Terminal initialized');
    }

    /**
     * Connect to a session's WebSocket stream
     * @param {string} sessionId - The session ID to connect to
     */
    connect(sessionId) {
        // Close existing connection if any
        this.disconnect();

        this.sessionId = sessionId;
        this.reconnectAttempts = 0;

        this._createWebSocket();
    }

    /**
     * Create WebSocket connection
     * @private
     */
    _createWebSocket() {
        // Clear terminal
        this.terminal.clear();

        // Build WebSocket URL
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws/sessions/${this.sessionId}/stream`;

        console.log('Connecting to WebSocket:', wsUrl);

        try {
            this.ws = new WebSocket(wsUrl);

            this.ws.onopen = () => {
                console.log('WebSocket connected');
                this.reconnectAttempts = 0;
                this.write('\r\n\x1b[32m[Connected to session]\x1b[0m\r\n\r\n');
            };

            this.ws.onmessage = (event) => {
                try {
                    // Try to parse as JSON first (for control messages)
                    const data = JSON.parse(event.data);
                    this._handleControlMessage(data);
                } catch (e) {
                    // Not JSON, treat as raw terminal output
                    this.write(event.data);
                }
            };

            this.ws.onerror = (error) => {
                console.error('WebSocket error:', error);
                this.write('\r\n\x1b[31m[Connection error]\x1b[0m\r\n');
            };

            this.ws.onclose = (event) => {
                console.log('WebSocket closed:', event.code, event.reason);
                this.write('\r\n\x1b[33m[Disconnected from session]\x1b[0m\r\n');

                // Attempt to reconnect if not a clean close
                if (event.code !== 1000 && this.reconnectAttempts < this.maxReconnectAttempts) {
                    this.reconnectAttempts++;
                    this.write(`\r\n\x1b[33m[Reconnecting (${this.reconnectAttempts}/${this.maxReconnectAttempts})...]\x1b[0m\r\n`);

                    this.reconnectTimer = setTimeout(() => {
                        if (this.sessionId) {
                            this._createWebSocket();
                        }
                    }, this.reconnectDelay);
                } else if (this.reconnectAttempts >= this.maxReconnectAttempts) {
                    this.write('\r\n\x1b[31m[Max reconnection attempts reached]\x1b[0m\r\n');
                }

                // Notify parent if callback is set
                if (this.onDisconnect) {
                    this.onDisconnect();
                }
            };
        } catch (error) {
            console.error('Failed to create WebSocket:', error);
            this.write('\r\n\x1b[31m[Failed to connect]\x1b[0m\r\n');
        }
    }

    /**
     * Handle control messages from server
     * @private
     */
    _handleControlMessage(data) {
        console.log('Control message:', data);

        switch (data.type) {
            case 'init':
                // Initial connection info
                this.write(`\r\n\x1b[36m[Session: ${data.session}]\x1b[0m\r\n`);
                this.write(`\x1b[36m[Status: ${data.status}]\x1b[0m\r\n\r\n`);
                break;

            case 'status':
                // Status update
                this.write(`\r\n\x1b[36m[Status changed: ${data.status}]\x1b[0m\r\n`);
                break;

            case 'error':
                // Error message
                this.write(`\r\n\x1b[31m[Error: ${data.message}]\x1b[0m\r\n`);
                break;

            default:
                console.warn('Unknown control message type:', data.type);
        }
    }

    /**
     * Write data to the terminal
     * @param {string} data - Data to write (raw bytes or text)
     */
    write(data) {
        if (this.terminal) {
            this.terminal.write(data);
        }
    }

    /**
     * Clear the terminal
     */
    clear() {
        if (this.terminal) {
            this.terminal.clear();
        }
    }

    /**
     * Disconnect from current session
     */
    disconnect() {
        // Clear reconnect timer
        if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
        }

        // Close WebSocket
        if (this.ws) {
            this.ws.close(1000, 'User disconnected');
            this.ws = null;
        }

        this.sessionId = null;
        this.reconnectAttempts = 0;
    }

    /**
     * Check if currently connected
     * @returns {boolean}
     */
    isConnected() {
        return this.ws && this.ws.readyState === WebSocket.OPEN;
    }

    /**
     * Dispose of the terminal and cleanup
     */
    dispose() {
        window.removeEventListener('resize', this.resizeHandler);
        this.disconnect();

        if (this.terminal) {
            this.terminal.dispose();
            this.terminal = null;
        }

        if (this.fitAddon) {
            this.fitAddon = null;
        }
    }
}

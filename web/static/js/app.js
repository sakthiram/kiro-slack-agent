/**
 * Main application logic for Kiro Slack Agent web UI
 */

class App {
    constructor() {
        this.terminal = null;
        this.currentSessionId = null;
        this.sessions = [];
        this.refreshInterval = null;
        this.refreshRate = 5000; // 5 seconds

        this.init();
    }

    /**
     * Initialize the application
     */
    async init() {
        console.log('Initializing Kiro Slack Agent Web UI');

        // Initialize terminal viewer
        const terminalContainer = document.getElementById('terminal-container');
        this.terminal = new TerminalViewer(terminalContainer);

        // Set disconnect callback
        this.terminal.onDisconnect = () => {
            this.updateConnectionStatus(false);
        };

        // Setup event listeners
        this.setupEventListeners();

        // Load initial session list
        await this.loadSessions();

        // Start auto-refresh
        this.startAutoRefresh();

        // Check health
        this.checkHealth();
    }

    /**
     * Setup event listeners for UI interactions
     */
    setupEventListeners() {
        // Refresh button
        document.getElementById('refresh-sessions').addEventListener('click', () => {
            this.loadSessions();
        });

        // Detach button
        document.getElementById('detach-button').addEventListener('click', () => {
            this.detachSession();
        });

        // Clear button
        document.getElementById('clear-button').addEventListener('click', () => {
            if (this.terminal) {
                this.terminal.clear();
            }
        });
    }

    /**
     * Load sessions from API and update UI
     */
    async loadSessions() {
        try {
            const response = await API.getSessions();
            this.sessions = response.sessions || [];
            this.renderSessionList();
            this.updateConnectionStatus(true);
        } catch (error) {
            console.error('Failed to load sessions:', error);
            this.showError('Failed to load sessions');
            this.updateConnectionStatus(false);
        }
    }

    /**
     * Render the session list in the sidebar
     */
    renderSessionList() {
        const sessionList = document.getElementById('session-list');

        if (this.sessions.length === 0) {
            sessionList.innerHTML = '<div class="loading">No active sessions</div>';
            return;
        }

        sessionList.innerHTML = this.sessions.map(session => {
            const isActive = session.id === this.currentSessionId;
            const createdDate = new Date(session.created_at * 1000);
            const timeAgo = this.formatTimeAgo(createdDate);

            return `
                <div class="session-item ${isActive ? 'active' : ''}" data-session-id="${session.id}">
                    <div class="session-id">${this.truncateId(session.id)}</div>
                    <div class="session-info">
                        <div class="session-info-row">
                            <span class="session-info-label">User:</span>
                            <span class="session-info-value">${session.user_id}</span>
                        </div>
                        <div class="session-info-row">
                            <span class="session-info-label">Channel:</span>
                            <span class="session-info-value">${session.channel_id}</span>
                        </div>
                    </div>
                    <div class="session-meta">
                        <span class="session-status ${session.status.toLowerCase()}">${session.status}</span>
                        <span class="session-time" title="${createdDate.toLocaleString()}">${timeAgo}</span>
                    </div>
                </div>
            `;
        }).join('');

        // Add click handlers
        sessionList.querySelectorAll('.session-item').forEach(item => {
            item.addEventListener('click', () => {
                const sessionId = item.dataset.sessionId;
                this.attachSession(sessionId);
            });
        });
    }

    /**
     * Attach to a session and start viewing terminal output
     * @param {string} sessionId - Session ID to attach to
     */
    async attachSession(sessionId) {
        if (this.currentSessionId === sessionId) {
            return; // Already attached
        }

        console.log('Attaching to session:', sessionId);

        try {
            // Get session details first to verify it exists
            const session = await API.getSession(sessionId);

            // Update UI
            this.currentSessionId = sessionId;
            this.updateTerminalTitle(sessionId);
            this.showTerminal();

            // Connect terminal to WebSocket
            this.terminal.connect(sessionId);

            // Enable detach button
            document.getElementById('detach-button').disabled = false;

            // Update session list to show active state
            this.renderSessionList();

            // Update observer count
            this.updateObserverCount(session.observer_count);

        } catch (error) {
            console.error('Failed to attach to session:', error);
            this.showError(`Failed to attach to session: ${error.message}`);
        }
    }

    /**
     * Detach from current session
     */
    detachSession() {
        if (!this.currentSessionId) {
            return;
        }

        console.log('Detaching from session:', this.currentSessionId);

        // Disconnect terminal
        this.terminal.disconnect();

        // Update UI
        this.currentSessionId = null;
        this.updateTerminalTitle(null);
        this.hideTerminal();

        // Disable detach button
        document.getElementById('detach-button').disabled = true;

        // Update session list
        this.renderSessionList();

        // Clear observer count
        this.updateObserverCount(0);
    }

    /**
     * Update the terminal panel title
     * @param {string|null} sessionId - Current session ID or null
     */
    updateTerminalTitle(sessionId) {
        const title = document.getElementById('terminal-title');
        if (sessionId) {
            title.textContent = `Terminal Output - ${this.truncateId(sessionId)}`;
        } else {
            title.textContent = 'Terminal Output';
        }
    }

    /**
     * Show the terminal and hide placeholder
     */
    showTerminal() {
        document.getElementById('terminal-container').classList.add('active');
        document.getElementById('terminal-placeholder').classList.add('hidden');
    }

    /**
     * Hide the terminal and show placeholder
     */
    hideTerminal() {
        document.getElementById('terminal-container').classList.remove('active');
        document.getElementById('terminal-placeholder').classList.remove('hidden');
    }

    /**
     * Update connection status indicator
     * @param {boolean} connected - Whether connected to server
     */
    updateConnectionStatus(connected) {
        const status = document.getElementById('connection-status');
        if (connected) {
            status.textContent = 'Connected';
            status.className = 'status-indicator status-connected';
        } else {
            status.textContent = 'Disconnected';
            status.className = 'status-indicator status-disconnected';
        }
    }

    /**
     * Update observer count display
     * @param {number} count - Number of observers
     */
    updateObserverCount(count) {
        const element = document.getElementById('observer-count');
        if (count > 0) {
            element.textContent = `${count} observer${count !== 1 ? 's' : ''}`;
            element.style.display = 'inline-block';
        } else {
            element.style.display = 'none';
        }
    }

    /**
     * Start auto-refresh of session list
     */
    startAutoRefresh() {
        this.refreshInterval = setInterval(() => {
            this.loadSessions();

            // If attached, refresh observer count
            if (this.currentSessionId) {
                API.getSession(this.currentSessionId).then(session => {
                    this.updateObserverCount(session.observer_count);
                }).catch(err => {
                    console.error('Failed to refresh session details:', err);
                });
            }
        }, this.refreshRate);
    }

    /**
     * Stop auto-refresh
     */
    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
            this.refreshInterval = null;
        }
    }

    /**
     * Check server health
     */
    async checkHealth() {
        try {
            const health = await API.getHealth();
            console.log('Server health:', health);
        } catch (error) {
            console.error('Health check failed:', error);
        }
    }

    /**
     * Show error message in session list
     * @param {string} message - Error message
     */
    showError(message) {
        const sessionList = document.getElementById('session-list');
        sessionList.innerHTML = `<div class="error-message">${message}</div>`;
    }

    /**
     * Truncate session ID for display
     * @param {string} id - Full session ID
     * @returns {string} Truncated ID
     */
    truncateId(id) {
        if (id.length > 20) {
            return id.substring(0, 17) + '...';
        }
        return id;
    }

    /**
     * Format timestamp as relative time
     * @param {Date} date - Date to format
     * @returns {string} Relative time string
     */
    formatTimeAgo(date) {
        const seconds = Math.floor((new Date() - date) / 1000);

        if (seconds < 60) return 'just now';

        const minutes = Math.floor(seconds / 60);
        if (minutes < 60) return `${minutes}m ago`;

        const hours = Math.floor(minutes / 60);
        if (hours < 24) return `${hours}h ago`;

        const days = Math.floor(hours / 24);
        return `${days}d ago`;
    }

    /**
     * Cleanup and dispose
     */
    dispose() {
        this.stopAutoRefresh();
        if (this.terminal) {
            this.terminal.dispose();
        }
    }
}

// Initialize app when DOM is ready
let app;
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
        app = new App();
    });
} else {
    app = new App();
}

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    if (app) {
        app.dispose();
    }
});

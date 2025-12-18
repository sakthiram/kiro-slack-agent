/**
 * REST API client for Kiro Slack Agent backend
 */

const API = {
    /**
     * Get list of all active sessions
     * @returns {Promise<Object>} Response with sessions array and total count
     */
    async getSessions() {
        try {
            const response = await fetch('/api/sessions');
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error('Failed to fetch sessions:', error);
            throw error;
        }
    },

    /**
     * Get detailed information about a specific session
     * @param {string} sessionId - The session ID
     * @returns {Promise<Object>} Session details
     */
    async getSession(sessionId) {
        try {
            const response = await fetch(`/api/sessions/${sessionId}`);
            if (!response.ok) {
                if (response.status === 404) {
                    throw new Error('Session not found');
                }
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error(`Failed to fetch session ${sessionId}:`, error);
            throw error;
        }
    },

    /**
     * Check server health
     * @returns {Promise<Object>} Health status
     */
    async getHealth() {
        try {
            const response = await fetch('/api/health');
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error('Health check failed:', error);
            throw error;
        }
    }
};

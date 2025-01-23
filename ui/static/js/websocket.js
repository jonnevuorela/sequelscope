function connectWebSocket() {
    let reconnectAttempts = 0;
    const maxReconnectAttempts = 5;

    function connect() {
        console.log('Attempting WebSocket connection...');

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

        ws.onopen = function() {
            console.log('WebSocket connection established');
            reconnectAttempts = 0; // Reset attempts on successful connection
        };

        ws.onmessage = function(event) {
            try {
                const data = JSON.parse(event.data);
                console.log('Received database change:', data);

                // notification before reload
                const message = `${data.type === 'query' ? 'Query executed' : 'Data changed'} in ${data.database}`;
                console.log(message);

                window.location.reload();
            } catch (error) {
                console.error('Error processing WebSocket message:', error);
            }
        };

        ws.onclose = function(event) {
            console.log('WebSocket connection closed. Code:', event.code, 'Reason:', event.reason);

            if (reconnectAttempts < maxReconnectAttempts) {
                const timeout = Math.min(1000 * Math.pow(2, reconnectAttempts), 10000);
                console.log(`Attempting to reconnect in ${timeout/1000} seconds...`);
                reconnectAttempts++;
                setTimeout(connect, timeout);
            } else {
                console.log('Max reconnection attempts reached. Please refresh the page manually.');
            }
        };

        ws.onerror = function(error) {
            console.error('WebSocket error occurred:', error);
        };
    }

    connect();
}

// Initialize WebSocket connection when DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    console.log('DOM loaded, initializing WebSocket...');
    connectWebSocket();
});


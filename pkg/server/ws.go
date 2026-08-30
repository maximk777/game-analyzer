package server

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSMessageType represents the type of WebSocket message.
type WSMessageType string

const (
	WSMsgStateUpdate    WSMessageType = "state_update"
	WSMsgRecommendation WSMessageType = "recommendation"
	WSMsgEvent          WSMessageType = "event"
	WSMsgPing           WSMessageType = "ping"
	WSMsgPong           WSMessageType = "pong"
	WSMsgError          WSMessageType = "error"
)

// WSMessage is the envelope for all WebSocket messages.
type WSMessage struct {
	Type      WSMessageType `json:"type"`
	TableID   string        `json:"table_id,omitempty"`
	Payload   any           `json:"payload,omitempty"`
	Timestamp int64         `json:"timestamp"`
	// Reason says why a recommendation is absent, when it is.
	//
	// The interface had one message for every case and it was the wrong one
	// more often than not: at a showdown, with hero's cards plainly on screen
	// and shown in its own panel, it read "no hero cards". Saying "no advice"
	// without saying why reads as a fault in the tool, and a player cannot tell
	// a fault from a hand that is simply over.
	Reason string `json:"reason,omitempty"`
}

const (
	writeWait      = 5 * time.Second
	pongWait       = 10 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024
	sendBufferSize = 256
)

// WSClient represents a single connected WebSocket subscriber.
type WSClient struct {
	hub       *WSHub
	conn      *websocket.Conn
	tableID   string
	send      chan []byte
	closeOnce sync.Once
	closed    bool
}

// NewWSClient creates a new WSClient instance.
func NewWSClient(hub *WSHub, conn *websocket.Conn, tableID string) *WSClient {
	return &WSClient{
		hub:     hub,
		conn:    conn,
		tableID: tableID,
		send:    make(chan []byte, sendBufferSize),
	}
}

// SendMessage enqueues a JSON-marshaled WSMessage to the client's send channel.
func (c *WSClient) SendMessage(msg WSMessage) {
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	c.sendBytes(data)
}

func (c *WSClient) sendBytes(data []byte) {
	if c.closed {
		return
	}
	select {
	case c.send <- data:
	default:
		// Buffer full - drop or unregister slow client
	}
}

// Close gracefully closes the client connection and send channel.
func (c *WSClient) Close() {
	c.closeOnce.Do(func() {
		c.closed = true
		c.hub.Unregister(c)
		_ = c.conn.Close()
	})
}

func (c *WSClient) readPump() {
	defer c.Close()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	c.conn.SetPingHandler(func(appData string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		err := c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeWait))
		if err == websocket.ErrCloseSent {
			return nil
		}
		return err
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *WSClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// WSHub maintains active client subscriptions and routes broadcasts by table ID.
type WSHub struct {
	mu           sync.RWMutex
	tableClients map[string]map[*WSClient]bool
	allClients   map[*WSClient]bool
	closed       bool
}

// NewWSHub creates an initialized WSHub instance.
func NewWSHub() *WSHub {
	return &WSHub{
		tableClients: make(map[string]map[*WSClient]bool),
		allClients:   make(map[*WSClient]bool),
	}
}

// Register registers a client to the hub and its associated table.
func (h *WSHub) Register(c *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}

	h.allClients[c] = true
	if c.tableID != "" {
		if _, exists := h.tableClients[c.tableID]; !exists {
			h.tableClients[c.tableID] = make(map[*WSClient]bool)
		}
		h.tableClients[c.tableID][c] = true
	}
}

// Unregister removes a client from the hub.
func (h *WSHub) Unregister(c *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.allClients, c)
	if c.tableID != "" {
		if clients, exists := h.tableClients[c.tableID]; exists {
			delete(clients, c)
			if len(clients) == 0 {
				delete(h.tableClients, c.tableID)
			}
		}
	}
}

// BroadcastToTable sends a message to all clients subscribed to a specific table ID.
func (h *WSHub) BroadcastToTable(tableID string, msg WSMessage) {
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return
	}

	sentClients := make(map[*WSClient]bool)

	// 1. Send to clients specifically subscribed to tableID
	if clients, exists := h.tableClients[tableID]; exists {
		for c := range clients {
			c.sendBytes(data)
			sentClients[c] = true
		}
	}

	// 2. Also send to generic live subscribers (coinpoker-live, live, *, table-1)
	for _, liveKey := range []string{"coinpoker-live", "live", "*", "table-1"} {
		if clients, exists := h.tableClients[liveKey]; exists {
			for c := range clients {
				if !sentClients[c] {
					c.sendBytes(data)
					sentClients[c] = true
				}
			}
		}
	}
}

// BroadcastAll sends a message to all connected clients across all tables.
func (h *WSHub) BroadcastAll(msg WSMessage) {
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return
	}

	for c := range h.allClients {
		c.sendBytes(data)
	}
}

// ClientCount returns the number of clients subscribed to a specific table ID.
func (h *WSHub) ClientCount(tableID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, exists := h.tableClients[tableID]; exists {
		return len(clients)
	}
	return 0
}

// TotalClientCount returns the total number of connected clients.
func (h *WSHub) TotalClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.allClients)
}

// Close disconnects all clients and closes the hub.
func (h *WSHub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true

	var clientsToClose []*WSClient
	for c := range h.allClients {
		clientsToClose = append(clientsToClose, c)
	}
	h.allClients = make(map[*WSClient]bool)
	h.tableClients = make(map[string]map[*WSClient]bool)
	h.mu.Unlock()

	for _, c := range clientsToClose {
		c.closed = true
		_ = c.conn.Close()
	}
}

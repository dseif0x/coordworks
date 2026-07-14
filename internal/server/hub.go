package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSEvent is what the frontend receives; Type tells it what to refetch.
type WSEvent struct {
	Type    string `json:"type"` // task.updated, plan.updated, agent.updated, activity.new, runner.updated, stats.updated
	Payload any    `json:"payload,omitempty"`
}

// Hub fan-outs events to all connected UI clients.
type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]chan []byte
	log     *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{clients: map[*websocket.Conn]chan []byte{}, log: log}
}

// Broadcast queues an event to every connected client; slow clients drop
// events rather than blocking the server.
func (h *Hub) Broadcast(eventType string, payload any) {
	data, err := json.Marshal(WSEvent{Type: eventType, Payload: payload})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

var upgrader = websocket.Upgrader{
	// The UI may be served from a dev server on another port.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.clients[conn] = ch
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
		conn.Close()
	}()

	// Reader: discard client messages, detect disconnect.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				conn.Close()
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

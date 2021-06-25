package sdp

import "sync"

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clients    map[*Client]bool
	clientlock sync.RWMutex

	sessions []map[string]string
	sesslock sync.RWMutex

	// Inbound messages from the clients.
	broadcast chan []byte

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		sessions:   make([]map[string]string, 0),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Leave(client *Client) {
	h.clientlock.RLock()
	clients := h.clients
	h.clientlock.RUnlock()
	close(client.send)
	client.conn.Close()
	if _, ok := clients[client]; ok {
		h.clientlock.Lock()
		delete(h.clients, client)
		h.clientlock.Unlock()
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clientlock.Lock()
			h.clients[client] = true
			h.clientlock.Unlock()
		case client := <-h.unregister:
			h.Leave(client)
		case message := <-h.broadcast:
			h.clientlock.RLock()
			clients := h.clients
			h.clientlock.RUnlock()
			for client := range clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					h.clientlock.Lock()
					delete(h.clients, client)
					h.clientlock.Unlock()
				}
			}
		}
	}
}

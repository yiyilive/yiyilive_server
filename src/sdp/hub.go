package sdp

import "C"
import "sync"

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clients sync.Map

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
		sessions:   make([]map[string]string, 0),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients.Store(client, true)
		case client := <-h.unregister:
			if _, ok := h.clients.Load(client); ok {
				h.clients.Delete(client)
				close(client.send)
			}
		case message := <-h.broadcast:
			h.clients.Range(func(key, value interface{}) bool {
				client := key.(*Client)
				select {
				case client.send <- message:
				default:
					close(client.send)
					h.clients.Delete(client)
				}
				return true
			})
		}
	}
}

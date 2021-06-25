package sdp

import "sync"

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clients *sync.Map //map[*Client]bool

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
		clients:    &sync.Map{},
		sessions:   make([]map[string]string, 0),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Leave(client *Client) {
	//close(client.send)
	_ = client.conn.Close()
	h.clients.Delete(client)
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients.Store(client, true)
		case client := <-h.unregister:
			h.Leave(client)
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

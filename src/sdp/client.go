package sdp

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1024 * 8
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024 * 8,
	WriteBufferSize: 1024 * 8,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	Id        string `json:"id"`
	Name      string `json:"name"`
	UserAgent string `json:"user_agent"`
	SessionId string `json:"session_id"`
}

func (c *Client) updatePeers() {
	peers := make([]*Client, 0)
	c.hub.clients.Range(func(key, value interface{}) bool {
		peers = append(peers, key.(*Client))
		return true
	})
	res := make(map[string]interface{})
	res["type"] = "peers"
	res["data"] = peers
	msg, _ := json.Marshal(&res)
	c.hub.broadcast <- msg
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c

		res := make(map[string]interface{})
		res["type"] = "leave"
		res["data"] = c.Id
		sendMsg, _ := json.Marshal(&res)
		c.hub.broadcast <- sendMsg
		c.updatePeers()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { _ = c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			return
		}
		var msg map[string]interface{}
		err = json.Unmarshal(message, &msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			return
		}

		switch msg["type"].(string) {
		case "new":
			c.Id = msg["id"].(string)
			c.Name = msg["name"].(string)
			c.UserAgent = msg["user_agent"].(string)
			c.updatePeers()
			break
		case "bye":
			sessId := msg["session_id"].(string)
			var session map[string]string
			c.hub.sesslock.RLock()
			sesss := c.hub.sessions
			c.hub.sesslock.RUnlock()
			for _, v := range sesss {
				if v["id"] == sessId {
					session = v
				}
			}
			if session == nil {
				res := make(map[string]interface{})
				res["type"] = "error"
				ers := make(map[string]interface{})
				ers["error"] = "Invalid session " + sessId
				res["data"] = ers
				sendMsg, _ := json.Marshal(&res)
				c.send <- sendMsg
				return
			}
			c.hub.clients.Range(func(key, value interface{}) bool {
				client := key.(*Client)
				if client.SessionId == sessId {
					from := msg["from"].(string)
					to := ""
					if client.Id == from {
						to = session["to"]
					} else {
						to = session["form"]
					}
					res := make(map[string]interface{})
					res["type"] = "bye"
					data := make(map[string]interface{})
					data["session_id"] = sessId
					data["from"] = from
					data["to"] = to
					res["data"] = data
					sendMsg, _ := json.Marshal(&res)
					client.send <- sendMsg
				}
				return true
			})
			break
		case "offer":
			sessId := msg["session_id"].(string)
			var peer *Client
			to := msg["to"].(string)
			c.hub.clients.Range(func(key, value interface{}) bool {
				client := key.(*Client)
				if client.Id == to {
					peer = client
					return false
				}
				return true
			})
			if peer != nil {
				res := make(map[string]interface{})
				res["type"] = "offer"
				data := make(map[string]interface{})
				data["session_id"] = sessId
				data["from"] = c.Id
				data["to"] = peer
				data["media"] = msg["media"].(string)
				data["description"] = msg["description"].(map[string]interface{})
				res["data"] = data
				sendMsg, _ := json.Marshal(&res)
				peer.send <- sendMsg

				peer.SessionId = sessId
				c.SessionId = sessId

				ses := make(map[string]string)
				ses["id"] = sessId
				ses["from"] = c.Id
				ses["to"] = peer.Id
				c.hub.sesslock.Lock()
				c.hub.sessions = append(c.hub.sessions, ses)
				c.hub.sesslock.Unlock()
			}
			break
		case "answer":
			sessId := msg["session_id"].(string)
			to := msg["to"].(string)

			res := make(map[string]interface{})
			res["type"] = "answer"
			data := make(map[string]interface{})
			data["session_id"] = sessId
			data["from"] = c.Id
			data["to"] = to
			data["description"] = msg["description"].(map[string]interface{})
			res["data"] = data
			sendMsg, _ := json.Marshal(&res)

			c.hub.clients.Range(func(key, value interface{}) bool {
				client := key.(*Client)
				if client.Id == to && c.SessionId == sessId {
					client.send <- sendMsg
					return false
				}
				return true
			})
			break
		case "candidate":
			sessId := msg["session_id"].(string)
			to := msg["to"].(string)

			res := make(map[string]interface{})
			res["type"] = "candidate"
			data := make(map[string]interface{})
			data["session_id"] = sessId
			data["from"] = c.Id
			data["to"] = to
			data["candidate"] = msg["candidate"].(map[string]interface{})
			res["data"] = data
			sendMsg, _ := json.Marshal(&res)

			c.hub.clients.Range(func(key, value interface{}) bool {
				client := key.(*Client)
				if client.Id == to && c.SessionId == sessId {
					client.send <- sendMsg
					return false
				}
				return true
			})
			break
		case "keepalive":
			res := make(map[string]interface{})
			res["type"] = "keepalive"
			res["data"] = nil
			sendMsg, _ := json.Marshal(&res)
			c.send <- sendMsg
			break
		default:
			log.Println("Unhandled message")
		}
	}
}

var (
	newline = []byte{'\n'}
)

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				_, _ = w.Write(newline)
				_, _ = w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
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

// serveWs handles websocket requests from the peer.
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 1024*8)}
	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}

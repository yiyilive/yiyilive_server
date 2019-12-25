package sdp

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/bson"
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
	maxMessageSize = 1024*8
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024*8,
	WriteBufferSize: 1024*8,
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
	for k := range c.hub.clients {
		peers = append(peers, k)
	}
	res := bson.M{"type": "peers", "data": peers}
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
		c.conn.Close()

		delete(c.hub.clients, c)
		res := bson.M{"type": "leave", "data": c.Id}
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
			continue
		}
		var msg map[string]interface{}
		err = json.Unmarshal(message, &msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
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
			for _, v := range c.hub.sessions {
				if v["id"] == sessId {
					session = v
				}
			}
			if session == nil {
				res := bson.M{"type": "error", "data": bson.M{"error": "Invalid session " + sessId}}
				sendMsg, _ := json.Marshal(&res)
				c.send <- []byte(sendMsg)
				return
			}
			for k := range c.hub.clients {
				if k.SessionId == sessId {
					form := msg["from"].(string)
					to := ""
					if k.SessionId == form {
						to = msg["to"].(string)
					} else {
						to = form
					}
					res := bson.M{"type": "bye", "data": bson.M{"session_id": sessId, "from": form, "to": to}}
					sendMsg, _ := json.Marshal(&res)
					k.send <- []byte(sendMsg)
				}
			}
			break
		case "offer":
			sessId := msg["session_id"].(string)
			var peer *Client
			to := msg["to"].(string)
			for k := range c.hub.clients {
				if k.Id == to {
					peer = k
				}
			}
			if peer != nil {
				res := bson.M{"type": "offer",
					"data": bson.M{"to": peer.Id, "from": c.Id,
						"media": msg["media"].(string), "session_id": sessId,
						"description": msg["description"].(map[string]interface{})}}
				sendMsg, _ := json.Marshal(&res)
				peer.send <- []byte(sendMsg)

				peer.SessionId = sessId
				c.SessionId = sessId

				ses := make(map[string]string)
				ses["id"] = sessId
				ses["from"] = c.Id
				ses["to"] = peer.Id
				c.hub.sessions = append(c.hub.sessions, ses)
			}
			break
		case "answer":
			sessId := msg["session_id"].(string)
			to := msg["to"].(string)
			res := bson.M{"type": "answer",
				"data": bson.M{"to": to, "from": c.Id,
					"description": msg["description"].(map[string]interface{})}}
			sendMsg, _ := json.Marshal(&res)
			for k := range c.hub.clients {
				if k.Id == to && c.SessionId == sessId {
					k.send <- sendMsg
				}
			}
			break
		case "candidate":
			sessId := msg["session_id"].(string)
			to := msg["to"].(string)
			res := bson.M{"type": "candidate",
				"data": bson.M{"to": to, "from": c.Id,
					"candidate": msg["candidate"].(map[string]interface{})}}
			sendMsg, _ := json.Marshal(&res)
			for k := range c.hub.clients {
				if k.Id == to && c.SessionId == sessId {
					k.send <- sendMsg
				}
			}
			break
		case "keepalive":
			res := bson.M{"type": "keepalive", "data": bson.M{}}
			sendMsg, _ := json.Marshal(&res)
			c.send <- sendMsg
			break
		default:
			log.Println("Unhandled message")
		}
	}
}

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
				//_, _ = w.Write(newline)
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

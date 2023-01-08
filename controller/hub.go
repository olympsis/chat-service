package hub

import (
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Hub is in charge of managing connections and rooms
type Hub struct {
	Clients    map[*Client]bool
	Rooms      map[string]*Room
	Register   chan *Client
	Unregister chan *Client
	Log        *logrus.Logger
}

// Client is a client that is connected to the server via a websocket
type Client struct {
	UUID string
	Room string
	Conn *websocket.Conn
}

// Clients are joined together into rooms where they can recive packets from the sockets
type Room struct {
	Name       string
	Clients    []*Client
	Broadcast  chan Message
	Register   chan *Client
	Unregister chan *Client
	Log        *logrus.Logger
}

type Message struct {
	ID        primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	From      string             `json:"from" bson:"from"`
	Type      string             `json:"type" bson:"type"`
	Body      string             `json:"body" bson:"body"`
	Timestamp int64              `json:"timestamp" bson:"timestamp"`
}

// creates new instance of Hub
func NewHub(l *logrus.Logger) *Hub {
	return &Hub{
		Clients:    make(map[*Client]bool),
		Rooms:      make(map[string]*Room),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Log:        l,
	}
}

// creates a new instance of room
func (h *Hub) NewRoom(n string) *Room {
	return &Room{
		Name:       n,
		Clients:    make([]*Client, 0),
		Broadcast:  make(chan Message),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Log:        h.Log,
	}
}

func (h *Hub) Run() {
	h.Log.Info("Hub started running...")
	for {
		// clean up groups that have no clients listening to them
		for k, v := range h.Rooms {
			if len(v.Clients) == 0 {
				h.Log.Info("Deleting empty room: " + k)
				delete(h.Rooms, k)
			}
		}
		select {
		case client := <-h.Register:
			// check if room exists
			room, ok := h.Rooms[client.Room]
			if !ok {
				// create a new room if it doesn't exist.
				rm := h.NewRoom(client.Room)
				h.Rooms[rm.Name] = rm
				h.Log.Info("New Room Created: " + rm.Name)
				go rm.Run(&h.Unregister)
				rm.Register <- client
			} else {
				// if it does exist send user to room
				room.Register <- client
			}

		case client := <-h.Unregister:
			h.Log.Info(client.UUID + " has disconnected from server.")
		}
	}
}

func (r *Room) Run(channel *chan *Client) {
	for {
		select {
		case client := <-r.Register:
			r.Clients = append(r.Clients, client)
			r.Log.Info(client.UUID + " joined room: " + r.Name)
		case client := <-r.Unregister:
			for i, c := range r.Clients {
				if c.UUID == client.UUID {
					r.Clients = append(r.Clients[:i], r.Clients[i+1:]...)
					r.Log.Info(client.UUID + " left room: " + r.Name)
					*channel <- client
					break
				}
			}
		case message := <-r.Broadcast:
			r.Log.Info(message.ID.Hex() + " was sent to room: " + r.Name)
			for _, client := range r.Clients {
				client.Conn.WriteJSON(message)
			}
		}
	}
}

func (h *Hub) SendMessage(m Message, n string) {
	go func() {
		h.Rooms[n].Broadcast <- m
	}()
}

func (h *Hub) DisconnectClientFromRoom(cl *Client, n string) {
	go func() {
		h.Rooms[n].Unregister <- cl
	}()
}

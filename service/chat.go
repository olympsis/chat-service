package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ChatService struct {
	cl  *mongo.Client
	col *mongo.Collection
	log *logrus.Logger
	rtr *mux.Router
	hub *Hub
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Client is a client that is connected to the server via a websocket.
type Client struct {
	UUID  string
	Conn  *websocket.Conn
	Group string
}

type Hub struct {
	// The clients that are connected to the server.
	Clients map[*Client]bool

	// The groups that exist on the server.
	Groups map[string]*Group

	// The channel on which new clients are added to the hub.
	Register chan *Client

	// The channel on which clients are removed from the hub.
	Unregister chan *Client

	// The channel on which messages are broadcast to all clients.
	Broadcast chan Message
}

type Group struct {
	Name       string
	Clients    []*Client
	Broadcast  chan Message
	Register   chan *Client
	Unregister chan *Client
	Log        *logrus.Logger
}

type Room struct {
	ID      primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	Owner   primitive.ObjectID `json:"owner,omitempty" bson:"owner,omitempty"`
	Name    string             `json:"name" bson:"name"`
	Type    string             `json:"type" bson:"type"`
	Members []Member           `json:"members" bson:"members"`
	History []Message          `json:"history" bson:"history"`
}

type RoomsResponse struct {
	TotalRooms int    `json:"totalRooms"`
	Rooms      []Room `json:"rooms"`
}

type Member struct {
	ID     primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	UUID   string             `json:"uuid" bson:"uuid"`
	Status string             `json:"status" bson:"status"`
}

type Message struct {
	ID        primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	From      string             `json:"from" bson:"from"`
	Type      string             `json:"type" bson:"type"`
	Body      string             `json:"body" bson:"body"`
	Timestamp int64              `json:"timestamp" bson:"timestamp"`
}

func NewChatService(l *logrus.Logger, r *mux.Router) *ChatService {
	return &ChatService{log: l, rtr: r}
}

/*
Connect to Database
  - Initiates connection to MongoDB
  - Grabs Enviroment Variables
*/
func (c *ChatService) ConnectToDatabase() (bool, error) {
	c.log.Info("Connecting to Database...")
	cl, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(os.Getenv("DB_URL")))

	// logs connection result and sets client
	if err != nil {
		c.log.Error("Failed to connect to Database!")
		c.log.Error(err.Error())
		return false, err
	} else {
		c.cl = cl // set controller client to client
		c.log.Info("Database connection successful")
		// set the collection
		c.col = c.cl.Database(os.Getenv("DB_NAME")).Collection(os.Getenv("DB_COL"))
		return true, nil
	}
}

func CreateHub() *Hub {
	return &Hub{
		Clients:    make(map[*Client]bool),
		Groups:     make(map[string]*Group),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan Message),
	}
}

func (c *ChatService) ConnectToHub(h *Hub) {
	c.hub = h
	c.log.Info("Hub connection successful")
}

func (c *ChatService) StartHub() {
	for {

	}
}

// NewGroup creates a new group.
func (c *ChatService) NewGroup(name string) *Group {
	return &Group{
		Name:       name,
		Clients:    make([]*Client, 0),
		Broadcast:  make(chan Message),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Log:        c.log,
	}
}

func (g *Group) Run() {
	for {
		select {
		case client := <-g.Register:
			g.Clients = append(g.Clients, client)
			g.Log.Info(client.UUID + " joined " + g.Name)
		case client := <-g.Unregister:
			for i, c := range g.Clients {
				if c.UUID == client.UUID {
					g.Clients = append(g.Clients[:i], g.Clients[i+1:]...)
					g.Log.Info(client.UUID + " left " + g.Name)
					break
				}
			}
		case message := <-g.Broadcast:
			g.Log.Info(message.ID.Hex() + " was sent")
			for _, client := range g.Clients {
				client.Conn.WriteJSON(message)
			}
		}
	}
}

func (c *ChatService) GetRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request" }`))
			return
		}

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)
		var room Room

		filter := bson.M{"_id": oid}
		err := c.col.FindOne(context.TODO(), filter).Decode(&room)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusNotFound)
				return
			}
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusFound)
		json.NewEncoder(rw).Encode(room)
	}
}

func (c *ChatService) GetRooms() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no club id found in request" }`))
			return
		}

		var rooms []Room

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)
		filter := bson.M{"owner": oid}
		cur, err := c.col.Find(context.Background(), filter)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				rw.WriteHeader(http.StatusNotFound)
				rw.Write([]byte(`{ "msg": "failed to search for room" }`))
				return
			}
		}

		if cur == nil {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusNoContent)
			return
		}

		for cur.Next(context.TODO()) {
			var room Room
			err := cur.Decode(&room)
			if err != nil {
				c.log.Error(err)
			}
			rooms = append(rooms, room)
		}

		if len(rooms) == 0 {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusNoContent)
			return
		}

		resp := RoomsResponse{
			TotalRooms: len(rooms),
			Rooms:      rooms,
		}

		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		json.NewEncoder(rw).Encode(resp)
	}
}

func (c *ChatService) CreateRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req Room

		_, x := context.WithTimeout(context.Background(), 30*time.Second)
		defer x()

		// decode request
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
			return
		}

		member := Member{
			ID:     primitive.NewObjectID(),
			UUID:   req.Members[0].UUID,
			Status: req.Members[0].Status,
		}

		room := Room{
			ID:      primitive.NewObjectID(),
			Owner:   req.Owner,
			Name:    req.Name,
			Type:    req.Type,
			Members: []Member{member},
			History: []Message{},
		}

		// create auth user in database
		_, err = c.col.InsertOne(context.TODO(), room)
		if err != nil {
			c.log.Error(err)
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
			return
		}

		rw.WriteHeader(http.StatusCreated)
		json.NewEncoder(rw).Encode(room)
	}
}

func (c *ChatService) UpdateRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req Room

		_, x := context.WithTimeout(context.Background(), 30*time.Second)
		defer x()

		// decode request
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
			return
		}

		// grab club id from path
		vars := mux.Vars(r)
		if len(vars["id"]) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request." }`))
			return
		}

		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "bad room id found in request." }`))
			return
		}

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)
		filter := bson.D{primitive.E{Key: "_id", Value: oid}}
		changes := bson.M{"$set": bson.M{
			"name": req.Name,
		}}

		_, err = c.col.UpdateOne(context.TODO(), filter, changes)
		if err != nil {
			c.log.Debug(err.Error())
		}

		var room Room
		err = c.col.FindOne(context.TODO(), filter).Decode(&room)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusNotFound)
				return
			}
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusFound)
		json.NewEncoder(rw).Encode(room)
	}
}

func (c *ChatService) DeleteRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		// grab club id from path
		vars := mux.Vars(r)
		if len(vars["id"]) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request." }`))
			return
		}

		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "bad room id found in request." }`))
			return
		}

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)

		filter := bson.D{primitive.E{Key: "_id", Value: oid}}
		_, err := c.col.DeleteOne(context.TODO(), filter)
		if err != nil {
			c.log.Debug(err.Error())
		}

		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(`OK`))
	}
}

func (c *ChatService) JoinRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		bearerToken := r.Header.Get("Authorization")
		tokenSplit := strings.Split(bearerToken, "Bearer ")
		token := tokenSplit[1]
		uuid, _, _, err := c.ValidateAndParseJWTToken(token)
		if err != nil {
			c.log.Error(err)
			return
		}

		vars := mux.Vars(r)
		if len(vars["id"]) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request." }`))
			return
		}

		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "bad room id found in request." }`))
			return
		}

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)

		member := Member{
			ID:     primitive.NewObjectID(),
			UUID:   uuid,
			Status: "live",
		}

		filter := bson.D{primitive.E{Key: "_id", Value: oid}}
		update := bson.M{"$push": bson.M{
			"members": member,
		}}
		_, err = c.col.UpdateOne(context.TODO(), filter, update)
		if err != nil {
			c.log.Debug(err.Error())
		}

		var room Room
		err = c.col.FindOne(context.TODO(), filter).Decode(&room)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusNotFound)
				return
			}
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		json.NewEncoder(rw).Encode(room)
	}
}

func (c *ChatService) LeaveRoom() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		bearerToken := r.Header.Get("Authorization")
		tokenSplit := strings.Split(bearerToken, "Bearer ")
		token := tokenSplit[1]
		uuid, _, _, err := c.ValidateAndParseJWTToken(token)
		if err != nil {
			c.log.Error(err)
			return
		}

		vars := mux.Vars(r)
		if len(vars["id"]) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request." }`))
			return
		}

		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "bad room id found in request." }`))
			return
		}

		id := vars["id"]
		oid, _ := primitive.ObjectIDFromHex(id)

		filter := bson.D{primitive.E{Key: "_id", Value: oid}}
		update := bson.M{"$pull": bson.M{
			"members": bson.M{"uuid": uuid},
		}}

		_, err = c.col.UpdateOne(context.TODO(), filter, update)
		if err != nil {
			c.log.Debug(err.Error())
		}

		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(`OK`))
	}
}

func (c *ChatService) Listen() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		// grab uuid from token
		bearerToken := r.Header.Get("Authorization")
		tokenSplit := strings.Split(bearerToken, "Bearer ")
		token := tokenSplit[1]
		uuid, _, _, err := c.ValidateAndParseJWTToken(token)
		if err != nil {
			c.log.Error(err)
			return
		}

		// grab club id from path
		vars := mux.Vars(r)
		if len(vars["id"]) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no room id found in request." }`))
			return
		}
		if len(vars["id"]) < 24 {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "bad room id found in request." }`))
			return
		}
		id := vars["id"]

		// upgrade connection to socket
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			c.log.Info(err)
			return
		}

		// create client
		cl := &Client{
			UUID:  uuid,
			Conn:  conn,
			Group: id,
		}

		// add client to group
		// if group doesn't exist then create one and add it to the hub
		group, ok := c.hub.Groups[cl.Group]
		if !ok {
			// Create a new group if it doesn't exist.
			group = c.NewGroup(cl.Group)
			c.hub.Groups[cl.Group] = group
			go group.Run()
		}

		group.Register <- cl

		var msg Message
		for {
			// Read a message from the client.
			err := conn.ReadJSON(&msg)
			if err != nil {
				c.log.Info(err)
				break
			}

			// add metadata to message
			message := Message{
				ID:        primitive.NewObjectID(),
				From:      msg.From,
				Type:      msg.Type,
				Body:      msg.Body,
				Timestamp: time.Now().Unix(),
			}

			// store message
			ok, err := c.StoreMessage(&message, group.Name)
			if !ok {
				c.log.Info(err)
			}

			// Send the message to the group.
			group.Broadcast <- message
		}

		// unregister if client disconnects
		group.Unregister <- cl
	}
}

func (c *ChatService) StoreMessage(message *Message, clubId string) (bool, error) {

	oid, _ := primitive.ObjectIDFromHex(clubId)
	filter := bson.M{"_id": oid}
	update := bson.M{"$push": bson.M{"history": message}}

	_, err := c.col.UpdateOne(context.TODO(), filter, update)
	if err != nil {
		c.log.Error(err)
		return false, err
	}

	return true, nil
}

/*
Middleware
  - Makes sure user is authenticated before taking requests
  - If there is no token or a bad token it returns the request with a unauthorized or forbidden error

Returns:

	Http handler
	- Passes the request to the next handler
*/
func (c *ChatService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		bearerToken := r.Header.Get("Authorization")
		tokenSplit := strings.Split(bearerToken, "Bearer ")

		if bearerToken == "" {
			c.log.WithFields(logrus.Fields{
				"Middleware": "ValidateAndParseJWTToken",
			}).Error("Failed to validate token")
			http.Error(rw, "Unauthorized", http.StatusUnauthorized)
			return
		}

		token := tokenSplit[1]
		if token == "" {
			c.log.WithFields(logrus.Fields{
				"Middleware": "ValidateAndParseJWTToken",
			}).Error("Failed to validate token")
			http.Error(rw, "Unauthorized", http.StatusUnauthorized)
			return
		}

		_, _, _, err := c.ValidateAndParseJWTToken(token)

		if err != nil {
			c.log.WithFields(logrus.Fields{
				"Middleware": "ValidateAndParseJWTToken",
			}).Error("Failed to validate token")
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

/*
Validate an Parse JWT Token
  - parse jwt token
  - return values

Returns:

	uuid - string of the user id token
	createdAt - string of the session token created date
	role - role of user
	error -  if there is an error return error else nil
*/
func (c *ChatService) ValidateAndParseJWTToken(tokenString string) (string, string, float64, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("KEY")), nil
	})

	if err != nil {
		return "", "", 0, err
	} else {
		uuid := claims["uuid"].(string)
		provider := claims["provider"].(string)
		createdAt := claims["createdAt"].(float64)
		return uuid, provider, createdAt, nil
	}
}

// Later we want to ping the db and if the db goes down or something is wrong with this service we want to restart it.
func (c *ChatService) Healthz() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte("ok"))
	}
}

func (c *ChatService) WhoAmi() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(`
		{
			"version": "0.1",
			"service": "chat"
		}
		`))
	}
}

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	hub "olympsis-services/chat/controller"
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
	hub *hub.Hub
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Room struct {
	ID      primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	Owner   primitive.ObjectID `json:"owner,omitempty" bson:"owner,omitempty"`
	Name    string             `json:"name" bson:"name"`
	Type    string             `json:"type" bson:"type"`
	Members []Member           `json:"members" bson:"members"`
	History []hub.Message      `json:"history" bson:"history"`
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

func (c *ChatService) ConnectToHub(h *hub.Hub) {
	c.hub = h
	c.log.Info("Hub connection successful")
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
		rw.WriteHeader(http.StatusOK)
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
			History: []hub.Message{},
		}

		// create auth user in database
		_, err = c.col.InsertOne(context.TODO(), room)
		if err != nil {
			c.log.Error(err)
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
			return
		}

		usr := c.FetchUser(*r, req.Members[0].UUID)
		ok, err := c.SubscribeToRoomNotifications(*r, room.ID.Hex(), []string{usr.DeviceToken})
		if !ok || err != nil {
			c.log.Error("Failed to subscribe user: " + req.Members[0].UUID + "to room topic. Room: " + room.ID.Hex())
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

		// we need to unregister users from topic before deleting room
		var room Room

		err := c.col.FindOne(context.TODO(), filter).Decode(&room)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusNotFound)
				return
			}
		}

		// fetch their device tokens
		var tokens []string
		for i := 0; i < len(room.Members); i++ {
			usr := c.FetchUser(*r, room.Members[i].UUID)
			tokens = append(tokens, usr.DeviceToken)
		}

		// unsubscribe users
		ok, err := c.UnsubscribeFromRoomNotifications(*r, room.ID.Hex(), tokens)
		if !ok || err != nil {
			c.log.Error("Failed to unsubscribe users from room topic. Room: " + id)
		}

		// delete room
		_, err = c.col.DeleteOne(context.TODO(), filter)
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

		usr := c.FetchUser(*r, uuid)
		ok, err := c.SubscribeToRoomNotifications(*r, room.ID.Hex(), []string{usr.DeviceToken})
		if !ok || err != nil {
			c.log.Error("Failed to subscribe user: " + uuid + "to room topic. Room: " + room.ID.Hex())
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

		usr := c.FetchUser(*r, uuid)
		ok, err := c.UnsubscribeFromRoomNotifications(*r, id, []string{usr.DeviceToken})
		if !ok || err != nil {
			c.log.Error("Failed to unsubscribe user: " + uuid + "from room topic. Room: " + id)
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
		cl := &hub.Client{
			UUID: uuid,
			Conn: conn,
			Room: id,
		}

		c.hub.Register <- cl

		var msg hub.Message
		for {
			// Read a message from the client.
			err := conn.ReadJSON(&msg)
			if err != nil {
				c.log.Info(err)
				break
			}

			// add metadata to message
			message := hub.Message{
				ID:        primitive.NewObjectID(),
				From:      msg.From,
				Type:      msg.Type,
				Body:      msg.Body,
				Timestamp: time.Now().Unix(),
			}

			// store message
			ok, err := c.StoreMessage(&message, cl.Room)
			if !ok {
				c.log.Info(err)
			}

			usr := c.FetchUser(*r, msg.From)
			fN := usr.FirstName + " " + usr.LastName

			// fetch room name
			oid, _ := primitive.ObjectIDFromHex(id)
			var room Room

			filter := bson.M{"_id": oid}
			err = c.col.FindOne(context.TODO(), filter).Decode(&room)
			if err != nil {
				c.log.Error("Failed to fetch room name")
			}
			rN := room.Name
			b := "Sent To " + rN

			// Send the message to the group.
			c.hub.SendMessage(message, cl.Room)

			// send notification
			c.SendNotification(*r, fN, b, id)
		}

		// unregister if client disconnects
		c.hub.DisconnectClientFromRoom(cl, cl.Room)
	}
}

func (c *ChatService) StoreMessage(message *hub.Message, clubId string) (bool, error) {

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

// NOTIFICATIONS

type NotificationRequest struct {
	Tokens []string `json:"tokens,omitempty"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Topic  string   `json:"topic"`
}

func (c *ChatService) SendNotification(r http.Request, t string, b string, tpc string) (bool, error) {
	bearerToken := r.Header.Get("Authorization")
	tokenSplit := strings.Split(bearerToken, "Bearer ")
	token := tokenSplit[1]
	client := &http.Client{}

	request := NotificationRequest{
		Title: t,
		Body:  b,
		Topic: tpc,
	}

	data, err := json.Marshal(request)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req, err := http.NewRequest("POST", "http://pushnote.olympsis.internal/v1/pushnote/topic", bytes.NewBuffer(data))
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	defer resp.Body.Close()
	return true, nil
}

func (c *ChatService) SubscribeToRoomNotifications(r http.Request, tpc string, tks []string) (bool, error) {
	bearerToken := r.Header.Get("Authorization")
	tokenSplit := strings.Split(bearerToken, "Bearer ")
	token := tokenSplit[1]
	client := &http.Client{}

	request := NotificationRequest{
		Topic:  tpc,
		Tokens: tks,
	}

	data, err := json.Marshal(request)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req, err := http.NewRequest("PUT", "http://pushnote.olympsis.internal/v1/pushnote/topic", bytes.NewBuffer(data))
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	defer resp.Body.Close()
	return true, nil
}

func (c *ChatService) UnsubscribeFromRoomNotifications(r http.Request, tpc string, tks []string) (bool, error) {
	bearerToken := r.Header.Get("Authorization")
	tokenSplit := strings.Split(bearerToken, "Bearer ")
	token := tokenSplit[1]
	client := &http.Client{}

	request := NotificationRequest{
		Topic:  tpc,
		Tokens: tks,
	}

	data, err := json.Marshal(request)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req, err := http.NewRequest("DELETE", "http://pushnote.olympsis.internal/v1/pushnote/topic", bytes.NewBuffer(data))
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		c.log.Error(err.Error())
		return false, err
	}

	defer resp.Body.Close()
	return true, nil
}

// USER LOOKUP

/*
Lookup User
- contains identifiable user data that others can see
*/
type LookUpUser struct {
	FirstName   string `json:"firstName" bson:"firstName"`
	LastName    string `json:"lastName" bson:"lastName"`
	DeviceToken string `json:"deviceToken,omitempty" bson:"deviceToken,omitempty"`
}

func (c *ChatService) FetchUser(r http.Request, user string) LookUpUser {
	bearerToken := r.Header.Get("Authorization")
	tokenSplit := strings.Split(bearerToken, "Bearer ")
	token := tokenSplit[1]
	client := &http.Client{}

	req, err := http.NewRequest("GET", "http://lookup.olympsis.internal/v1/lookup/"+user, nil)
	if err != nil {
		c.log.Error(err.Error())
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		c.log.Error(err.Error())
	}

	defer resp.Body.Close()

	var lookup LookUpUser
	err = json.NewDecoder(resp.Body).Decode(&lookup)
	if err != nil {
		c.log.Error(err.Error())
	}
	return lookup
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
			"version": "0.1.6",
			"service": "chat"
		}
		`))
	}
}

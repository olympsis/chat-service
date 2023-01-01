package chat

import (
	"encoding/json"
	"net/http"
	"time"

	stream_chat "github.com/GetStream/stream-chat-go/v3"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ChatService struct {
	cl  *stream_chat.Client
	log *logrus.Logger
	rtr *mux.Router
}

type TokenRequest struct {
	ID string `json:"id"`
}

type TokenResponse struct {
	Token string `json:"token"`
}

func NewChatService(l *logrus.Logger, r *mux.Router) *ChatService {
	return &ChatService{log: l, rtr: r}
}

func (c *ChatService) InitiateClient() {
	client, err := stream_chat.NewClient("28assc742pjv", "ednrgne9vhphsq6xwsm5jsqcaj5y2hevquh4mgad2ec5ph8m6fbc4cha95yg5ftn")
	if err != nil {
		c.log.Fatal("Failed to Initiate Client")
	} else {
		c.cl = client
		c.log.Info("Chat Server Client Initiated!")
	}
}

func (c *ChatService) GenerateToken() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {

		var req TokenRequest

		// check if body is empty
		if r.Body == http.NoBody {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": "no body in request" }`))
		}

		// decode request
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
		}

		issuedAt := time.Now().UTC()
		expiration := issuedAt.Add(time.Hour)
		token, err := c.cl.CreateToken(req.ID, expiration, issuedAt)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			rw.Write([]byte(`{ "msg": " ` + err.Error() + `" }`))
		}

		resp := TokenResponse{
			Token: token,
		}

		// write back login response
		rw.WriteHeader(http.StatusOK)
		json.NewEncoder(rw).Encode(&resp)
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

package main

import (
	"context"
	"net/http"
	chat "olympsis-services/chat/service"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func main() {
	// logger
	l := logrus.New()
	// mux router
	r := mux.NewRouter()

	// authentication service
	service := chat.NewChatService(l, r)

	// connecting to database
	res, err := service.ConnectToDatabase()

	// quit on err
	if !res || err != nil {
		os.Exit(1)
	}

	hub := chat.CreateHub()
	service.ConnectToHub(hub)

	r.Handle("/", service.WhoAmi()).Methods("GET")
	r.Handle("/healthz", service.Healthz()).Methods("GET")

	// service subrouter
	sr := r.PathPrefix("/v1").Subrouter()

	sr.Use(service.Middleware)
	sr.Handle("/chats", service.CreateRoom()).Methods("POST")
	sr.Handle("/chats/{id}", service.GetRoom()).Methods("GET")
	sr.Handle("/chats/club/{id}", service.GetRooms()).Methods("GET")
	sr.Handle("/chats/{id}", service.UpdateRoom()).Methods("PUT")
	sr.Handle("/chats/{id}", service.DeleteRoom()).Methods("DELETE")
	sr.Handle("/chats/{id}/join", service.JoinRoom()).Methods("POST")
	sr.Handle("/chats/{id}/leave", service.LeaveRoom()).Methods("DELETE")
	sr.Handle("/chats/{id}/ws", service.Listen())

	port := os.Getenv("PORT")

	// server config
	s := &http.Server{
		Addr:    `:` + port, // pull from env
		Handler: r,
	}

	// start server
	go func() {
		l.Info(`Starting Chat Service at...` + port)
		err := s.ListenAndServe()

		if err != nil {
			l.Info("Error Starting Server: ", err)
			os.Exit(1)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigs

	l.Printf("Recieved Termination(%s), graceful shutdown \n", sig)

	tc, c := context.WithTimeout(context.Background(), 30*time.Second)

	defer c()

	s.Shutdown(tc)
}

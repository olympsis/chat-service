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
	service.InitiateClient()

	r.Handle("/", service.WhoAmi()).Methods("GET")
	r.Handle("/healthz", service.Healthz()).Methods("GET")

	r.Handle("/v1/chat/token", service.GenerateToken()).Methods("POST")

	port := os.Getenv("PORT")

	// server config
	s := &http.Server{
		Addr:         `:` + port, // pull from env
		Handler:      r,
		IdleTimeout:  30 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
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

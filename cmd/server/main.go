package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"GridKing-Backend/internal/api"
	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/profile"
	"GridKing-Backend/internal/realtime"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	config := &firebase.Config{ProjectID: os.Getenv("FIREBASE_PROJECT_ID")}
	var clientOptions []option.ClientOption
	if credentials := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON"); credentials != "" {
		clientOptions = append(clientOptions, option.WithCredentialsJSON([]byte(credentials)))
	}
	app, err := firebase.NewApp(ctx, config, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}
	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Fatal(err)
	}
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer firestoreClient.Close()

	profiles := profile.NewStore(firestoreClient)
	verifier := auth.New(authClient)
	hub := realtime.NewHub(profiles)
	handler := api.NewServer(profiles, verifier, hub, os.Getenv("FRONTEND_ORIGIN")).Router()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		log.Printf("GridKing backend listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

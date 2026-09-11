package main

import (
	"context"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"log"
	"net/http"
	"os"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := store.Open(context.Background(), url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	addr := os.Getenv("CONDUCTOR_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("conductord listening on %s (local development authentication)", addr)
	log.Fatal(http.ListenAndServe(addr, api.New(service.New(db))))
}

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/ldm0206/claude-docker/backend/internal/config"
	"github.com/ldm0206/claude-docker/backend/internal/server"
)

func main() {
	cfg, err := config.Load(envLookup)
	if err != nil {
		log.Fatalf("[server] config: %v", err)
	}
	if cfg.SessionSecret == "" {
		log.Fatal("[server] SESSION_SECRET environment variable is required")
	}
	srv := server.New(cfg)
	log.Printf("[server] listening on :%d", cfg.Port)
	if err := httpListenAndServe(cfg.Port, srv.Routes()); err != nil {
		log.Fatalf("[server] %v", err)
	}
}

func envLookup(k string) (string, bool) { return osLookupEnv(k) }

func httpListenAndServe(port int, h http.Handler) error {
	return http.ListenAndServe(fmt.Sprintf(":%d", port), h)
}

func osLookupEnv(k string) (string, bool) { return os.LookupEnv(k) }

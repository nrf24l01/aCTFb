package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/nrf24l01/actfb/internal/config"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	addr := net.JoinHostPort(cfg.WebApp.AppHost, strconv.Itoa(cfg.WebApp.Port))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

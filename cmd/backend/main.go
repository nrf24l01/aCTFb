package main

import (
	"log"

	"github.com/nrf24l01/actfb/internal/config"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
}

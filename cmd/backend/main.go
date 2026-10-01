package main

import "github.com/nrf24l01/actfb/internal/config"

func main() {
	config, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

}

package config

import (
	"os"

	"github.com/ilyakaznacheev/cleanenv"
)

func LoadConfig() (*Config, error) {
	var cfg Config

	config_file := os.Getenv("CONFIG_FILE")
	if config_file == "" {
		config_file = "config.yml"
	}

	err := cleanenv.ReadConfig(config_file, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

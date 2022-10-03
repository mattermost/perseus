package server

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
)

// Config is the configuration for a perseus server.
type Config struct {
	ListenAddress string
	DBSettings    DBSettings
}

type DBSettings struct {
	WriterDSN  string
	ReaderDSNs []string
}

// ParseConfig reads the config file and returns a new *Config,
// This method overrides values in the file if there is any environment
// variables corresponding to a specific setting.
func ParseConfig(path string) (Config, error) {
	var cfg Config
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	err = json.NewDecoder(file).Decode(&cfg)
	if err != nil {
		return cfg, fmt.Errorf("could not decode file: %w", err)
	}

	if err = envconfig.Process("perseus", &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
)

// Config is the configuration for a perseus server.
type Config struct {
	ListenAddress    string
	AWSSettings      AWSSettings
	AuthDBSettings   AuthDBSettings
	PoolSettings     PoolSettings
	OverrideSettings map[string]PoolSettings
}

type AWSSettings struct {
	AccessKeyId     string
	SecretAccessKey string
	Region          string
	Endpoint        string
	KMSKeyARN       string
}

type PoolSettings struct {
	MaxIdle               int
	MaxOpen               int
	MaxLifetimeSecs       int
	MaxIdletimeSecs       int
	ConnCreateTimeoutSecs int
	ConnCloseTimeoutSecs  int
	SchemaExecTimeoutSecs int
}

type AuthDBSettings struct {
	AuthDBDSN            string
	AuthQueryTimeoutSecs int
}

// Parse reads the config file and returns a new *Config,
// This method overrides values in the file if there is any environment
// variables corresponding to a specific setting.
func Parse(path string) (Config, error) {
	var cfg Config
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	err = dec.Decode(&cfg)
	if err != nil {
		return cfg, fmt.Errorf("could not decode file: %w", err)
	}

	if err = envconfig.Process("perseus", &cfg); err != nil {
		return cfg, fmt.Errorf("error processing env vars: %w", err)
	}

	return cfg, nil
}

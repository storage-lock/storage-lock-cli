package config

import (
	"encoding/json"
	"os"

	"github.com/mitchellh/go-homedir"
	"path/filepath"
)

type Config struct {
	StorageName string `json:"storage_name"`
	DSN         string `json:"dsn"`
}

func ReadConfig() (*Config, error) {
	path, err := FilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func SaveConfig(config *Config) error {
	path, err := FilePath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func Workspace() (string, error) {
	dir, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".storage-lock"), nil
}

func FilePath() (string, error) {
	workspace, err := Workspace()
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, "config"), nil
}

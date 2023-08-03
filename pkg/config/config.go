package config

import (
	"github.com/mitchellh/go-homedir"
	"path/filepath"
)

type Config struct {
	StorageName string `json:"storage_name"`
	DSN         string `json:"dsn"`
}

func ReadConfig() (*Config, error) {
	
}

func SaveConfig(config *Config) error {

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

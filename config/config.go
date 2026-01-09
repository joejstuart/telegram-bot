// Package config provides configuration management for the bot.
package config

import (
	"os"
)

// Config holds all application configuration.
type Config struct {
	TelegramToken     string
	OllamaURL         string
	OllamaModel       string
	GoogleClientID    string
	GoogleSecret      string
	GoogleRedirectURL string
	GoogleTokenFile   string
	PythonWorkspace   string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		TelegramToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
		OllamaURL:         getEnvOrDefault("OLLAMA_URL", "http://localhost:11434/api/chat"),
		OllamaModel:       getEnvOrDefault("OLLAMA_MODEL", "qwen3-coder:30b"),
		GoogleClientID:    os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleSecret:      os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL: getEnvOrDefault("GOOGLE_REDIRECT_URL", "urn:ietf:wg:oauth:2.0:oob"),
		GoogleTokenFile:   getEnvOrDefault("GOOGLE_TOKEN_FILE", "google_token.json"),
		PythonWorkspace:   getEnvOrDefault("PYTHON_WORKSPACE", "workspace"),
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

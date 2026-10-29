package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultAPI is the API base URL when neither --api nor PC_API is set.
const defaultAPI = "http://localhost:8080"

// defaultToken is the stub token stored by "pc login" without arguments.
const defaultToken = "dev-token"

// configDir returns the pc config directory: PC_CONFIG_DIR when set
// (used by tests), otherwise ~/.pc.
func configDir(getenv func(string) string) string {
	if d := getenv("PC_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pc"
	}
	return filepath.Join(home, ".pc")
}

// tokenPath returns the file holding the saved auth token.
func tokenPath(getenv func(string) string) string {
	return filepath.Join(configDir(getenv), "token")
}

// saveToken stores the token with owner-only permissions.
func saveToken(getenv func(string) string, token string) error {
	dir := configDir(getenv)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(tokenPath(getenv), []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

// loadToken returns the saved token, or "" when absent or unreadable.
func loadToken(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// apiBase resolves the API base URL: the --api flag wins, then PC_API,
// then the localhost default. A trailing slash is stripped.
func apiBase(getenv func(string) string, flag string) string {
	base := flag
	if base == "" {
		base = getenv("PC_API")
	}
	if base == "" {
		base = defaultAPI
	}
	return strings.TrimRight(base, "/")
}

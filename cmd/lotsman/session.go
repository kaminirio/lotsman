package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cachedSession is the persisted login state at ~/.config/lotsman/session.json.
type cachedSession struct {
	API      string `json:"api"`
	Username string `json:"username"`
	Cookie   string `json:"cookie"` // lotsman_session value
}

// sessionPath returns the session cache file path (~/.config/lotsman/session.json).
func sessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lotsman", "session.json"), nil
}

// saveSession writes the session cache with 0600 permissions (it holds a bearer
// credential), creating the directory as needed.
func saveSession(s cachedSession) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o600)
}

// loadCachedCookie returns the cached lotsman_session value, or "" if none is
// stored (or the file is unreadable).
func loadCachedCookie() string {
	path, err := sessionPath()
	if err != nil {
		return ""
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s cachedSession
	if err := json.Unmarshal(buf, &s); err != nil {
		return ""
	}
	return s.Cookie
}

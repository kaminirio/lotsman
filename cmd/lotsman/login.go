package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

// runLogin implements `lotsman login`: prompt for a password (no echo), POST it
// to /auth/login, and cache the returned lotsman_session cookie for later
// commands. Route contract: POST /auth/login {username,password} -> 200 + cookie.
func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	apiDefault := os.Getenv("LOTSMAN_API")
	if apiDefault == "" {
		apiDefault = "http://localhost:8080"
	}
	api := fs.String("api", apiDefault, "control-plane API base URL (env LOTSMAN_API)")
	username := fs.String("username", "", "account username (prompted if empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	user := strings.TrimSpace(*username)
	if user == "" {
		fmt.Fprint(os.Stderr, "Username: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read username: %w", err)
		}
		user = strings.TrimSpace(line)
	}
	if user == "" {
		return fmt.Errorf("login: username is required")
	}

	password, err := readPassword("Password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}

	body, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, *api+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "lotsman-cli") // CSRF header the API requires
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: %s", resp.Status)
	}

	cookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == "lotsman_session" {
			cookie = c.Value
		}
	}
	if cookie == "" {
		return fmt.Errorf("login: server returned no session cookie")
	}
	if err := saveSession(cachedSession{API: *api, Username: user, Cookie: cookie}); err != nil {
		return fmt.Errorf("persist session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "logged in as %s; session cached\n", user)
	return nil
}

// readPassword reads a password from the terminal without echo, falling back to a
// plain (echoed) stdin read when stdin is not a terminal (e.g. piped input).
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

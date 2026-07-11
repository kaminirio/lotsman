package auth

import (
	"context"
	"log/slog"

	"lotsman/internal/store"
)

// compatSecret is the deterministic session secret used when a manager is built
// from an empty legacy config (tests that never authenticate). It is never used
// in production, which always goes through NewManagerFromEnv.
const compatSecret = "lotsman-compat-dev-session-secret-0123456789"

// NewManager builds an auth manager from a legacy LOTSMAN_SSO_CONFIG JSON string,
// seeding an internal in-memory user store from the config (init_admin -> admin;
// binding subjects and allowed_usernames -> active non-admin accounts). It exists
// for backward-compatible tests; production wiring uses NewManagerFromEnv.
func NewManager(ssoConfigJSON string) *Manager {
	m, _ := NewManagerErr(ssoConfigJSON, slog.Default())
	return m
}

// NewManagerErr is NewManager returning any config parse error.
func NewManagerErr(ssoConfigJSON string, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	users := store.NewMemory()

	if ssoConfigJSON == "" {
		// Empty legacy config ⇒ SSO disabled: local-dev Anonymous mode with all
		// endpoints open. Build a store-backed manager but flip it back to disabled.
		m, err := NewManagerFromEnv(Config{SessionSecret: compatSecret}, users, logger)
		if err != nil {
			return nil, err
		}
		m.enabled = false
		return m, nil
	}

	cfg, err := ParseSSOConfig(ssoConfigJSON)
	if err != nil {
		// A present-but-invalid config fails open to disabled (Anonymous) mode, but
		// still surfaces the parse error so the caller can log/fail as it chooses —
		// matching the historical NewManagerErr contract.
		m, merr := NewManagerFromEnv(Config{SessionSecret: compatSecret}, users, logger)
		if merr != nil {
			return nil, merr
		}
		m.enabled = false
		return m, err
	}

	seed := func(username string, admin bool) {
		if username == "" {
			return
		}
		_ = users.CreateUser(context.Background(), store.User{
			ID:       username,
			Username: username,
			Email:    username + "@example.com",
			IsAdmin:  admin,
			Active:   true,
		})
	}
	seed(cfg.InitAdmin, true)
	for _, b := range cfg.Bindings {
		seed(b.Subject, false)
	}
	for _, u := range cfg.GitHub.AllowedUsernames {
		seed(u, false)
	}

	m, err := NewManagerFromEnv(Config{
		SessionSecret: cfg.SessionSecret,
		BaseURL:       cfg.BaseURL,
		UIURL:         cfg.UIURL,
		GitHub:        ProviderCreds{ClientID: cfg.GitHub.ClientID, ClientSecret: cfg.GitHub.ClientSecret},
		Bindings:      cfg.Bindings,
		GroupBindings: cfg.GroupBindings,
	}, users, logger)
	if err != nil {
		return nil, err
	}
	// Retain the parsed SSOConfig so the legacy RBAC Enforcer (init_admin +
	// bindings) and the Config() accessor keep working for existing callers/tests.
	m.cfg = cfg
	return m, nil
}

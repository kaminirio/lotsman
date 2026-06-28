package auth

// rbac_resolution_test.go covers the test-plan items that are not yet exercised
// by auth_test.go or session_test.go:
//
//   - Session Groups round-trip through MintSession → VerifySession.
//   - A token minted without groups (nil) parses to an empty (not nil) Groups
//     slice (backward-compat / deny-by-default for group bindings).
//   - Per-cluster isolation at the Manager.Enforcer level (not just rbac.New).
//   - Namespace-scoped binding through Manager.Enforcer.
//   - Operator role via Manager.Enforcer: CanView true AND CanInvestigate true.
//   - Union of user binding + group binding when both match.
//   - SSO-disabled pass-through is global admin (explicit contract assertion).
//   - RBACConfig() when enabled returns bindings; when disabled returns nil slices.

import (
	"encoding/json"
	"testing"
	"time"

	"lotsman/internal/rbac"
)

// ── Session: Groups round-trip ──────────────────────────────────────────────

func TestSessionGroupsRoundTrip(t *testing.T) {
	groups := []string{"acme", "acme/platform-team"}
	tok, err := MintSession([]byte(testSecret), "alice", "a@acme.io", "Alice", "github", groups, time.Hour)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	claims, err := VerifySession([]byte(testSecret), tok)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	if len(claims.Groups) != len(groups) {
		t.Fatalf("Groups length: want %d, got %d", len(groups), len(claims.Groups))
	}
	for i, g := range groups {
		if claims.Groups[i] != g {
			t.Errorf("Groups[%d]: want %q, got %q", i, g, claims.Groups[i])
		}
	}
}

// TestSessionNilGroupsDenyByDefault ensures that a token minted with groups=nil
// comes back with a zero-length slice so group-based RBAC denies by default.
// The JWT omitempty field produces an absent "groups" claim, which the jwt
// library decodes as nil — both nil and empty mean "no group bindings apply".
func TestSessionNilGroupsDenyByDefault(t *testing.T) {
	tok, err := MintSession([]byte(testSecret), "ghost", "", "", "github", nil, time.Hour)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	claims, err := VerifySession([]byte(testSecret), tok)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	if len(claims.Groups) != 0 {
		t.Errorf("nil groups must decode to zero-length (deny group bindings); got %v", claims.Groups)
	}
}

// TestSessionGroupsEmptySliceRoundTrip verifies that an explicit empty
// []string{} round-trips without becoming populated.
func TestSessionGroupsEmptySliceRoundTrip(t *testing.T) {
	tok, err := MintSession([]byte(testSecret), "ghost", "", "", "github", []string{}, time.Hour)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	claims, err := VerifySession([]byte(testSecret), tok)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	if len(claims.Groups) != 0 {
		t.Errorf("empty groups must decode to zero-length; got %v", claims.Groups)
	}
}

// ── Enforcer: per-cluster isolation through Manager ─────────────────────────

// TestEnforcerClusterIsolation builds a Manager with a cluster-scoped viewer
// binding and asserts that a different cluster is strictly denied at the
// Manager.Enforcer level (not just rbac.New directly).
func TestEnforcerClusterIsolation(t *testing.T) {
	// alice is a viewer on "prod" only (no namespace restriction).
	cfg := configWithNamespacedBinding(t, "alice", rbac.RoleViewer, "prod", "")
	m := NewManager(cfg)
	e := m.Enforcer(User{Login: "alice"})

	cases := []struct {
		cluster   string
		namespace string
		wantView  bool
		label     string
	}{
		{"prod", "any-ns", true, "prod viewer should view any namespace in prod"},
		{"prod", "another-ns", true, "prod viewer should view another namespace in prod"},
		{"staging", "any-ns", false, "prod binding must not grant access to staging"},
		{"staging", "", false, "prod binding must not grant empty-ns access to staging"},
	}
	for _, tc := range cases {
		if got := e.CanView(tc.cluster, tc.namespace); got != tc.wantView {
			t.Errorf("%s: CanView(%q,%q) = %v, want %v",
				tc.label, tc.cluster, tc.namespace, got, tc.wantView)
		}
	}
}

// TestEnforcerNamespaceScopedBinding verifies that a namespace-scoped binding
// grants view only within that namespace; a different namespace in the same
// cluster is denied.
func TestEnforcerNamespaceScopedBinding(t *testing.T) {
	// alice is a viewer on prod/team-a only.
	cfg := configWithNamespacedBinding(t, "alice", rbac.RoleViewer, "prod", "team-a")
	m := NewManager(cfg)
	e := m.Enforcer(User{Login: "alice"})

	if !e.CanView("prod", "team-a") {
		t.Error("namespace binding must grant view in prod/team-a")
	}
	if e.CanView("prod", "team-b") {
		t.Error("namespace binding for team-a must NOT grant view in prod/team-b")
	}
	if e.CanView("staging", "team-a") {
		t.Error("namespace binding for prod must NOT grant view in staging/team-a")
	}
	if e.CanInvestigate("prod", "team-a") {
		t.Error("viewer binding must NOT grant investigate even within scope")
	}
}

// TestEnforcerOperatorRole verifies that an operator binding grants BOTH
// CanView and CanInvestigate within scope, and that it does NOT grant IsAdmin.
func TestEnforcerOperatorRole(t *testing.T) {
	cfg := configWithNamespacedBinding(t, "bob", rbac.RoleOperator, "prod", "payments")
	m := NewManager(cfg)
	e := m.Enforcer(User{Login: "bob"})

	if !e.CanView("prod", "payments") {
		t.Error("operator binding must grant CanView in scope")
	}
	if !e.CanInvestigate("prod", "payments") {
		t.Error("operator binding must grant CanInvestigate in scope")
	}
	if e.IsAdmin() {
		t.Error("operator binding must NOT make the user a global admin")
	}
	// Outside scope.
	if e.CanView("prod", "other-ns") {
		t.Error("operator binding in payments must NOT grant view in other-ns")
	}
	if e.CanInvestigate("staging", "payments") {
		t.Error("operator binding in prod must NOT grant investigate in staging")
	}
}

// TestEnforcerUserAndGroupUnion asserts that when a user has both a direct
// user binding AND a matching group binding, they both contribute: the
// resulting enforcer is the union of both sets.
func TestEnforcerUserAndGroupUnion(t *testing.T) {
	// alice has viewer on "prod" (direct) + is in "sre" which gets operator on "staging".
	cfg := configWithUserAndGroupBindings(t,
		"alice", rbac.RoleViewer, "prod",
		"sre", rbac.RoleOperator, "staging",
	)
	m := NewManager(cfg)
	// alice is in group "sre".
	e := m.Enforcer(User{Login: "alice", Groups: []string{"sre"}})

	if !e.CanView("prod", "any") {
		t.Error("direct user viewer binding must grant view on prod")
	}
	if e.CanInvestigate("prod", "any") {
		t.Error("direct user viewer binding must NOT grant investigate on prod")
	}
	if !e.CanInvestigate("staging", "any") {
		t.Error("group operator binding must grant investigate on staging (via group membership)")
	}
	if !e.CanView("staging", "any") {
		t.Error("group operator binding must grant view on staging")
	}
}

// TestEnforcerGroupMembershipCaseFolding ensures that group comparison is
// case-insensitive (GitHub org/team slugs may arrive in varying case).
func TestEnforcerGroupMembershipCaseFolding(t *testing.T) {
	cfg := configWithNamespacedGroupBinding(t, "ACME", rbac.RoleOperator, "prod", "")
	m := NewManager(cfg)

	// Session carries the group in lower-case.
	eLower := m.Enforcer(User{Login: "carol", Groups: []string{"acme"}})
	if !eLower.CanInvestigate("prod", "demo") {
		t.Error("group match must be case-insensitive (lower-case group in session)")
	}

	// Session carries the group in upper-case.
	eUpper := m.Enforcer(User{Login: "dave", Groups: []string{"ACME"}})
	if !eUpper.CanInvestigate("prod", "demo") {
		t.Error("group match must be case-insensitive (upper-case group in session)")
	}
}

// TestEnforcerSSODisabledIsGlobalAdmin asserts the critical local-dev contract:
// with SSO disabled the Anonymous user resolves to a global admin enforcer —
// CanView, CanInvestigate, and IsAdmin must all be true for any scope.
func TestEnforcerSSODisabledIsGlobalAdmin(t *testing.T) {
	m := NewManager("") // SSO disabled
	e := m.Enforcer(Anonymous())

	if !e.IsAdmin() {
		t.Error("SSO-disabled anonymous must be a global admin")
	}
	if !e.CanView("any-cluster", "any-ns") {
		t.Error("SSO-disabled anonymous must CanView any cluster/namespace")
	}
	if !e.CanInvestigate("any-cluster", "any-ns") {
		t.Error("SSO-disabled anonymous must CanInvestigate any cluster/namespace")
	}
}

// TestEnforcerGroupNotMemberDenied verifies that a user NOT in the group gets
// nothing from the group binding.
func TestEnforcerGroupNotMemberDenied(t *testing.T) {
	cfg := configWithNamespacedGroupBinding(t, "acme", rbac.RoleOperator, "prod", "")
	m := NewManager(cfg)

	// User is in "other-org", not "acme".
	e := m.Enforcer(User{Login: "bob", Groups: []string{"other-org"}})
	if e.CanView("prod", "any") {
		t.Error("a user not in the bound group must be denied (deny-by-default)")
	}
	if e.CanInvestigate("prod", "any") {
		t.Error("a user not in the bound group must not investigate")
	}
}

// TestEnforcerOrgTeamSlugGroupBinding verifies that an "org/team" slug matches
// a session group carrying that exact slug, and that the parent org slug alone
// is not sufficient.
func TestEnforcerOrgTeamSlugGroupBinding(t *testing.T) {
	cfg := configWithNamespacedGroupBinding(t, "acme/platform", rbac.RoleViewer, "prod", "")
	m := NewManager(cfg)

	eMatch := m.Enforcer(User{Login: "carol", Groups: []string{"acme/platform"}})
	if !eMatch.CanView("prod", "demo") {
		t.Error("org/team slug group binding must match the session group with the same slug")
	}

	// A user in the parent org "acme" but not the team "acme/platform" must not match.
	eParent := m.Enforcer(User{Login: "dave", Groups: []string{"acme"}})
	if eParent.CanView("prod", "demo") {
		t.Error("org/team group binding must not match the parent org slug alone")
	}
}

// TestRBACConfigAccessorDisabledReturnsNilSlices reinforces that the RBACConfig
// accessor returns nil (not empty) slices for bindings when SSO is disabled, so
// callers that distinguish nil from empty behave correctly.
func TestRBACConfigAccessorDisabledReturnsNilSlices(t *testing.T) {
	m := NewManager("")
	roles, bindings, groupBindings := m.RBACConfig()
	if len(roles) == 0 {
		t.Error("RBACConfig must always return the known roles list")
	}
	if bindings != nil {
		t.Errorf("disabled SSO: bindings should be nil, got %v", bindings)
	}
	if groupBindings != nil {
		t.Errorf("disabled SSO: group_bindings should be nil, got %v", groupBindings)
	}
}

// TestRBACConfigAccessorEnabledReturnsConfiguredBindings verifies that the
// accessor surfaces every configured binding when SSO is enabled.
func TestRBACConfigAccessorEnabledReturnsConfiguredBindings(t *testing.T) {
	cfg := configWithUserAndGroupBindings(t,
		"alice", rbac.RoleViewer, "prod",
		"acme", rbac.RoleOperator, "staging",
	)
	m := NewManager(cfg)
	roles, bindings, groupBindings := m.RBACConfig()

	if len(roles) != 3 {
		t.Errorf("want 3 known roles, got %d", len(roles))
	}
	if len(bindings) != 1 {
		t.Errorf("want 1 user binding, got %d", len(bindings))
	}
	if len(groupBindings) != 1 {
		t.Errorf("want 1 group binding, got %d", len(groupBindings))
	}
}

// TestIsGitHubUsernameAllowedBindingOnlyNoAllowlist checks that a login named
// only as a Binding Subject (absent from allowed_usernames) is implicitly
// allowed to log in.
func TestIsGitHubUsernameAllowedBindingOnlyNoAllowlist(t *testing.T) {
	// No allowed_usernames at all; "alice" is present only as a binding subject.
	cfgStr := configWithNamespacedBinding(t, "alice", rbac.RoleViewer, "prod", "")
	cfg, err := ParseSSOConfig(cfgStr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !cfg.IsGitHubUsernameAllowed("alice") {
		t.Error("a binding subject must be implicitly allowed to log in, even with no allowed_usernames")
	}
	if !cfg.IsGitHubUsernameAllowed("Alice") {
		t.Error("binding subject check must be case-insensitive")
	}
	if cfg.IsGitHubUsernameAllowed("mallory") {
		t.Error("a user absent from bindings and allowed_usernames must be denied login")
	}
}

// TestIsGitHubUsernameAllowedEmptyAllowlistNoBindings verifies that with no
// allowed_usernames and no bindings, only the init_admin passes.
func TestIsGitHubUsernameAllowedEmptyAllowlistNoBindings(t *testing.T) {
	cfg, err := ParseSSOConfig(validConfigJSON(t, "octocat"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !cfg.IsGitHubUsernameAllowed("octocat") {
		t.Error("init_admin must always be allowed")
	}
	if cfg.IsGitHubUsernameAllowed("stranger") {
		t.Error("non-init_admin with no bindings/allowlist must be denied")
	}
}

// TestParseSSOConfigGroupBindingEmptyCluster asserts that an empty cluster in
// a group_binding is rejected (must use "*").
func TestParseSSOConfigGroupBindingEmptyCluster(t *testing.T) {
	_, err := ParseSSOConfig(`{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","group_bindings":[{"group":"acme","role":"viewer","cluster":""}],"github":{"client_id":"a","client_secret":"b"}}`)
	if err == nil {
		t.Fatal("empty cluster in group_binding must be rejected")
	}
}

// TestParseSSOConfigGroupBindingMissingGroup asserts that a group_binding with
// no group value is rejected.
func TestParseSSOConfigGroupBindingMissingGroup(t *testing.T) {
	_, err := ParseSSOConfig(`{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","group_bindings":[{"group":"","role":"viewer","cluster":"*"}],"github":{"client_id":"a","client_secret":"b"}}`)
	if err == nil {
		t.Fatal("empty group in group_binding must be rejected")
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// configWithNamespacedBinding returns a valid SSO config JSON string where
// subject has a single binding to cluster/namespace with the given role.
// An empty namespace means "all namespaces in that cluster".
func configWithNamespacedBinding(t *testing.T, subject, role, cluster, namespace string) string {
	t.Helper()
	binding := map[string]any{
		"subject": subject,
		"role":    role,
		"cluster": cluster,
	}
	if namespace != "" {
		binding["namespace"] = namespace
	}
	m := map[string]any{
		"session_secret": testSecret,
		"base_url":       "http://localhost:8080",
		"ui_url":         "http://localhost:3000",
		"init_admin":     "octocat",
		"bindings":       []map[string]any{binding},
		"github": map[string]any{
			"client_id":     "cid",
			"client_secret": "csecret",
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// configWithNamespacedGroupBinding returns a valid SSO config with a single
// group binding.
func configWithNamespacedGroupBinding(t *testing.T, group, role, cluster, namespace string) string {
	t.Helper()
	binding := map[string]any{
		"group":   group,
		"role":    role,
		"cluster": cluster,
	}
	if namespace != "" {
		binding["namespace"] = namespace
	}
	m := map[string]any{
		"session_secret": testSecret,
		"base_url":       "http://localhost:8080",
		"ui_url":         "http://localhost:3000",
		"init_admin":     "octocat",
		"group_bindings": []map[string]any{binding},
		"github": map[string]any{
			"client_id":     "cid",
			"client_secret": "csecret",
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// configWithUserAndGroupBindings returns a valid SSO config with one user
// binding (subject/role/cluster) and one group binding (group/role/cluster).
func configWithUserAndGroupBindings(t *testing.T, subject, userRole, userCluster, group, groupRole, groupCluster string) string {
	t.Helper()
	m := map[string]any{
		"session_secret": testSecret,
		"base_url":       "http://localhost:8080",
		"ui_url":         "http://localhost:3000",
		"init_admin":     "octocat",
		"bindings": []map[string]any{
			{"subject": subject, "role": userRole, "cluster": userCluster},
		},
		"group_bindings": []map[string]any{
			{"group": group, "role": groupRole, "cluster": groupCluster},
		},
		"github": map[string]any{
			"client_id":     "cid",
			"client_secret": "csecret",
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

package rbac

import "testing"

// --- role hierarchy --------------------------------------------------------

func TestIsRoleAtLeast(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		minRole string
		want    bool
	}{
		{"admin>=admin", RoleAdmin, RoleAdmin, true},
		{"admin>=operator", RoleAdmin, RoleOperator, true},
		{"admin>=viewer", RoleAdmin, RoleViewer, true},

		{"operator>=viewer", RoleOperator, RoleViewer, true},
		{"operator>=operator", RoleOperator, RoleOperator, true},
		{"operator<admin", RoleOperator, RoleAdmin, false},

		{"viewer>=viewer", RoleViewer, RoleViewer, true},
		{"viewer<operator", RoleViewer, RoleOperator, false},
		{"viewer<admin", RoleViewer, RoleAdmin, false},

		{"unknown role", "god", RoleViewer, false},
		{"unknown minRole", RoleAdmin, "god", false},
		{"both unknown", "god", "demigod", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRoleAtLeast(tc.role, tc.minRole); got != tc.want {
				t.Errorf("IsRoleAtLeast(%q,%q)=%v want %v", tc.role, tc.minRole, got, tc.want)
			}
		})
	}
}

func TestRoleCanPerform(t *testing.T) {
	tests := []struct {
		role, action string
		want         bool
	}{
		{RoleAdmin, ActionView, true},
		{RoleAdmin, ActionInvestigate, true},
		{RoleOperator, ActionView, true},
		{RoleOperator, ActionInvestigate, true},
		{RoleViewer, ActionView, true},
		{RoleViewer, ActionInvestigate, false},
		{"unknown", ActionView, false},
		{RoleAdmin, "unknown-action", false},
	}
	for _, tc := range tests {
		t.Run(tc.role+"/"+tc.action, func(t *testing.T) {
			if got := roleCanPerform(tc.role, tc.action); got != tc.want {
				t.Errorf("roleCanPerform(%q,%q)=%v want %v", tc.role, tc.action, got, tc.want)
			}
		})
	}
}

func TestIsValidRole(t *testing.T) {
	for _, r := range []string{RoleAdmin, RoleOperator, RoleViewer} {
		if !IsValidRole(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	if IsValidRole("god") {
		t.Error("god should not be valid")
	}
}

// --- Enforcer cluster/namespace scoping -----------------------------------

func TestEnforcer_GlobalAdmin(t *testing.T) {
	e := New([]Binding{{Role: RoleAdmin, Cluster: Wildcard}})
	if !e.IsAdmin() {
		t.Fatal("expected global admin")
	}
	if !e.CanView("prod", "payments") {
		t.Error("admin should view any cluster/namespace")
	}
	if !e.CanInvestigate("staging", "demo") {
		t.Error("admin should investigate anywhere")
	}
}

func TestEnforcer_ClusterScopedViewer(t *testing.T) {
	// Viewer on cluster "prod" only.
	e := New([]Binding{{Role: RoleViewer, Cluster: "prod"}})

	if e.IsAdmin() {
		t.Error("cluster viewer is not global admin")
	}
	if !e.CanView("prod", "anything") {
		t.Error("should view its cluster")
	}
	if !e.CanView("prod", "") {
		t.Error("empty namespace should match whole cluster")
	}
	if e.CanView("staging", "anything") {
		t.Error("should NOT view a different cluster")
	}
	if e.CanInvestigate("prod", "anything") {
		t.Error("viewer should NOT investigate")
	}
}

func TestEnforcer_ScopeMatchCaseSensitive(t *testing.T) {
	// A binding for "prod" must NOT grant a case-variant cluster name, which could
	// be a distinct cluster — scope matching is case-sensitive.
	e := New([]Binding{{Role: RoleViewer, Cluster: "prod", Namespace: "payments"}})
	if e.CanView("Prod", "payments") {
		t.Error("cluster scope must be case-sensitive (Prod != prod)")
	}
	if e.CanView("prod", "Payments") {
		t.Error("namespace scope must be case-sensitive (Payments != payments)")
	}
	if !e.CanView("prod", "payments") {
		t.Error("exact-case match should still be granted")
	}
}

func TestEnforcer_NamespaceScoped(t *testing.T) {
	// Operator only in prod/payments.
	e := New([]Binding{{Role: RoleOperator, Cluster: "prod", Namespace: "payments"}})

	if !e.CanView("prod", "payments") {
		t.Error("should view its namespace")
	}
	if !e.CanInvestigate("prod", "payments") {
		t.Error("operator should investigate its namespace")
	}
	if e.CanView("prod", "checkout") {
		t.Error("should NOT view a different namespace in the same cluster")
	}
	if e.CanView("staging", "payments") {
		t.Error("should NOT view the same namespace in a different cluster")
	}
	// A cluster-wide query (empty namespace) must NOT be satisfied by a
	// namespace-scoped binding — that would leak every namespace via the _all
	// sentinel (the FIX-1 blocker). Use CanViewCluster for existence/enumeration.
	if e.CanView("prod", "") {
		t.Error("namespace-scoped binding must NOT grant the cluster-wide (_all) query")
	}
	// ...but the cluster still EXISTS for this subject (enumeration), so the
	// cluster list shows it.
	if !e.CanViewCluster("prod") {
		t.Error("namespace-scoped viewer should still see its cluster exists")
	}
	if e.CanViewCluster("staging") {
		t.Error("namespace-scoped viewer must NOT see an out-of-scope cluster")
	}
}

func TestEnforcer_MultipleBindings(t *testing.T) {
	e := New([]Binding{
		{Role: RoleViewer, Cluster: "prod"},
		{Role: RoleOperator, Cluster: "staging", Namespace: "demo"},
	})

	if !e.CanView("prod", "x") || e.CanInvestigate("prod", "x") {
		t.Error("prod is view-only")
	}
	if !e.CanInvestigate("staging", "demo") {
		t.Error("staging/demo is operator")
	}
	if e.CanInvestigate("staging", "other") {
		t.Error("staging/other is out of scope")
	}
}

func TestEnforcer_Empty(t *testing.T) {
	e := New(nil)
	if e.IsAdmin() {
		t.Error("no bindings => not admin")
	}
	if e.CanView("prod", "x") {
		t.Error("no bindings => no access")
	}
}

func TestEnforcer_WildcardNamespace(t *testing.T) {
	e := New([]Binding{{Role: RoleViewer, Cluster: "prod", Namespace: Wildcard}})
	if !e.CanView("prod", "anything") {
		t.Error("wildcard namespace should match anything in cluster")
	}
}

// TestEnforcer_NamespaceScopedNoAllLeak is the FIX-1 regression: a
// namespace-scoped viewer ({viewer, prod, team-a}) must be DENIED the cluster-
// wide (_all -> empty namespace) query — which previously authorized a
// prod/_all/secrets list — but ALLOWED its own namespace. The cluster-wide grant
// is reserved for bindings whose namespace is wildcard/empty.
func TestEnforcer_NamespaceScopedNoAllLeak(t *testing.T) {
	scoped := New([]Binding{{Role: RoleViewer, Cluster: "prod", Namespace: "team-a"}})
	if !scoped.CanView("prod", "team-a") {
		t.Error("namespace-scoped viewer must see its own namespace")
	}
	if scoped.CanView("prod", "") {
		t.Error("namespace-scoped viewer must NOT be granted the cluster-wide (_all) query")
	}
	if scoped.CanView("prod", "team-b") {
		t.Error("namespace-scoped viewer must NOT see a sibling namespace")
	}

	// A cluster-wide binding (empty namespace == wildcard) DOES satisfy _all.
	clusterWide := New([]Binding{{Role: RoleViewer, Cluster: "prod"}})
	if !clusterWide.CanView("prod", "") {
		t.Error("cluster-wide viewer should be granted the _all query")
	}
}

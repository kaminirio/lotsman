// Package rbac models role-based access control for lotsman. As a monitoring
// product, roles scope *visibility* to clusters and namespaces (rather than
// gating deployment actions per project/environment).
//
// A user holds a set of Bindings. Each binding grants a Role over a scope: a
// cluster (and optionally a namespace within it). The wildcard "*" matches any
// cluster or namespace, so an admin binding {Role: admin, Cluster: "*"} grants
// global visibility. The Enforcer answers "can this subject see this
// cluster/namespace?" — the only authorization question the engine and API ask.
package rbac

// Wildcard matches any cluster or namespace in a Binding scope.
const Wildcard = "*"

// Role is an RBAC role slug. The constants below are the only valid values.
type Role = string

// Role constants in hierarchical order: Admin > Operator > Viewer.
//   - Viewer:   read-only visibility into the scoped clusters/namespaces.
//   - Operator: viewer + may trigger investigations (mutations) in scope.
//   - Admin:    full access across all clusters/namespaces.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Action constants name the operations the API authorizes.
const (
	ActionView        = "view"        // read incidents/clusters/streams
	ActionInvestigate = "investigate" // trigger an on-demand investigation
)

// rolePermissions maps each role to the actions it permits.
var rolePermissions = map[Role][]string{
	RoleAdmin:    {ActionView, ActionInvestigate},
	RoleOperator: {ActionView, ActionInvestigate},
	RoleViewer:   {ActionView},
}

// roleHierarchy orders roles from least to most powerful (higher index = more).
var roleHierarchy = []Role{RoleViewer, RoleOperator, RoleAdmin}

var roleIndex = func() map[Role]int {
	m := make(map[Role]int, len(roleHierarchy))
	for i, r := range roleHierarchy {
		m[r] = i
	}
	return m
}()

// IsValidRole reports whether role is one of the known role slugs.
func IsValidRole(role Role) bool {
	_, ok := roleIndex[role]
	return ok
}

// IsRoleAtLeast reports whether role is at least as powerful as minRole.
// Returns false if either role is unknown.
func IsRoleAtLeast(role, minRole Role) bool {
	ri, ok := roleIndex[role]
	if !ok {
		return false
	}
	mi, ok := roleIndex[minRole]
	if !ok {
		return false
	}
	return ri >= mi
}

// roleCanPerform reports whether role permits action.
func roleCanPerform(role, action string) bool {
	for _, a := range rolePermissions[role] {
		if a == action {
			return true
		}
	}
	return false
}

// Binding grants a Role over a cluster scope (optionally a single namespace).
// Cluster or Namespace may be Wildcard ("*") to match anything. An empty
// Namespace is treated as the wildcard (the binding covers the whole cluster).
type Binding struct {
	Role      Role   `json:"role"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

func (b Binding) namespace() string {
	if b.Namespace == "" {
		return Wildcard
	}
	return b.Namespace
}

// scopeMatch reports whether a binding scope pattern covers a value. Matching is
// case-SENSITIVE: Kubernetes cluster and namespace names are case-sensitive, so a
// binding for "prod" must not grant "Prod"/"PROD" (which could be a distinct
// cluster), avoiding accidental over-grant across case-colliding names.
func scopeMatch(pattern, value string) bool {
	return pattern == Wildcard || pattern == value
}

// Enforcer evaluates access decisions for a fixed set of bindings (one user).
// The zero value denies everything; build one with New.
type Enforcer struct {
	bindings []Binding
}

// New builds an Enforcer over the given bindings.
func New(bindings []Binding) *Enforcer { return &Enforcer{bindings: bindings} }

// IsAdmin reports whether any binding grants admin globally (cluster "*").
func (e *Enforcer) IsAdmin() bool {
	for _, b := range e.bindings {
		if b.Role == RoleAdmin && b.Cluster == Wildcard {
			return true
		}
	}
	return false
}

// CanAccess reports whether the subject may perform action on the given
// cluster/namespace.
//
// An empty namespace is the cluster-wide query ("may the subject act across the
// WHOLE cluster?"), and is granted ONLY by a binding that itself covers the
// whole cluster — i.e. its namespace is the wildcard (an empty binding Namespace
// is normalized to Wildcard by Binding.namespace). A namespace-scoped binding
// does NOT satisfy a cluster-wide query: scopeMatch(b.namespace(), "") is true
// only when b.namespace() == Wildcard, because EqualFold(nonEmpty, "") is false.
// A query for a SPECIFIC namespace is granted by a binding whose namespace is
// the wildcard or matches that namespace. (To answer "does the cluster exist for
// this subject at all?", regardless of namespace scope, use CanViewCluster.)
func (e *Enforcer) CanAccess(action, cluster, namespace string) bool {
	for _, b := range e.bindings {
		if !roleCanPerform(b.Role, action) {
			continue
		}
		if !scopeMatch(b.Cluster, cluster) {
			continue
		}
		if scopeMatch(b.namespace(), namespace) {
			return true
		}
	}
	return false
}

// CanViewCluster reports whether the subject has ANY view-permitting binding
// matching the cluster, regardless of namespace scope. It answers the cluster
// EXISTENCE question for enumeration (e.g. the cluster list): a namespace-scoped
// viewer should still see that their cluster exists. It deliberately ignores the
// namespace, so unlike CanView(cluster, "") it is NOT a cluster-wide access
// grant — use CanView for data access.
func (e *Enforcer) CanViewCluster(cluster string) bool {
	for _, b := range e.bindings {
		if !roleCanPerform(b.Role, ActionView) {
			continue
		}
		if scopeMatch(b.Cluster, cluster) {
			return true
		}
	}
	return false
}

// CanView is shorthand for CanAccess(ActionView, ...).
func (e *Enforcer) CanView(cluster, namespace string) bool {
	return e.CanAccess(ActionView, cluster, namespace)
}

// CanInvestigate is shorthand for CanAccess(ActionInvestigate, ...).
func (e *Enforcer) CanInvestigate(cluster, namespace string) bool {
	return e.CanAccess(ActionInvestigate, cluster, namespace)
}

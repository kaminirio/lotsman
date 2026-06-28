// Package store is the control-plane persistence boundary. Lotsman persists only
// its own derived state — incidents, change history, clusters, config, users —
// and queries telemetry (logs/metrics) live through agents (ADR-0004). First
// implementation: PostgreSQL via pgx (ADR-0005). The scaffold ships an in-memory
// implementation.
package store

import (
	"context"
	"errors"

	"lotsman/internal/model"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("lotsman: not found")

// Store persists control-plane state.
type Store interface {
	SaveIncident(ctx context.Context, inc *model.Incident) error
	GetIncident(ctx context.Context, id string) (*model.Incident, error)
	ListIncidents(ctx context.Context, f IncidentFilter) ([]*model.Incident, error)

	SaveCluster(ctx context.Context, c Cluster) error
	ListClusters(ctx context.Context) ([]Cluster, error)
}

// IncidentFilter narrows ListIncidents.
type IncidentFilter struct {
	Cluster string
	Status  model.IncidentStatus
	Limit   int
}

// Cluster is a registered cluster plus its agent connection state. Field shape
// (env/region) maps onto the clusters table.
type Cluster struct {
	Name         string `json:"name"`
	Env          string `json:"env"`
	Region       string `json:"region"`
	Connected    bool   `json:"connected"`
	AgentVersion string `json:"agent_version,omitempty"`
	// Mode is "connected" for a cluster currently reachable through the registry
	// (a direct provider or a live agent link); empty for clusters known only from
	// the persisted store. Derived at read time by the API, not persisted.
	Mode string `json:"mode,omitempty"`
}

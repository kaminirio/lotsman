package sources

// provider is the default Provider implementation: a simple bundle of the four
// per-cluster sources. The agent constructs one from concrete adapters; tests
// construct one from fakes; sources/remote constructs one from proxy adapters.
type provider struct {
	cluster     string
	logs        LogSource
	metrics     MetricSource
	deployments DeploymentSource
	resources   ClusterSource
}

// NewProvider bundles four sources for a cluster into a Provider.
func NewProvider(cluster string, logs LogSource, metrics MetricSource, deployments DeploymentSource, resources ClusterSource) Provider {
	return &provider{
		cluster:     cluster,
		logs:        logs,
		metrics:     metrics,
		deployments: deployments,
		resources:   resources,
	}
}

func (p *provider) Cluster() string               { return p.cluster }
func (p *provider) Logs() LogSource               { return p.logs }
func (p *provider) Metrics() MetricSource         { return p.metrics }
func (p *provider) Deployments() DeploymentSource { return p.deployments }
func (p *provider) Resources() ClusterSource      { return p.resources }

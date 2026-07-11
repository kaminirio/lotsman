# lotsman-agent

The in-cluster Lotsman agent: reads cluster data through the source-agnostic
adapters (Kubernetes, Loki, VictoriaMetrics, ArgoCD) and dials **out** to a central
`lotsman-control-plane`. Install **one release per monitored cluster**, each with a
unique `config.cluster` and the token shared with the control plane.

```sh
helm install lotsman-agent ./lotsman-agent \
  -n lotsman --create-namespace \
  --set config.cluster=prod-eu \
  --set config.controlPlaneAddr=lotsman.example.com:9090 \
  --set agentToken.value="$SHARED_TOKEN"
```

⚠ `rbac.envReveal` + `allowEnvReveal` grant the agent cluster-wide Secret reads —
opt-in, trusted single-tenant clusters only. See [`../README.md`](../README.md) for
the full values reference, backend endpoints, and the env-reveal warning. All values
are documented inline in [`values.yaml`](./values.yaml).

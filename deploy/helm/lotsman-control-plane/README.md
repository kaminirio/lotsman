# lotsman-control-plane

The central Lotsman server: terminates agent links, runs the correlation engine,
persists derived state, and serves the API + embedded UI. Install **one** centrally.

```sh
# Agent mode (default) — agents in other clusters connect with a shared token:
helm install lotsman ./lotsman-control-plane \
  -n lotsman --create-namespace \
  --set agentToken.value=$(openssl rand -hex 24)

# Direct mode — read this cluster in-process, no agent:
helm install lotsman ./lotsman-control-plane \
  -n lotsman --create-namespace \
  --set config.directMode=true --set config.cluster=local
```

See [`../README.md`](../README.md) for layouts, multi-cluster setup, exposing the
gateway port, and the full values reference. All values are documented inline in
[`values.yaml`](./values.yaml).

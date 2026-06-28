# ArgoCD auth for the lotsman agent

The in-cluster `argocd-server` requires auth on `/api/v1/applications`, and by
default redirects HTTP->HTTPS with a self-signed cert (the agent uses
`http.DefaultClient`, which then fails TLS verification). Two cluster-side tweaks
make the adapter work over plain HTTP with a Bearer token:

1. Serve plain HTTP (no TLS redirect):
   ```sh
   kubectl -n argocd patch configmap argocd-cmd-params-cm --type merge \
     -p '{"data":{"server.insecure":"true"}}'
   kubectl -n argocd rollout restart deploy/argocd-server
   ```

2. Create a read-only apiKey account `lotsman` and grant it applications get/list:
   ```sh
   kubectl -n argocd patch configmap argocd-cm --type merge \
     -p '{"data":{"accounts.lotsman":"apiKey"}}'
   kubectl -n argocd patch configmap argocd-rbac-cm --type merge -p '{"data":{"policy.csv":
     "p, role:lotsman-ro, applications, get, */*, allow\np, role:lotsman-ro, applications, list, */*, allow\ng, lotsman, role:lotsman-ro\n"}}'
   ```

3. Mint a token (admin session -> account token) and store it for the agent:
   ```sh
   ADMIN_PW=$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
   SESSION=$(curl -s http://argocd-server.argocd.svc/api/v1/session -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | jq -r .token)
   TOKEN=$(curl -s http://argocd-server.argocd.svc/api/v1/account/lotsman/token -H "Authorization: Bearer $SESSION" -d '{}' | jq -r .token)
   kubectl -n lotsman create secret generic lotsman-argocd-token --from-literal=token="$TOKEN"
   ```

4. The agent (21-agent.yaml) reads it via:
   ```yaml
   - name: LOTSMAN_ARGOCD_TOKEN
     valueFrom:
       secretKeyRef: { name: lotsman-argocd-token, key: token }
   ```
   (applied here as a `kubectl patch`; bake it into 21-agent.yaml for a permanent setup).

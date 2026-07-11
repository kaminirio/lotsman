// REST client for the Lotsman control plane:
// a thin apiFetch<T>() over fetch() with HttpOnly session cookies, a CSRF
// header on every request, and 401 -> /login redirect. Domain types are hand-
// written (no codegen), mirroring the Go structs in internal/model + internal/store.

const API_URL = process.env.NEXT_PUBLIC_API_URL || ''

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      'X-Requested-With': 'lotsman',
      ...(init?.headers || {}),
    },
  })

  if (res.status === 401 && typeof window !== 'undefined' && window.location.pathname !== '/login') {
    window.location.href = '/login'
  }

  if (!res.ok) {
    let message = res.statusText
    try {
      const body = await res.json()
      message = body.error || message
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, message)
  }

  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

// ---- Domain types (mirror internal/model + internal/store) ----

export type Severity = 0 | 1 | 2 | 3
export const SEVERITY_LABELS = ['info', 'warning', 'error', 'critical'] as const

export type SignalKind = 'log' | 'metric' | 'change' | 'k8s_event'
export type IncidentStatus = 'open' | 'investigating' | 'resolved' | 'closed'

export interface ResourceRef {
  cluster: string
  namespace?: string
  kind?: string
  name?: string
  pod?: string
}

export interface ChangeRef {
  source: string
  app: string
  revision: string
  synced_at: string
  url?: string
}

export interface Signal {
  id: string
  kind: SignalKind
  resource: ResourceRef
  timestamp: string
  severity: Severity
  source: string
  title: string
  message?: string
  labels?: Record<string, string>
  change?: ChangeRef
}

export interface Hypothesis {
  summary: string
  confidence: number
  category: string
  evidence?: Signal[]
  change?: ChangeRef
}

export interface Incident {
  id: string
  resource: ResourceRef
  title: string
  status: IncidentStatus
  severity: Severity
  opened_at: string
  updated_at: string
  resolved_at?: string
  timeline: Signal[]
  hypotheses: Hypothesis[]
}

export interface Cluster {
  name: string
  env?: string
  region?: string
  connected: boolean
  mode?: string
  agent_version?: string
}

// ---- Endpoints ----

export const listIncidents = () => apiFetch<Incident[]>('/api/v1/incidents')
export const getIncident = (id: string) => apiFetch<Incident>(`/api/v1/incidents/${id}`)
export const listClusters = () => apiFetch<Cluster[]>('/api/v1/clusters')

export interface InvestigateRequest {
  cluster: string
  namespace: string
  kind: string
  name: string
}
export const investigate = (req: InvestigateRequest) =>
  apiFetch<Incident>('/api/v1/investigate', { method: 'POST', body: JSON.stringify(req) })

// ---- Optional LLM incident explainer (assistive, off by default) ----
// POST /api/v1/incidents/{id}/explain (no body). Turns deterministic findings
// into a plain-English narrative + triage label via a small CPU model. The
// feature is OFF by default: when LOTSMAN_LLM_URL is unset the endpoint returns
// 503, which surfaces here as an ApiError with status 503 so the UI can show a
// "not configured" note rather than a generic error. 502 = model call failed.
export interface Explanation {
  summary: string
  category: string
  confidence: string
  model: string
}

// HTTP status returned when the explainer is not enabled (LOTSMAN_LLM_URL unset).
export const EXPLAINER_DISABLED_STATUS = 503

export const explainIncident = (id: string) =>
  apiFetch<Explanation>(`/api/v1/incidents/${encodeURIComponent(id)}/explain`, { method: 'POST' })

// ---- Workloads / Pods browser (mirror internal/sources Pod/Container types) ----

// Source of a container env var. The backend sets `kind` to either a Kubernetes
// reference kind (`secret`/`configMap`/`field`/`resource`) or a workload kind
// (`Deployment`/`StatefulSet`/`DaemonSet`/`Pod`) for inline/literal values, so
// every env var now carries a source. `key` is present only for secret/configMap
// refs (the data key); workload/field sources omit it.
export type EnvVarSourceKind =
  | 'secret'
  | 'configMap'
  | 'field'
  | 'resource'
  | 'Deployment'
  | 'StatefulSet'
  | 'DaemonSet'
  | 'Pod'
export interface EnvVarSource {
  // Backend may add new kinds; keep the union open with a string fallback so an
  // unknown kind still renders (normalized for display) instead of breaking types.
  kind: EnvVarSourceKind | (string & {})
  name?: string
  key?: string
}
export interface ContainerEnvVar {
  name: string
  value?: string
  masked?: boolean
  source?: EnvVarSource
}
// State is one of 'running' | 'waiting' | 'terminated', or absent when the
// kubelet has not reported the container yet. Reason carries the kubelet reason
// for a waiting/terminated container (e.g. 'CrashLoopBackOff', 'Completed').
export type ContainerState = 'running' | 'waiting' | 'terminated'
export interface Container {
  name: string
  image: string
  ready?: boolean
  state?: ContainerState | (string & {})
  reason?: string
  restart_count?: number
  env?: ContainerEnvVar[]
}
export interface WorkloadRef {
  kind: string
  name: string
  // Present on the /workloads listing endpoint; absent when used as a pod owner ref.
  cluster?: string
  namespace?: string
}
export interface Pod {
  name: string
  namespace: string
  phase: string
  ready: boolean
  restarts: number
  node?: string
  owner?: WorkloadRef
  containers: Container[]
}
export interface PodLogsResult {
  pod: string
  namespace: string
  container: string
  lines: string
  truncated?: boolean
}

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/pods?workload=<name>  -> Pod[]
export const listPods = (cluster: string, namespace: string, workload?: string) =>
  apiFetch<Pod[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(namespace)}/pods${
      workload ? `?workload=${encodeURIComponent(workload)}` : ''
    }`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/pods/{pod}/logs?container=&tail=  -> PodLogsResult
export const getPodLogs = (cluster: string, namespace: string, pod: string, container?: string, tail?: number) =>
  apiFetch<PodLogsResult>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(
      namespace,
    )}/pods/${encodeURIComponent(pod)}/logs?${new URLSearchParams({
      ...(container ? { container } : {}),
      ...(tail ? { tail: String(tail) } : {}),
    })}`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/workloads  -> WorkloadRef[]
// namespace = "_all" for all namespaces.
export const listWorkloads = (cluster: string, namespace: string) =>
  apiFetch<WorkloadRef[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(namespace)}/workloads`,
  )

// One point in a workload's rollout history (newest-first), reconstructed from
// the Deployment's ReplicaSet revisions. `current` marks the live revision.
export interface WorkloadRevision {
  revision: number
  images: string[]
  created_at: string
  change_cause?: string
  current?: boolean
}

// GET .../workloads/{kind}/{name}/history  -> WorkloadRevision[]
export const getWorkloadHistory = (cluster: string, namespace: string, kind: string, name: string) =>
  apiFetch<WorkloadRevision[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(
      namespace,
    )}/workloads/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/history`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/events?since=&limit=  -> Signal[]
// namespace = "_all" for all namespaces.
export const listEvents = (cluster: string, namespace: string, since = '60m', limit = 200) =>
  apiFetch<Signal[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(
      namespace,
    )}/events?${new URLSearchParams({ since, limit: String(limit) })}`,
  )

// Special namespace sentinel meaning "all namespaces" in the path segment.
export const ALL_NAMESPACES = '_all'

// ---- Nodes (cluster-scoped, NO namespace) ----
// Mirror the Go Node struct the control plane serves for the CLUSTER nav group.
// Capacities are raw Kubernetes quantities (e.g. "16412236Ki"); the UI formats
// them for display.
export interface Node {
  name: string
  ready: boolean
  roles?: string[]
  kubelet_version?: string
  os?: string
  arch?: string
  os_image?: string
  kernel_version?: string
  container_runtime?: string
  internal_ip?: string
  cpu_capacity?: string
  memory_capacity?: string
  pods_capacity?: string
  cpu_allocatable?: string
  memory_allocatable?: string
  unschedulable?: boolean
  created_at: string
}

// GET /api/v1/clusters/{cluster}/nodes  -> Node[]   (cluster-scoped, NO namespace)
export const listNodes = (cluster: string) =>
  apiFetch<Node[]>(`/api/v1/clusters/${encodeURIComponent(cluster)}/nodes`)

// ---- Config browser: ConfigMaps / Secrets / Certificates ----
// Mirror the Go structs the control plane serves for the CONFIG nav group. The
// LIST routes accept `namespace = "_all"` like /pods and /events. Secrets are
// behind agent reveal/RBAC: when reveal is disabled the secrets routes return
// 502, surfaced here as an ApiError the views detect (SECRET_ACCESS_DISABLED_STATUS).

export interface ConfigMapRef {
  cluster: string
  namespace: string
  name: string
  keys: string[]
}
export interface ConfigMapDetail {
  cluster: string
  namespace: string
  name: string
  data: Record<string, string>
}

// Parsed X.509 certificate data (for TLS secrets). Dates are RFC3339 strings;
// `expired` / `expires_in_days` are precomputed by the backend so the UI never
// has to parse the cert itself.
export interface CertInfo {
  subject_cn: string
  issuer_cn: string
  not_before: string
  not_after: string
  dns_names?: string[]
  serial?: string
  is_ca?: boolean
  key_algorithm?: string
  expired: boolean
  expires_in_days: number
}

export interface SecretEntry {
  key: string
  value?: string
  masked?: boolean
  is_cert?: boolean
}
export interface SecretRef {
  cluster: string
  namespace: string
  name: string
  type: string
  keys: string[]
  is_tls?: boolean
  cert?: CertInfo
}
export interface SecretDetail {
  cluster: string
  namespace: string
  name: string
  type: string
  entries: SecretEntry[]
  cert?: CertInfo
}

// HTTP status the secrets routes return when agent reveal/RBAC is not enabled.
// The Secrets / Certificates views detect this (like the AI panel detects 503)
// and show a friendly "secret access not enabled" note instead of an error.
export const SECRET_ACCESS_DISABLED_STATUS = 502

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/configmaps  -> ConfigMapRef[]
// namespace = "_all" for all namespaces.
export const listConfigMaps = (cluster: string, namespace: string) =>
  apiFetch<ConfigMapRef[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(namespace)}/configmaps`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/configmaps/{name}  -> ConfigMapDetail
export const getConfigMap = (cluster: string, namespace: string, name: string) =>
  apiFetch<ConfigMapDetail>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(
      namespace,
    )}/configmaps/${encodeURIComponent(name)}`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/secrets  -> SecretRef[]
// namespace = "_all" for all namespaces.
export const listSecrets = (cluster: string, namespace: string) =>
  apiFetch<SecretRef[]>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(namespace)}/secrets`,
  )

// GET /api/v1/clusters/{cluster}/namespaces/{namespace}/secrets/{name}  -> SecretDetail
export const getSecret = (cluster: string, namespace: string, name: string) =>
  apiFetch<SecretDetail>(
    `/api/v1/clusters/${encodeURIComponent(cluster)}/namespaces/${encodeURIComponent(
      namespace,
    )}/secrets/${encodeURIComponent(name)}`,
  )

// ---- Strong RBAC (admin, read-only) ----
// Config-driven authorization defined in LOTSMAN_SSO_CONFIG. These endpoints are
// admin-gated by the control plane: 401 when unauthenticated, 403 when the caller
// is not an admin. There are no mutation endpoints — bindings are configured at
// deploy time, so this view is read-only.

// A single user/group -> role binding, optionally scoped to a cluster/namespace.
// Empty `cluster`/`namespace` means "all" at that scope.
export interface RbacBinding {
  subject: string
  role: string
  cluster: string
  namespace: string
}
export interface RbacGroupBinding {
  group: string
  role: string
  cluster: string
  namespace: string
}
export interface RbacConfig {
  roles: string[]
  bindings: RbacBinding[]
  group_bindings: RbacGroupBinding[]
}

// Effective (resolved) binding for one user — role + scope, without the subject.
export interface RbacEffectiveBinding {
  role: string
  cluster: string
  namespace: string
}
export interface RbacEffective {
  user: string
  bindings: RbacEffectiveBinding[]
  is_admin: boolean
}

// GET /api/v1/admin/rbac/config  -> RbacConfig   (admin only)
export const getRbacConfig = () => apiFetch<RbacConfig>('/api/v1/admin/rbac/config')

// GET /api/v1/admin/rbac/effective?user=<login>  -> RbacEffective   (admin only)
export const getRbacEffective = (user: string) =>
  apiFetch<RbacEffective>(`/api/v1/admin/rbac/effective?${new URLSearchParams({ user })}`)

// ---- First-party users (ADR-0011, admin only) ----
// Mirror the Go userView (internal/api/users.go): never carries password_hash or
// sso_subject. sso_provider is "" for local-only accounts.

export interface LotsmanUser {
  id: string
  username: string
  email: string
  is_admin: boolean
  active: boolean
  sso_provider: string
  created_at: string
}

export interface CreateUserInput {
  username: string
  email: string
  password: string
  is_admin: boolean
}

// Partial update: only the provided fields are changed. `password` resets the
// account's password; omit it to leave the credential untouched.
export interface UpdateUserInput {
  is_admin?: boolean
  active?: boolean
  password?: string
}

// GET /api/v1/users -> { users: [...] }  (admin only)
export const listUsers = () =>
  apiFetch<{ users: LotsmanUser[] }>('/api/v1/users').then((r) => r.users)

// POST /api/v1/users -> LotsmanUser  (409 on a duplicate username/email)
export const createUser = (input: CreateUserInput) =>
  apiFetch<LotsmanUser>('/api/v1/users', { method: 'POST', body: JSON.stringify(input) })

// PATCH /api/v1/users/{id} -> LotsmanUser  (409 if it would remove the last admin)
export const updateUser = (id: string, patch: UpdateUserInput) =>
  apiFetch<LotsmanUser>(`/api/v1/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  })

// DELETE /api/v1/users/{id} -> 204  (409 if it would remove the last admin)
export const deleteUser = (id: string) =>
  apiFetch<void>(`/api/v1/users/${encodeURIComponent(id)}`, { method: 'DELETE' })

// ---- Per-cluster agent enrollment tokens (ADR-0010, admin only) ----
// Only the plaintext token returned by createEnrollmentToken ever exposes secret
// material, and only once; list/defaults never do.

// Presentation hints for assembling the `helm install lotsman-agent` command.
// `durable` is false on an in-memory store, in which case minting is disabled.
export interface EnrollmentDefaults {
  gateway_addr: string
  chart: string
  chart_version: string
  namespace: string
  durable: boolean
}

// Token metadata (no plaintext, no hash). expires_at is null/absent = never.
export interface EnrollmentToken {
  id: string
  cluster: string
  created_at: string
  expires_at?: string | null
  revoked: boolean
}

// The create response additionally carries the one-time plaintext token.
export interface EnrollmentTokenCreated extends EnrollmentToken {
  token: string
}

// GET /api/v1/enrollment-defaults -> EnrollmentDefaults  (admin only)
export const getEnrollmentDefaults = () => apiFetch<EnrollmentDefaults>('/api/v1/enrollment-defaults')

// GET /api/v1/enrollment-tokens -> { tokens: [...] }  (admin only; empty on a
// non-durable store rather than erroring, so the Clusters page still loads).
export const listEnrollmentTokens = () =>
  apiFetch<{ tokens: EnrollmentToken[] }>('/api/v1/enrollment-tokens').then((r) => r.tokens)

// POST /api/v1/enrollment-tokens -> EnrollmentTokenCreated  (503 on a non-durable
// store). ttlHours of 0 mints a non-expiring token.
export const createEnrollmentToken = (cluster: string, ttlHours: number) =>
  apiFetch<EnrollmentTokenCreated>('/api/v1/enrollment-tokens', {
    method: 'POST',
    body: JSON.stringify({ cluster, ttl_hours: ttlHours }),
  })

// POST /api/v1/enrollment-tokens/{id}/revoke -> 204  (404 for an unknown id)
export const revokeEnrollmentToken = (id: string) =>
  apiFetch<void>(`/api/v1/enrollment-tokens/${encodeURIComponent(id)}/revoke`, { method: 'POST' })

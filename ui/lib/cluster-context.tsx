'use client'

// Cross-view selection state for the Lens-style console: which cluster and which
// namespace the resource views operate on. Persisted to localStorage so switching
// nav items keeps the selection, and so a reload restores it.

import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  type ReactNode,
} from 'react'
import { listClusters, ALL_NAMESPACES, type Cluster } from './api'

const CLUSTER_KEY = 'lotsman.cluster'
const NAMESPACE_KEY = 'lotsman.namespace'

// Common namespaces always offered in the namespace filter, even before any
// results have been fetched (matches the demo control plane's seeded set).
export const COMMON_NAMESPACES = ['demo', 'argocd', 'monitoring', 'lotsman', 'kube-system'] as const

interface ClusterContextValue {
  clusters: Cluster[]
  clustersLoading: boolean
  clustersError: string | null
  cluster: string
  setCluster: (name: string) => void
  namespace: string // ALL_NAMESPACES ("_all") means "All namespaces"
  setNamespace: (ns: string) => void
  refreshClusters: () => void
}

const ClusterContext = createContext<ClusterContextValue | null>(null)

function readStored(key: string): string | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStored(key: string, value: string) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(key, value)
  } catch {
    /* storage unavailable (private mode etc.) — selection just won't persist */
  }
}

export function ClusterProvider({ children }: { children: ReactNode }) {
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [clustersLoading, setClustersLoading] = useState(true)
  const [clustersError, setClustersError] = useState<string | null>(null)

  // Start from deterministic defaults so the server-rendered HTML and the first
  // client render agree (reading localStorage during render would make them
  // differ → hydration mismatch). The persisted selection is loaded right after
  // mount, before any data is requested, in the effect below.
  const [cluster, setClusterState] = useState<string>('')
  const [namespace, setNamespaceState] = useState<string>(ALL_NAMESPACES)

  // Hydrate the persisted selection once, after mount (client-only).
  useEffect(() => {
    const storedCluster = readStored(CLUSTER_KEY)
    if (storedCluster) setClusterState(storedCluster)
    const storedNamespace = readStored(NAMESPACE_KEY)
    if (storedNamespace) setNamespaceState(storedNamespace)
  }, [])

  const setCluster = useCallback((name: string) => {
    setClusterState(name)
    writeStored(CLUSTER_KEY, name)
  }, [])

  const setNamespace = useCallback((ns: string) => {
    setNamespaceState(ns)
    writeStored(NAMESPACE_KEY, ns)
  }, [])

  const refreshClusters = useCallback(() => {
    let cancelled = false
    setClustersLoading(true)
    setClustersError(null)
    listClusters()
      .then((cs) => {
        if (cancelled) return
        setClusters(cs)
        // Default-select the first connected cluster (the live `local` cluster),
        // unless a valid selection is already in place.
        setClusterState((current) => {
          if (current && cs.some((c) => c.name === current)) return current
          const firstConnected = cs.find((c) => c.connected) ?? cs[0]
          if (firstConnected) {
            writeStored(CLUSTER_KEY, firstConnected.name)
            return firstConnected.name
          }
          return current
        })
      })
      .catch((e) => {
        if (!cancelled) setClustersError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setClustersLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const cancel = refreshClusters()
    return cancel
  }, [refreshClusters])

  return (
    <ClusterContext.Provider
      value={{
        clusters,
        clustersLoading,
        clustersError,
        cluster,
        setCluster,
        namespace,
        setNamespace,
        refreshClusters,
      }}
    >
      {children}
    </ClusterContext.Provider>
  )
}

export function useCluster(): ClusterContextValue {
  const ctx = useContext(ClusterContext)
  if (!ctx) throw new Error('useCluster must be used within a ClusterProvider')
  return ctx
}

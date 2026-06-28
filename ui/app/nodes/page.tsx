'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { listNodes, type Node } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  formatMemoryQuantity,
  relativeTime,
} from '@/lib/styles'

// Nodes overview — cluster-scoped, so the toolbar's namespace filter is hidden.
// Dense Lens-style table mirroring the Pods view; one row per node.
export default function NodesPage() {
  const { cluster } = useCluster()
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    listNodes(cluster)
      .then((n) => {
        if (!cancelled) setNodes(n)
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [cluster, nonce])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return nodes
    return nodes.filter(
      (n) =>
        n.name.toLowerCase().includes(q) ||
        (n.roles ?? []).some((r) => r.toLowerCase().includes(q)) ||
        (n.internal_ip ?? '').toLowerCase().includes(q) ||
        (n.kubelet_version ?? '').toLowerCase().includes(q),
    )
  }, [nodes, search])

  return (
    <>
      <ResourceToolbar
        title="Nodes"
        showNamespace={false}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter nodes…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list nodes." />
        ) : loading && nodes.length === 0 ? (
          <LoadingState label="Loading nodes…" />
        ) : error ? (
          <ErrorState label="Failed to load nodes" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No nodes match the filter.' : 'No nodes in this cluster.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Name</th>
                <th className={denseThCls}>Status</th>
                <th className={denseThCls}>Roles</th>
                <th className={denseThCls}>Version</th>
                <th className={denseThCls}>OS/Arch</th>
                <th className={denseThCls}>Internal IP</th>
                <th className={denseThCls}>CPU</th>
                <th className={denseThCls}>Memory</th>
                <th className={denseThCls}>Age</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((n) => (
                <NodeRow key={n.name} node={n} />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}

function NodeRow({ node }: { node: Node }) {
  const roles = node.roles ?? []
  return (
    <tr className="border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)]">
      <td className={`${denseTdCls} font-tech text-slate-200`}>{node.name}</td>
      <td className={denseTdCls}>
        <span className="inline-flex items-center gap-1.5">
          <span
            className={`h-2 w-2 rounded-full ${node.ready ? 'bg-emerald-400' : 'bg-red-400'}`}
            aria-hidden="true"
          />
          <span className={node.ready ? 'text-emerald-400' : 'text-red-400'}>
            {node.ready ? 'Ready' : 'NotReady'}
          </span>
          {node.unschedulable && (
            <span className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20">
              SchedulingDisabled
            </span>
          )}
        </span>
      </td>
      <td className={denseTdCls}>
        {roles.length > 0 ? (
          <span className="inline-flex flex-wrap gap-1">
            {roles.map((r) => (
              <span
                key={r}
                className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-slate-500/10 text-slate-300 ring-1 ring-slate-700/40"
              >
                {r}
              </span>
            ))}
          </span>
        ) : (
          <span className="font-tech text-[12px] text-slate-600">&lt;none&gt;</span>
        )}
      </td>
      <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
        {node.kubelet_version || '—'}
      </td>
      <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
        {node.os || node.arch ? `${node.os ?? '?'}/${node.arch ?? '?'}` : '—'}
      </td>
      <td className={`${denseTdCls} font-tech text-[12px] text-slate-500`}>{node.internal_ip || '—'}</td>
      <td className={`${denseTdCls} font-tech text-slate-400`}>{node.cpu_capacity || '—'}</td>
      <td
        className={`${denseTdCls} font-tech text-slate-400`}
        title={node.memory_capacity || undefined}
      >
        {formatMemoryQuantity(node.memory_capacity)}
      </td>
      <td className={`${denseTdCls} font-tech text-[12px] text-slate-500`}>
        {relativeTime(node.created_at)}
      </td>
    </tr>
  )
}

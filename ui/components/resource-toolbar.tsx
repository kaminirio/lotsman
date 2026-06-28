'use client'

import { type ReactNode } from 'react'
import { ALL_NAMESPACES } from '@/lib/api'
import { useCluster, COMMON_NAMESPACES } from '@/lib/cluster-context'
import { toolbarInputCls, toolbarBtnCls } from '@/lib/styles'

function IconRefresh({ spinning }: { spinning?: boolean }) {
  return (
    <svg
      className={`h-3.5 w-3.5 ${spinning ? 'animate-spin' : ''}`}
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      strokeWidth={1.9}
      aria-hidden="true"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M16.023 9.348h4.992V4.356M3.985 19.644v-4.992h4.992m-4.318-3.34a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99M3.343 12.69l3.182 3.182a8.25 8.25 0 0013.803-3.7"
      />
    </svg>
  )
}

interface ResourceToolbarProps {
  title: string
  /** Namespaces discovered from the current result set, merged with common ones. */
  discoveredNamespaces?: string[]
  /** Hide the namespace filter for views that aren't namespace-scoped. */
  showNamespace?: boolean
  search?: string
  onSearch?: (value: string) => void
  searchPlaceholder?: string
  onRefresh?: () => void
  refreshing?: boolean
  /** Extra controls rendered between search and refresh. */
  extra?: ReactNode
}

export function ResourceToolbar({
  title,
  discoveredNamespaces = [],
  showNamespace = true,
  search,
  onSearch,
  searchPlaceholder = 'Filter…',
  onRefresh,
  refreshing,
  extra,
}: ResourceToolbarProps) {
  const { cluster, namespace, setNamespace } = useCluster()

  // Build the namespace option list: common namespaces + whatever the current
  // results surfaced, de-duplicated and sorted, with "All namespaces" pinned first.
  const nsOptions = Array.from(
    new Set([...COMMON_NAMESPACES, ...discoveredNamespaces].filter(Boolean)),
  ).sort((a, b) => a.localeCompare(b))

  return (
    <div className="flex min-h-12 flex-wrap items-center gap-3 border-b border-slate-800 bg-[var(--surface)] px-4 py-2">
      <div className="flex min-w-0 items-baseline gap-2">
        <h1 className="truncate text-sm font-semibold tracking-tight text-slate-100">{title}</h1>
        <span className="font-tech text-[11px] text-slate-600">
          {cluster || '—'}
          {showNamespace && (
            <> · {namespace === ALL_NAMESPACES ? 'all namespaces' : namespace}</>
          )}
        </span>
      </div>

      <div className="ml-auto flex flex-wrap items-center gap-2">
        {showNamespace && (
          <label className="flex items-center gap-1.5">
            <span className="sr-only">Namespace</span>
            <select
              aria-label="Namespace filter"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              className={`${toolbarInputCls} max-w-[12rem]`}
            >
              <option value={ALL_NAMESPACES}>All namespaces</option>
              {nsOptions.map((ns) => (
                <option key={ns} value={ns}>
                  {ns}
                </option>
              ))}
            </select>
          </label>
        )}

        {onSearch && (
          <input
            type="search"
            aria-label="Filter rows"
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            placeholder={searchPlaceholder}
            className={`${toolbarInputCls} w-44`}
          />
        )}

        {extra}

        {onRefresh && (
          <button
            type="button"
            onClick={onRefresh}
            disabled={refreshing}
            className={toolbarBtnCls}
            aria-label="Refresh"
          >
            <IconRefresh spinning={refreshing} />
            Refresh
          </button>
        )}
      </div>
    </div>
  )
}

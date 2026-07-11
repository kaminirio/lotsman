'use client'

import type { ReactNode } from 'react'
import { usePathname } from 'next/navigation'
import { ClusterProvider } from '@/lib/cluster-context'
import { ClusterSelector } from '@/components/cluster-selector'
import { useAuth } from '@/lib/auth-context'
import { focusRingCls } from '@/lib/styles'

// ---- Nav tree (Lens-style grouped sections) ----

interface NavItemDef {
  label: string
  href: string
  icon: ReactNode
  exact?: boolean
  /** Render only for admins (strong RBAC). */
  adminOnly?: boolean
}
interface NavGroup {
  label: string
  items: NavItemDef[]
}

function IconGauge() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
    </svg>
  )
}
function IconServer() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z" />
    </svg>
  )
}
function IconCube() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
    </svg>
  )
}
function IconLayers() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M6.429 9.75L2.25 12l4.179 2.25m0-4.5l5.571 3 5.571-3m-11.142 0L2.25 7.5 12 2.25l9.75 5.25-4.179 2.25m0 0L12 12.75l-5.571-3m11.142 0l4.179 2.25L12 17.25l-9.75-5.25 4.179-2.25" />
    </svg>
  )
}
function IconSliders() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75" />
    </svg>
  )
}
function IconKey() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H9v1.5H7.5v1.5H4.5a1.125 1.125 0 01-1.125-1.125v-2.4c0-.298.119-.585.33-.796l5.073-5.073c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z" />
    </svg>
  )
}
function IconCertificate() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />
    </svg>
  )
}
function IconBell() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
    </svg>
  )
}
function IconPulse() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 12h3l2.25-6 4.5 12 2.25-6h4.5" />
    </svg>
  )
}
function IconShield() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M12 2.714l7.5 2.25v5.036c0 4.708-3.2 9.11-7.5 10.286-4.3-1.176-7.5-5.578-7.5-10.286V4.964l7.5-2.25zM9.75 11.25a2.25 2.25 0 114.5 0 2.25 2.25 0 01-4.5 0z" />
    </svg>
  )
}

const NAV: NavGroup[] = [
  {
    label: 'Cluster',
    items: [
      { label: 'Overview', href: '/overview', icon: <IconGauge /> },
      { label: 'Nodes', href: '/nodes', icon: <IconServer /> },
    ],
  },
  {
    label: 'Workloads',
    items: [
      { label: 'Pods', href: '/pods', icon: <IconCube /> },
      { label: 'Workloads', href: '/workloads', icon: <IconLayers /> },
    ],
  },
  {
    label: 'Config',
    items: [
      { label: 'ConfigMaps', href: '/configmaps', icon: <IconSliders /> },
      { label: 'Secrets', href: '/secrets', icon: <IconKey /> },
      { label: 'Certificates', href: '/certificates', icon: <IconCertificate /> },
    ],
  },
  {
    label: 'Events',
    items: [{ label: 'Events', href: '/events', icon: <IconBell /> }],
  },
  {
    label: 'Lotsman',
    items: [{ label: 'Incidents', href: '/incidents', icon: <IconPulse /> }],
  },
  {
    label: 'Administration',
    items: [{ label: 'Access', href: '/admin/rbac', icon: <IconShield />, adminOnly: true }],
  },
]

function isActive(pathname: string, item: NavItemDef): boolean {
  if (item.exact) return pathname === item.href
  return pathname === item.href || pathname.startsWith(`${item.href}/`)
}

function NavLink({ item, active }: { item: NavItemDef; active: boolean }) {
  return (
    <a
      href={item.href}
      aria-current={active ? 'page' : undefined}
      className={[
        'group relative flex items-center gap-2.5 rounded-md px-3 py-1.5 text-[13px] transition-colors',
        active
          ? 'bg-[var(--accent-soft)] font-medium text-slate-100'
          : 'text-slate-400 hover:bg-[var(--surface-hover)] hover:text-slate-200',
        focusRingCls,
      ].join(' ')}
    >
      {active && (
        <span className="absolute left-0 top-1/2 h-4 w-0.5 -translate-y-1/2 rounded-r-full bg-[var(--accent)]" />
      )}
      <span className={active ? 'text-[var(--accent-hover)]' : 'text-slate-500 group-hover:text-slate-300'}>
        {item.icon}
      </span>
      <span className="flex-1 truncate">{item.label}</span>
    </a>
  )
}

export function LayoutShell({ children }: { children: ReactNode }) {
  const pathname = usePathname()
  const { isAdmin } = useAuth()

  // Hide admin-only items for non-admins; drop a group entirely once it's empty.
  const navGroups = NAV.map((group) => ({
    ...group,
    items: group.items.filter((item) => !item.adminOnly || isAdmin),
  })).filter((group) => group.items.length > 0)

  // The login route renders without the console chrome.
  if (pathname === '/login') {
    return <div className="relative z-10 flex h-screen w-full">{children}</div>
  }

  return (
    <ClusterProvider>
      <div className="relative z-10 flex h-screen w-full">
        {/* Skip-to-content link (UI-5): visually hidden until focused, lets
            keyboard users jump past the sidebar nav to the main landmark. */}
        <a
          href="#main-content"
          className={[
            'sr-only rounded-md bg-[var(--surface)] px-3 py-1.5 text-[13px] font-medium text-slate-100',
            'focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50',
            focusRingCls,
          ].join(' ')}
        >
          Skip to content
        </a>
        {/* Sidebar — slightly darker than the content area (Lens-style). */}
        <aside className="flex w-60 shrink-0 flex-col border-r border-slate-800 bg-[var(--bg-deep)]">
          {/* Wordmark */}
          <div className="flex h-12 items-center gap-2.5 border-b border-slate-800 px-4">
            <div className="flex h-6 w-6 items-center justify-center rounded-md bg-[var(--accent-soft)] ring-1 ring-indigo-500/20 shadow-[0_0_16px_-4px_var(--accent-glow)]">
              <span className="block h-2.5 w-2.5 rounded-sm bg-[var(--accent)]" />
            </div>
            <span className="text-sm font-bold tracking-tight text-slate-100">Lotsman</span>
          </div>

          {/* Cluster selector */}
          <div className="border-b border-slate-800 px-3 py-3">
            <ClusterSelector />
          </div>

          {/* Nav tree */}
          <nav className="flex-1 overflow-y-auto px-2 py-3">
            {navGroups.map((group) => (
              <div key={group.label} className="mb-3">
                <div className="px-3 pb-1 pt-1.5 text-[10px] font-semibold uppercase tracking-wider text-slate-600">
                  {group.label}
                </div>
                <div className="space-y-0.5">
                  {group.items.map((item) => (
                    <NavLink key={item.href} item={item} active={isActive(pathname, item)} />
                  ))}
                </div>
              </div>
            ))}
          </nav>

          <div className="border-t border-slate-800 px-4 py-2.5 font-tech text-[10px] text-slate-600">
            v0.1.0 · scaffold
          </div>
        </aside>

        {/* Content — each view owns its toolbar + body. The `main` element is
            the primary landmark; `id`/`tabIndex` make it the skip-link target. */}
        <main
          id="main-content"
          tabIndex={-1}
          className="flex min-w-0 flex-1 flex-col overflow-hidden bg-[var(--bg-base)] focus:outline-none"
        >
          {children}
        </main>
      </div>
    </ClusterProvider>
  )
}

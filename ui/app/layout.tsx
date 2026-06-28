import type { Metadata } from 'next'
import './globals.css'
import { AuthProvider } from '@/lib/auth-context'
import { LayoutShell } from '@/components/layout-shell'

export const metadata: Metadata = {
  title: 'Lotsman — Kubernetes Monitoring & Investigation',
  description: 'Investigate incidents across logs, metrics, and deployments.',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dark">
      <body className="flex h-screen overflow-hidden text-slate-50" style={{ backgroundColor: 'var(--bg-base)' }}>
        <AuthProvider>
          <LayoutShell>{children}</LayoutShell>
        </AuthProvider>
      </body>
    </html>
  )
}

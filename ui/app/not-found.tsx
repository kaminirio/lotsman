// 404 boundary (UI-3). Rendered inside the console chrome for unmatched routes;
// under `output: 'export'` Next emits this as the static 404 page.

import Link from 'next/link'
import { toolbarBtnCls } from '@/lib/styles'

export default function NotFound() {
  return (
    <div className="flex flex-1 items-center justify-center p-8">
      <div className="w-full max-w-md space-y-4 rounded-2xl border border-slate-800 bg-[var(--surface)] p-6 text-center shadow-card">
        <p className="font-tech text-4xl font-semibold text-slate-700">404</p>
        <div className="space-y-1">
          <h1 className="text-sm font-semibold tracking-tight text-slate-100">Page not found</h1>
          <p className="text-[13px] text-slate-500">
            The page you’re looking for doesn’t exist or has moved.
          </p>
        </div>
        <div className="flex justify-center">
          <Link href="/overview" className={toolbarBtnCls}>
            Back to overview
          </Link>
        </div>
      </div>
    </div>
  )
}

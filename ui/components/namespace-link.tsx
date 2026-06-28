'use client'

import { useRouter } from 'next/navigation'
import { useCluster } from '@/lib/cluster-context'
import { focusRingCls } from '@/lib/styles'

/**
 * NamespaceLink renders a namespace in a breadcrumb as a clickable target that
 * selects that cluster + namespace in the shared context and jumps to the Pods
 * list scoped to it. Detail pages drive their own data from the URL, but a
 * namespace click is a navigation, so it writes the context the list views read.
 */
export function NamespaceLink({
  cluster,
  namespace,
  className,
}: {
  cluster: string
  namespace: string
  className?: string
}) {
  const router = useRouter()
  const { setCluster, setNamespace } = useCluster()
  return (
    <button
      type="button"
      onClick={() => {
        setCluster(cluster)
        setNamespace(namespace)
        router.push('/pods')
      }}
      className={`rounded underline-offset-2 transition-colors hover:text-slate-300 hover:underline ${focusRingCls} ${className ?? ''}`}
    >
      {namespace}
    </button>
  )
}

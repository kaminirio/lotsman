'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { LoadingState } from '@/components/view-states'

// The root route is the console entry point: redirect to the cluster Overview.
// Done client-side so it works under static export (no server redirects).
export default function HomePage() {
  const router = useRouter()
  useEffect(() => {
    router.replace('/overview')
  }, [router])
  return <LoadingState label="Opening overview…" />
}

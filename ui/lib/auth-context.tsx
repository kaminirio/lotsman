'use client'

// Auth context for the Lotsman UI. See ADR-0006/0007.

import { createContext, useContext, useEffect, useState, useCallback, type ReactNode } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import { getMe, getProviders, logout as doLogout, type AuthUser } from './auth'

interface AuthContextValue {
  user: AuthUser | null
  loading: boolean
  authEnabled: boolean
  isAdmin: boolean
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  loading: true,
  authEnabled: false,
  isAdmin: false,
  logout: async () => {},
})

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)
  const [authEnabled, setAuthEnabled] = useState(false)
  const pathname = usePathname()
  const router = useRouter()

  useEffect(() => {
    async function check() {
      try {
        const providers = await getProviders()
        const enabled = providers.enabled !== false && !!providers.github
        setAuthEnabled(enabled)

        if (!enabled) {
          setUser({ login: 'anonymous', email: '', name: 'Anonymous', provider: 'none' })
          if (pathname === '/login') router.replace('/')
          return
        }

        const me = await getMe()
        setUser(me)
        if (!me && pathname !== '/login') router.replace('/login')
        if (me && pathname === '/login') router.replace('/')
      } catch {
        setUser({ login: 'anonymous', email: '', name: 'Anonymous', provider: 'none' })
      } finally {
        setLoading(false)
      }
    }
    check()
  }, [pathname, router])

  const logout = useCallback(async () => {
    await doLogout()
    setUser(null)
    window.location.href = '/login'
  }, [])

  const isAdmin = user?.is_admin === true

  return (
    <AuthContext.Provider value={{ user, loading, authEnabled, isAdmin, logout }}>{children}</AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}

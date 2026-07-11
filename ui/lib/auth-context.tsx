'use client'

// Auth context for the Lotsman UI. See ADR-0006/0007.

import { createContext, useContext, useEffect, useState, useCallback, type ReactNode } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import { getMe, logout as doLogout, type AuthUser } from './auth'

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
        // First-party auth is always enforced (ADR-0011): resolve the current
        // session directly. A backend running with auth disabled (local dev)
        // answers /auth/me with the anonymous principal, so getMe still returns a
        // user there and no redirect happens.
        setAuthEnabled(true)
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

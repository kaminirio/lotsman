import { apiFetch } from './api'

export interface AuthUser {
  login: string
  email: string
  name: string
  provider: string
  // Strong RBAC (config-driven via LOTSMAN_SSO_CONFIG). Optional so older or
  // anonymous /auth/me responses (auth disabled) still satisfy the type.
  is_admin?: boolean
  groups?: string[]
}

// SSO providers offered alongside first-party login (ADR-0011).
export type SsoProvider = 'github' | 'google' | 'azure'

// Shape of GET /auth/providers. `local` (username/password) is always true; each
// SSO provider is true only when its credentials are configured.
export interface Providers {
  local: boolean
  github: boolean
  google: boolean
  azure: boolean
}

export async function getProviders(): Promise<Providers> {
  return apiFetch<Providers>('/auth/providers')
}

// login authenticates a first-party account and, on success, sets the session
// cookie (POST /auth/login). Rejects with an ApiError (401) on bad credentials.
export async function login(username: string, password: string): Promise<void> {
  await apiFetch('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
}

export async function getMe(): Promise<AuthUser | null> {
  try {
    return await apiFetch<AuthUser>('/auth/me')
  } catch {
    return null
  }
}

export async function logout(): Promise<void> {
  await apiFetch('/auth/logout', { method: 'POST' })
}

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

export interface Providers {
  enabled: boolean
  github?: boolean
}

export async function getProviders(): Promise<Providers> {
  return apiFetch<Providers>('/auth/providers')
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

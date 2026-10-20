import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { apiFetch, clearUserToken, getUserToken, setUserToken } from '../lib/api'

interface LoginResponse {
  token: string
  user_id: string
  expires_in: number
}

interface NidStartResponse {
  authorize_url: string
  poll_token: string
  state: string
}

interface AuthContextType {
  userID: string | null
  isAuthenticated: boolean
  login: (email: string, password: string) => Promise<void>
  signup: (email: string, password: string) => Promise<void>
  loginWithNid: () => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextType | undefined>(undefined)

function decodeUserIDFromStorage(): string | null {
  return localStorage.getItem('nmail_user_id')
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [userID, setUserID] = useState<string | null>(() => (getUserToken() ? decodeUserIDFromStorage() : null))

  // If a /auth/nid/callback page already stashed a token, pick it up.
  useEffect(() => {
    if (getUserToken() && !userID) {
      setUserID(decodeUserIDFromStorage())
    }
  }, [userID])

  async function login(email: string, password: string) {
    const res = await apiFetch<LoginResponse>('/api/users/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    })
    setUserToken(res.token)
    localStorage.setItem('nmail_user_id', res.user_id)
    setUserID(res.user_id)
  }

  async function signup(email: string, password: string) {
    const res = await apiFetch<LoginResponse>('/api/users/signup', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    })
    setUserToken(res.token)
    localStorage.setItem('nmail_user_id', res.user_id)
    setUserID(res.user_id)
  }

  async function loginWithNid() {
    const res = await apiFetch<NidStartResponse>('/api/users/login/nid/start')
    window.location.href = res.authorize_url
  }

  function logout() {
    clearUserToken()
    localStorage.removeItem('nmail_user_id')
    setUserID(null)
  }

  return (
    <AuthContext.Provider value={{ userID, isAuthenticated: !!userID, login, signup, loginWithNid, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

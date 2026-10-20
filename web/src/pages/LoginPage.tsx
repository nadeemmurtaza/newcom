import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { brand } from '../config/brand'
import { ROUTES } from '../constants/routes'
import { ApiError } from '../lib/api'
import { useAuth } from '../providers/auth-provider'

export function LoginPage() {
  const { login, signup, loginWithNid } = useAuth()
  const navigate = useNavigate()
  const [mode, setMode] = useState<'login' | 'signup'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      if (mode === 'login') {
        await login(email, password)
      } else {
        await signup(email, password)
      }
      navigate(ROUTES.inbox)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Something went wrong')
    } finally {
      setBusy(false)
    }
  }

  async function handleNid() {
    setError(null)
    try {
      await loginWithNid()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'nID login is unavailable')
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-base-200 px-4">
      <div className="w-full max-w-sm rounded-box bg-base-100 p-8 shadow">
        <h1 className="mb-1 text-2xl font-semibold text-primary">{brand.identity.name}</h1>
        <p className="mb-6 text-sm text-base-content/60">{brand.identity.tagline}</p>

        <button onClick={handleNid} className="btn btn-primary w-full mb-4">
          Login with nID
        </button>

        <div className="divider text-xs text-base-content/40">
          or {mode === 'login' ? 'sign in' : 'sign up'} with a password
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-3">
          <input
            type="email"
            placeholder="Email"
            className="input input-bordered w-full"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
          <input
            type="password"
            placeholder="Password"
            className="input input-bordered w-full"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={mode === 'signup' ? 8 : undefined}
            required
          />
          {error && <p className="text-sm text-error">{error}</p>}
          <button type="submit" className="btn btn-outline w-full" disabled={busy}>
            {busy ? 'Please wait…' : mode === 'login' ? 'Sign in' : 'Create account'}
          </button>
        </form>

        <button
          onClick={() => {
            setMode(mode === 'login' ? 'signup' : 'login')
            setError(null)
          }}
          className="mt-4 w-full text-center text-xs text-base-content/50 link"
        >
          {mode === 'login' ? "Don't have an account? Sign up" : 'Already have an account? Sign in'}
        </button>
      </div>
    </div>
  )
}

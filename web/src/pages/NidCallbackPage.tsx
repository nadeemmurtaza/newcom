import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ROUTES } from '../constants/routes'
import { ApiError, apiFetch, setUserToken } from '../lib/api'

interface NidCallbackResponse {
  token: string
  user_id: string
  nid_account_id: string
  expires_in: number
}

/**
 * NID_REDIRECT_URI must point here, not at the backend directly -- nID
 * redirects the browser to this URL with ?code&state, and this page
 * forwards them to the backend's exchange endpoint so the resulting
 * session token lands in the SPA's own storage instead of a bare JSON
 * response the user would just see as text.
 */
export function NidCallbackPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [error, setError] = useState<string | null>(null)
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true

    const code = params.get('code')
    const state = params.get('state')
    if (!code || !state) {
      setError('Missing code or state from nID')
      return
    }

    apiFetch<NidCallbackResponse>(`/api/users/login/nid/callback?code=${encodeURIComponent(code)}&state=${encodeURIComponent(state)}`)
      .then((res) => {
        setUserToken(res.token)
        localStorage.setItem('nmail_user_id', res.user_id)
        navigate(ROUTES.inbox, { replace: true })
      })
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Login failed'))
  }, [params, navigate])

  return (
    <div className="flex min-h-screen items-center justify-center bg-base-200">
      {error ? (
        <div className="text-center">
          <p className="text-error mb-2">{error}</p>
          <a href={ROUTES.login} className="link">
            Back to login
          </a>
        </div>
      ) : (
        <span className="loading loading-spinner loading-lg text-primary" />
      )}
    </div>
  )
}

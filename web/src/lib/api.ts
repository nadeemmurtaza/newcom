/**
 * One API client, shared error handling. Every page imports this instead
 * of calling fetch() directly.
 */

const USER_TOKEN_KEY = 'nmail_user_token'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export function getUserToken(): string | null {
  return localStorage.getItem(USER_TOKEN_KEY)
}

export function setUserToken(token: string) {
  localStorage.setItem(USER_TOKEN_KEY, token)
}

export function clearUserToken() {
  localStorage.removeItem(USER_TOKEN_KEY)
}

async function parseError(res: Response): Promise<string> {
  try {
    const body = await res.json()
    return body.message || body.error || JSON.stringify(body)
  } catch {
    try {
      return await res.text()
    } catch {
      return res.statusText
    }
  }
}

/** Calls a /api/me/*, /api/stats, /api/domains (dashboard) etc. route authenticated by the user's session token. */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getUserToken()
  const headers = new Headers(init.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')

  const res = await fetch(path, { ...init, headers })
  if (!res.ok) throw new ApiError(res.status, await parseError(res))
  if (res.status === 204) return undefined as T
  return res.json()
}

/** Calls a route authenticated by an org API key (domain/mailbox admin). */
export async function apiKeyFetch<T>(apiKey: string, path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Authorization', apiKey)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')

  const res = await fetch(path, { ...init, headers })
  if (!res.ok) throw new ApiError(res.status, await parseError(res))
  if (res.status === 204) return undefined as T
  return res.json()
}

/** Calls a /api/mailbox/* route authenticated by a mailbox session token. */
export async function mailboxFetch<T>(mailboxToken: string, path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Authorization', `Bearer ${mailboxToken}`)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')

  const res = await fetch(path, { ...init, headers })
  if (!res.ok) throw new ApiError(res.status, await parseError(res))
  if (res.status === 204) return undefined as T
  return res.json()
}

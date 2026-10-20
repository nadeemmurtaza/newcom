import { useEffect, useState } from 'react'
import { apiFetch } from '../lib/api'
import type { MyMailboxSummary } from '../types'

/** Every mailbox the signed-in person owns -- personal plus every work mailbox assigned to them, across any org. */
export function useMyMailboxes() {
  const [mailboxes, setMailboxes] = useState<MyMailboxSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    apiFetch<MyMailboxSummary[]>('/api/me/mailboxes')
      .then((res) => {
        if (!cancelled) setMailboxes(res || [])
      })
      .catch((err) => {
        if (!cancelled) setError(err.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return { mailboxes, loading, error }
}

import { useCallback } from 'react'
import { apiFetch } from '../lib/api'

interface MailboxSessionResponse {
  token: string
  mailbox_id: string
  expires_in: number
}

function storageKey(mailboxID: string) {
  return `nmail_mailbox_token:${mailboxID}`
}

/**
 * Opens one of the signed-in person's own mailboxes (personal or any work
 * mailbox assigned to them) without ever touching its app password --
 * the user session already proves who they are. Tokens are cached in
 * localStorage per mailbox so switching between mailboxes doesn't re-mint
 * a session every time.
 */
export function useMailboxSession() {
  const getToken = useCallback(async (mailboxID: string): Promise<string> => {
    const cached = localStorage.getItem(storageKey(mailboxID))
    if (cached) return cached

    const res = await apiFetch<MailboxSessionResponse>(`/api/me/mailboxes/${mailboxID}/session`, {
      method: 'POST',
    })
    localStorage.setItem(storageKey(mailboxID), res.token)
    return res.token
  }, [])

  const invalidate = useCallback((mailboxID: string) => {
    localStorage.removeItem(storageKey(mailboxID))
  }, [])

  return { getToken, invalidate }
}

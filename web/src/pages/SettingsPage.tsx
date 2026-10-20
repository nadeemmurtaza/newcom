import { useState } from 'react'
import { brand } from '../config/brand'
import { useMyMailboxes } from '../hooks/useMyMailboxes'
import { apiFetch, ApiError } from '../lib/api'

interface PersonalMailboxResponse {
  mailbox_id: string
  email_address: string
  app_password: string
}

export function SettingsPage() {
  const { mailboxes, loading, error: listError } = useMyMailboxes()
  const [localPart, setLocalPart] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<PersonalMailboxResponse | null>(null)

  const hasPersonal = mailboxes.some((m) => m.kind === 'personal')

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const res = await apiFetch<PersonalMailboxResponse>('/api/me/personal-mailbox', {
        method: 'POST',
        body: JSON.stringify({ local_part: localPart }),
      })
      setCreated(res)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create mailbox')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex-1 overflow-y-auto p-4 md:p-8 max-w-2xl">
      <h1 className="text-xl font-semibold mb-6">Settings</h1>

      <section className="mb-8">
        <h2 className="text-sm font-medium text-base-content/60 mb-3">Your mailboxes</h2>
        {loading && <p className="text-sm text-base-content/50">Loading…</p>}
        {listError && <p className="text-sm text-error">{listError}</p>}
        <ul className="flex flex-col gap-2">
          {mailboxes.map((m) => (
            <li key={m.id} className="flex items-center justify-between rounded-field bg-base-100 px-4 py-2 text-sm">
              <span>{m.email_address}</span>
              <span className="badge badge-sm">{m.kind}</span>
            </li>
          ))}
        </ul>
      </section>

      {!hasPersonal && !created && (
        <section className="rounded-box bg-base-100 p-5">
          <h2 className="text-sm font-medium mb-3">Create your personal mailbox</h2>
          <form onSubmit={handleCreate} className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <input
                type="text"
                placeholder="yourname"
                className="input input-bordered input-sm flex-1"
                value={localPart}
                onChange={(e) => setLocalPart(e.target.value)}
                required
              />
              <span className="text-sm text-base-content/50">@{brand.identity.domain}</span>
            </div>
            {error && <p className="text-sm text-error">{error}</p>}
            <button type="submit" className="btn btn-primary btn-sm self-start" disabled={busy}>
              {busy ? 'Creating…' : 'Create mailbox'}
            </button>
          </form>
        </section>
      )}

      {created && (
        <section className="rounded-box bg-success/10 border border-success p-5">
          <p className="font-medium mb-2">Mailbox created: {created.email_address}</p>
          <p className="text-sm text-base-content/70 mb-1">
            App password (shown once -- save it if you need to log in from another mail client):
          </p>
          <code className="block rounded-field bg-base-200 px-3 py-2 text-sm">{created.app_password}</code>
        </section>
      )}
    </div>
  )
}

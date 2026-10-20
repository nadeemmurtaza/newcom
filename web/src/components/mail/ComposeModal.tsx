import { useState } from 'react'
import { sendMail } from '../../lib/mailApi'

export function ComposeModal({
  fromAddress,
  mailboxToken,
  onClose,
  onSent,
}: {
  fromAddress: string
  mailboxToken: string
  onClose: () => void
  onSent: () => void
}) {
  const [to, setTo] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSend() {
    setError(null)
    setBusy(true)
    try {
      await sendMail(mailboxToken, to, subject, body)
      onSent()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-black/30 p-0 sm:p-4">
      <div className="w-full sm:max-w-lg rounded-t-box sm:rounded-box bg-base-100 shadow-xl flex flex-col max-h-[90vh]">
        <div className="flex items-center justify-between border-b border-base-300 px-4 py-3">
          <span className="font-medium">New message</span>
          <button onClick={onClose} className="btn btn-ghost btn-sm btn-circle">
            ✕
          </button>
        </div>
        <div className="flex flex-col gap-2 p-4 overflow-y-auto">
          <div className="text-xs text-base-content/50">From: {fromAddress}</div>
          <input
            type="email"
            placeholder="To"
            className="input input-bordered input-sm"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
          <input
            type="text"
            placeholder="Subject"
            className="input input-bordered input-sm"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
          />
          <textarea
            placeholder="Write your message…"
            className="textarea textarea-bordered min-h-48 flex-1"
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
          {error && <p className="text-sm text-error">{error}</p>}
        </div>
        <div className="flex justify-end gap-2 border-t border-base-300 px-4 py-3">
          <button onClick={onClose} className="btn btn-ghost btn-sm">
            Discard
          </button>
          <button onClick={handleSend} className="btn btn-primary btn-sm" disabled={busy || !to || !subject}>
            {busy ? 'Sending…' : 'Send'}
          </button>
        </div>
      </div>
    </div>
  )
}

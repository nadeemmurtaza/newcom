import { useCallback, useEffect, useMemo, useState } from 'react'
import { ComposeModal } from '../components/mail/ComposeModal'
import { useMailboxSession } from '../hooks/useMailboxSession'
import { useMyMailboxes } from '../hooks/useMyMailboxes'
import { deleteMessage, getMessages, markRead, SYSTEM_FOLDERS, toggleStar, type SystemFolder } from '../lib/mailApi'
import type { Message } from '../types'

const FOLDER_LABELS: Record<SystemFolder, string> = {
  INBOX: 'Inbox',
  SENT: 'Sent',
  DRAFTS: 'Drafts',
  SPAM: 'Spam',
  TRASH: 'Trash',
  ARCHIVE: 'Archive',
}

export function MailPage() {
  const { mailboxes, loading: mailboxesLoading } = useMyMailboxes()
  const { getToken } = useMailboxSession()

  const [activeMailboxId, setActiveMailboxId] = useState<string | null>(null)
  const [folder, setFolder] = useState<SystemFolder>('INBOX')
  const [token, setToken] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [selected, setSelected] = useState<Message | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [composing, setComposing] = useState(false)

  useEffect(() => {
    if (!activeMailboxId && mailboxes.length > 0) {
      setActiveMailboxId(mailboxes[0].id)
    }
  }, [mailboxes, activeMailboxId])

  const activeMailbox = useMemo(() => mailboxes.find((m) => m.id === activeMailboxId) ?? null, [mailboxes, activeMailboxId])

  const refresh = useCallback(async () => {
    if (!activeMailboxId) return
    setLoading(true)
    setError(null)
    try {
      const t = await getToken(activeMailboxId)
      setToken(t)
      const msgs = await getMessages(t, activeMailboxId, folder)
      setMessages(msgs || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load messages')
    } finally {
      setLoading(false)
    }
  }, [activeMailboxId, folder, getToken])

  useEffect(() => {
    setSelected(null)
    refresh()
  }, [refresh])

  async function openMessage(m: Message) {
    setSelected(m)
    if (!m.is_read && token) {
      try {
        await markRead(token, m.id, true)
        setMessages((prev) => prev.map((x) => (x.id === m.id ? { ...x, is_read: true } : x)))
      } catch {
        // best-effort; reading the message still succeeded for the user
      }
    }
  }

  async function handleStar(m: Message, e: React.MouseEvent) {
    e.stopPropagation()
    if (!token) return
    try {
      await toggleStar(token, m.id)
      setMessages((prev) => prev.map((x) => (x.id === m.id ? { ...x, is_starred: !x.is_starred } : x)))
    } catch {
      // ignore
    }
  }

  async function handleDelete(m: Message) {
    if (!token) return
    try {
      await deleteMessage(token, m.id)
      setMessages((prev) => prev.filter((x) => x.id !== m.id))
      setSelected(null)
    } catch {
      // ignore
    }
  }

  if (mailboxesLoading) {
    return <div className="flex flex-1 items-center justify-center text-base-content/50">Loading your mailboxes…</div>
  }

  if (mailboxes.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center text-center p-8">
        <div>
          <p className="mb-2 text-base-content/70">You don't have a mailbox yet.</p>
          <a href="/settings" className="link link-primary">
            Create your personal mailbox
          </a>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-1 overflow-hidden">
      {/* Folder + mailbox rail */}
      <div className="hidden md:flex w-48 flex-col border-r border-base-300 bg-base-100 p-3 gap-1">
        <select
          className="select select-sm select-bordered mb-3"
          value={activeMailboxId ?? ''}
          onChange={(e) => setActiveMailboxId(e.target.value)}
        >
          {mailboxes.map((m) => (
            <option key={m.id} value={m.id}>
              {m.email_address} {m.kind === 'personal' ? '(personal)' : ''}
            </option>
          ))}
        </select>
        <button onClick={() => setComposing(true)} className="btn btn-primary btn-sm mb-3">
          Compose
        </button>
        {SYSTEM_FOLDERS.map((f) => (
          <button
            key={f}
            onClick={() => setFolder(f)}
            className={`text-left rounded-field px-3 py-1.5 text-sm ${folder === f ? 'bg-primary text-primary-content' : 'hover:bg-base-200'}`}
          >
            {FOLDER_LABELS[f]}
          </button>
        ))}
      </div>

      {/* Message list */}
      <div className={`flex flex-col border-r border-base-300 bg-base-100 overflow-y-auto ${selected ? 'hidden lg:flex lg:w-96' : 'flex-1 lg:w-96 lg:flex-none'}`}>
        <div className="md:hidden flex items-center gap-2 p-3 border-b border-base-300">
          <select
            className="select select-sm select-bordered flex-1"
            value={activeMailboxId ?? ''}
            onChange={(e) => setActiveMailboxId(e.target.value)}
          >
            {mailboxes.map((m) => (
              <option key={m.id} value={m.id}>
                {m.email_address}
              </option>
            ))}
          </select>
          <select className="select select-sm select-bordered" value={folder} onChange={(e) => setFolder(e.target.value as SystemFolder)}>
            {SYSTEM_FOLDERS.map((f) => (
              <option key={f} value={f}>
                {FOLDER_LABELS[f]}
              </option>
            ))}
          </select>
          <button onClick={() => setComposing(true)} className="btn btn-primary btn-sm">
            +
          </button>
        </div>

        {loading && <div className="p-4 text-sm text-base-content/50">Loading…</div>}
        {error && <div className="p-4 text-sm text-error">{error}</div>}
        {!loading && !error && messages.length === 0 && (
          <div className="p-8 text-center text-sm text-base-content/50">No messages in {FOLDER_LABELS[folder]}.</div>
        )}

        {messages.map((m) => (
          <button
            key={m.id}
            onClick={() => openMessage(m)}
            className={`flex flex-col gap-0.5 border-b border-base-200 px-4 py-3 text-left hover:bg-base-200 ${
              selected?.id === m.id ? 'bg-base-200' : ''
            } ${!m.is_read ? 'font-semibold' : ''}`}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="truncate text-sm">{m.sender}</span>
              <span onClick={(e) => handleStar(m, e)} className="shrink-0 cursor-pointer">
                {m.is_starred ? '⭐' : '☆'}
              </span>
            </div>
            <span className="truncate text-sm">{m.subject || '(no subject)'}</span>
            <span className="truncate text-xs text-base-content/50">{m.body_text}</span>
          </button>
        ))}
      </div>

      {/* Reading pane */}
      <div className={`flex-1 flex-col bg-base-100 overflow-y-auto ${selected ? 'flex' : 'hidden lg:flex'}`}>
        {selected ? (
          <div className="p-6">
            <button onClick={() => setSelected(null)} className="btn btn-ghost btn-sm mb-4 lg:hidden">
              ← Back
            </button>
            <h2 className="text-xl font-semibold mb-1">{selected.subject || '(no subject)'}</h2>
            <div className="mb-4 flex items-center justify-between text-sm text-base-content/60">
              <span>
                {selected.sender} · {new Date(selected.created_at).toLocaleString()}
              </span>
              <button onClick={() => handleDelete(selected)} className="btn btn-ghost btn-xs">
                🗑 Delete
              </button>
            </div>
            {selected.body_html ? (
              <div className="prose max-w-none" dangerouslySetInnerHTML={{ __html: selected.body_html }} />
            ) : (
              <p className="whitespace-pre-wrap">{selected.body_text}</p>
            )}
          </div>
        ) : (
          <div className="flex flex-1 items-center justify-center text-base-content/40">Select a message</div>
        )}
      </div>

      {composing && activeMailbox && token && (
        <ComposeModal
          fromAddress={activeMailbox.email_address}
          mailboxToken={token}
          onClose={() => setComposing(false)}
          onSent={refresh}
        />
      )}
    </div>
  )
}

import { mailboxFetch } from './api'
import type { Message } from '../types'

export const SYSTEM_FOLDERS = ['INBOX', 'SENT', 'DRAFTS', 'SPAM', 'TRASH', 'ARCHIVE'] as const
export type SystemFolder = (typeof SYSTEM_FOLDERS)[number]

export function getMessages(mailboxToken: string, mailboxId: string, folder: SystemFolder) {
  const params = new URLSearchParams({ mailbox_id: mailboxId, folder })
  return mailboxFetch<Message[]>(mailboxToken, `/api/mailbox/messages?${params}`)
}

export function toggleStar(mailboxToken: string, messageId: string) {
  return mailboxFetch<void>(mailboxToken, `/api/mailbox/messages/${messageId}/star`, { method: 'PUT' })
}

export function markRead(mailboxToken: string, messageId: string, read: boolean) {
  return mailboxFetch<void>(mailboxToken, `/api/mailbox/messages/${messageId}/read?read=${read}`, { method: 'PUT' })
}

export function moveMessage(mailboxToken: string, messageId: string, folder: SystemFolder) {
  return mailboxFetch<void>(mailboxToken, `/api/mailbox/messages/${messageId}/move?folder=${folder}`, { method: 'PUT' })
}

export function deleteMessage(mailboxToken: string, messageId: string) {
  return mailboxFetch<void>(mailboxToken, `/api/mailbox/messages/${messageId}`, { method: 'DELETE' })
}

export function sendMail(mailboxToken: string, to: string, subject: string, body: string) {
  return mailboxFetch<{ message_id: string; status: string }>(mailboxToken, '/api/mailbox/send', {
    method: 'POST',
    body: JSON.stringify({ to, subject, body }),
  })
}

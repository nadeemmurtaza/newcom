export type MailboxKind = 'personal' | 'work'

export interface MyMailboxSummary {
  id: string
  email_address: string
  kind: MailboxKind
  org_id: string
  created_at: string
}

export interface Folder {
  id: string
  name: string
  is_system: boolean
}

export interface Message {
  id: string
  sender: string
  subject: string
  body_text: string
  body_html: string
  is_read: boolean
  is_starred: boolean
  created_at: string
  thread_id: string
  category: string
  snoozed_until?: string
}

export interface Domain {
  id: string
  domain_name: string
  status?: string
  is_verified: boolean
  custom_tracking_url?: string
  dns_records?: DnsRecord[]
}

export interface DnsRecord {
  type: string
  host: string
  value: string
  priority?: number
}

export interface MailboxListItem {
  id: string
  email_address: string
  is_catch_all: boolean
  kind: MailboxKind
  created_at: string
}

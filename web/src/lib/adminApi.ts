import { apiFetch, apiKeyFetch } from './api'
import type { Domain, MailboxListItem } from '../types'

export interface OrgSummary {
  id: string
  name: string
  tier: string
}

export interface ApiKeySummary {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
}

export function listMyOrgs() {
  return apiFetch<OrgSummary[]>('/api/me/orgs')
}

export function createOrg(name: string) {
  return apiFetch<{ id: string; name: string }>('/api/orgs', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function listApiKeys(orgId: string) {
  return apiFetch<ApiKeySummary[]>(`/api/me/orgs/${orgId}/api-keys`)
}

export function createApiKey(orgId: string, name: string) {
  return apiFetch<{ id: string; key: string; name: string }>(`/api/me/orgs/${orgId}/api-keys`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function getOrgDomains(orgId: string) {
  return apiFetch<Domain[]>(`/api/domains?org_id=${orgId}`)
}

export function createDomain(apiKey: string, domainName: string) {
  return apiKeyFetch<Domain>(apiKey, '/api/domains', {
    method: 'POST',
    body: JSON.stringify({ domain_name: domainName }),
  })
}

export function getDomain(apiKey: string, domainId: string) {
  return apiKeyFetch<Domain>(apiKey, `/api/domains/${domainId}`)
}

export function createMailbox(apiKey: string, domainId: string, localPart: string, assigneeNidAccountId?: string) {
  return apiKeyFetch<{ id: string; email_address: string; app_password?: string }>(
    apiKey,
    `/api/domains/${domainId}/mailboxes`,
    {
      method: 'POST',
      body: JSON.stringify({
        local_part: localPart,
        ...(assigneeNidAccountId ? { assignee_nid_account_id: assigneeNidAccountId } : {}),
      }),
    },
  )
}

export function listMailboxesForDomain(apiKey: string, domainId: string) {
  return apiKeyFetch<MailboxListItem[]>(apiKey, `/api/domains/${domainId}/mailboxes`)
}

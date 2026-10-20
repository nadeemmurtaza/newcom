import { useEffect, useState } from 'react'
import {
  createApiKey,
  createDomain,
  createMailbox,
  createOrg,
  getDomain,
  getOrgDomains,
  listApiKeys,
  listMailboxesForDomain,
  listMyOrgs,
  type ApiKeySummary,
  type OrgSummary,
} from '../lib/adminApi'
import { ApiError } from '../lib/api'
import type { Domain, MailboxListItem } from '../types'

export function DomainsPage() {
  const [orgs, setOrgs] = useState<OrgSummary[]>([])
  const [activeOrgId, setActiveOrgId] = useState<string | null>(null)
  const [newOrgName, setNewOrgName] = useState('')

  const [apiKey, setApiKey] = useState<string | null>(null)
  const [apiKeys, setApiKeys] = useState<ApiKeySummary[]>([])
  const [showKeys, setShowKeys] = useState(false)

  const [domains, setDomains] = useState<Domain[]>([])
  const [selectedDomain, setSelectedDomain] = useState<Domain | null>(null)
  const [mailboxes, setMailboxes] = useState<MailboxListItem[]>([])

  const [newDomainName, setNewDomainName] = useState('')
  const [newLocalPart, setNewLocalPart] = useState('')
  const [justCreatedPassword, setJustCreatedPassword] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listMyOrgs().then((res) => {
      setOrgs(res || [])
      if (res?.length) setActiveOrgId(res[0].id)
    })
  }, [])

  useEffect(() => {
    if (!activeOrgId) return
    setSelectedDomain(null)
    getOrgDomains(activeOrgId)
      .then((res) => setDomains(res || []))
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load domains'))
    listApiKeys(activeOrgId)
      .then((res) => setApiKeys(res || []))
      .catch(() => setApiKeys([]))
  }, [activeOrgId])

  useEffect(() => {
    if (!selectedDomain || !apiKey) {
      setMailboxes([])
      return
    }
    listMailboxesForDomain(apiKey, selectedDomain.id)
      .then((res) => setMailboxes(res || []))
      .catch(() => setMailboxes([]))

    // The list view (getOrgDomains) never includes DNS records -- only
    // the per-domain admin read does.
    getDomain(apiKey, selectedDomain.id)
      .then((full) => setSelectedDomain((prev) => (prev && prev.id === full.id ? { ...prev, ...full } : prev)))
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedDomain?.id, apiKey])

  async function handleCreateOrg(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      const res = await createOrg(newOrgName)
      setOrgs((prev) => [...prev, { id: res.id, name: res.name, tier: 'Free' }])
      setActiveOrgId(res.id)
      setNewOrgName('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create organization')
    }
  }

  async function handleCreateApiKey() {
    if (!activeOrgId) return
    setError(null)
    try {
      const res = await createApiKey(activeOrgId, 'Dashboard')
      setApiKey(res.key)
      setApiKeys((prev) => [...prev, { id: res.id, name: res.name, created_at: new Date().toISOString(), last_used_at: null }])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create API key')
    }
  }

  async function handleCreateDomain(e: React.FormEvent) {
    e.preventDefault()
    if (!apiKey) {
      setError('Generate an API key below first')
      return
    }
    setError(null)
    try {
      const res = await createDomain(apiKey, newDomainName)
      setDomains((prev) => [...prev, res])
      setNewDomainName('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create domain')
    }
  }

  async function handleCreateMailbox(e: React.FormEvent) {
    e.preventDefault()
    if (!apiKey || !selectedDomain) return
    setError(null)
    setJustCreatedPassword(null)
    try {
      const res = await createMailbox(apiKey, selectedDomain.id, newLocalPart)
      setMailboxes((prev) => [
        ...prev,
        { id: res.id, email_address: res.email_address, is_catch_all: false, kind: 'work', created_at: new Date().toISOString() },
      ])
      setNewLocalPart('')
      if (res.app_password) setJustCreatedPassword(res.app_password)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create mailbox')
    }
  }

  return (
    <div className="flex-1 overflow-y-auto p-4 md:p-8 max-w-3xl">
      <h1 className="text-xl font-semibold mb-6">Domains</h1>

      {orgs.length === 0 ? (
        <section className="rounded-box bg-base-100 p-5 mb-6">
          <h2 className="text-sm font-medium mb-3">Create your organization</h2>
          <form onSubmit={handleCreateOrg} className="flex gap-2">
            <input
              className="input input-bordered input-sm flex-1"
              placeholder="Acme Inc"
              value={newOrgName}
              onChange={(e) => setNewOrgName(e.target.value)}
              required
            />
            <button className="btn btn-primary btn-sm">Create</button>
          </form>
        </section>
      ) : (
        <div className="mb-6 flex items-center gap-2">
          <select className="select select-sm select-bordered" value={activeOrgId ?? ''} onChange={(e) => setActiveOrgId(e.target.value)}>
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
        </div>
      )}

      {error && <p className="mb-4 text-sm text-error">{error}</p>}

      {activeOrgId && (
        <>
          {!selectedDomain ? (
            <>
              <section className="rounded-box bg-base-100 p-5 mb-6">
                <h2 className="text-sm font-medium mb-3">Add a domain</h2>
                <form onSubmit={handleCreateDomain} className="flex gap-2">
                  <input
                    className="input input-bordered input-sm flex-1"
                    placeholder="example.com"
                    value={newDomainName}
                    onChange={(e) => setNewDomainName(e.target.value)}
                    required
                  />
                  <button className="btn btn-primary btn-sm">Add</button>
                </form>
              </section>

              <ul className="flex flex-col gap-2 mb-8">
                {domains.map((d) => (
                  <li key={d.id}>
                    <button
                      onClick={() => setSelectedDomain(d)}
                      className="flex w-full items-center justify-between rounded-field bg-base-100 px-4 py-3 text-left hover:bg-base-200"
                    >
                      <span>{d.domain_name}</span>
                      <span className={`badge badge-sm ${d.is_verified ? 'badge-success' : 'badge-warning'}`}>
                        {d.is_verified ? 'verified' : 'pending'}
                      </span>
                    </button>
                  </li>
                ))}
                {domains.length === 0 && <p className="text-sm text-base-content/50">No domains yet.</p>}
              </ul>

              <details className="rounded-box bg-base-100 p-5" open={showKeys} onToggle={(e) => setShowKeys(e.currentTarget.open)}>
                <summary className="cursor-pointer text-sm font-medium">API keys (advanced)</summary>
                <p className="mt-2 mb-3 text-xs text-base-content/50">Needed to add domains and mailboxes above.</p>
                <ul className="flex flex-col gap-1 mb-3">
                  {apiKeys.map((k) => (
                    <li key={k.id} className="text-sm text-base-content/70">
                      {k.name} — created {new Date(k.created_at).toLocaleDateString()}
                    </li>
                  ))}
                </ul>
                {apiKey ? (
                  <code className="block rounded-field bg-base-200 px-3 py-2 text-xs break-all">{apiKey}</code>
                ) : (
                  <button onClick={handleCreateApiKey} className="btn btn-sm btn-outline">
                    Generate API key
                  </button>
                )}
              </details>
            </>
          ) : (
            <div>
              <button onClick={() => setSelectedDomain(null)} className="btn btn-ghost btn-sm mb-4">
                ← Domains
              </button>
              <h2 className="text-lg font-medium mb-4">{selectedDomain.domain_name}</h2>

              {selectedDomain.dns_records && selectedDomain.dns_records.length > 0 && (
                <section className="mb-6">
                  <h3 className="text-sm font-medium text-base-content/60 mb-2">DNS records</h3>
                  <div className="overflow-x-auto rounded-box bg-base-100">
                    <table className="table table-sm">
                      <thead>
                        <tr>
                          <th>Type</th>
                          <th>Host</th>
                          <th>Value</th>
                        </tr>
                      </thead>
                      <tbody>
                        {selectedDomain.dns_records.map((r, i) => (
                          <tr key={i}>
                            <td>{r.type}</td>
                            <td className="font-mono text-xs">{r.host}</td>
                            <td className="font-mono text-xs break-all">{r.value}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              )}

              <section className="rounded-box bg-base-100 p-5 mb-4">
                <h3 className="text-sm font-medium mb-3">Add a mailbox</h3>
                <form onSubmit={handleCreateMailbox} className="flex items-center gap-2">
                  <input
                    className="input input-bordered input-sm flex-1"
                    placeholder="sales"
                    value={newLocalPart}
                    onChange={(e) => setNewLocalPart(e.target.value)}
                    required
                  />
                  <span className="text-sm text-base-content/50">@{selectedDomain.domain_name}</span>
                  <button className="btn btn-primary btn-sm">Add</button>
                </form>
                {justCreatedPassword && (
                  <p className="mt-3 text-xs text-base-content/70">
                    App password (shown once): <code className="bg-base-200 px-2 py-0.5 rounded">{justCreatedPassword}</code>
                  </p>
                )}
              </section>

              <ul className="flex flex-col gap-2">
                {mailboxes.map((m) => (
                  <li key={m.id} className="rounded-field bg-base-100 px-4 py-2 text-sm">
                    {m.email_address}
                  </li>
                ))}
                {mailboxes.length === 0 && <p className="text-sm text-base-content/50">No mailboxes yet.</p>}
              </ul>
            </div>
          )}
        </>
      )}
    </div>
  )
}

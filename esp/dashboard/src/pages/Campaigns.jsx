import { useState } from 'react';

export default function Campaigns() {
  // In a real app, we'd fetch this from the API
  const [campaigns] = useState([
    { id: '1', name: 'Black Friday Sale', status: 'completed', list_id: 'All Customers' },
    { id: '2', name: 'Welcome Series Test', status: 'sending', list_id: 'New Signups' },
  ]);

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '2rem' }}>
        <div>
          <h1 style={{ margin: 0 }}>Campaigns</h1>
          <p style={{ color: '#64748b', margin: '0.5rem 0 0 0' }}>Launch and monitor massive A/B tested email blasts.</p>
        </div>
        <button style={{ background: '#2563eb', color: '#fff', padding: '0.75rem 1.5rem', border: 'none', borderRadius: '6px', cursor: 'pointer', fontWeight: 'bold' }}>
          Launch Campaign
        </button>
      </div>

      <div style={{ background: '#fff', borderRadius: '8px', boxShadow: '0 1px 3px rgba(0,0,0,0.1)', overflow: 'hidden' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
          <thead style={{ background: '#f8fafc', borderBottom: '1px solid #e2e8f0' }}>
            <tr>
              <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>Name</th>
              <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>Target List</th>
              <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>Status</th>
            </tr>
          </thead>
          <tbody>
            {campaigns.map(c => (
              <tr key={c.id} style={{ borderBottom: '1px solid #f1f5f9' }}>
                <td style={{ padding: '1rem', color: '#0f172a', fontWeight: '500' }}>{c.name}</td>
                <td style={{ padding: '1rem', color: '#64748b' }}>{c.list_id}</td>
                <td style={{ padding: '1rem' }}>
                  <span style={{ 
                    padding: '0.25rem 0.5rem', 
                    borderRadius: '9999px', 
                    fontSize: '0.85rem',
                    background: c.status === 'completed' ? '#dcfce7' : '#dbeafe',
                    color: c.status === 'completed' ? '#166534' : '#1e40af',
                    fontWeight: '500'
                  }}>
                    {c.status.toUpperCase()}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

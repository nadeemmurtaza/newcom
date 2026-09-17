import { useEffect, useState } from 'react';

export default function Templates() {
  const [templates, setTemplates] = useState([]);
  
  const orgId = "11111111-1111-1111-1111-111111111111";
  const userId = "22222222-2222-2222-2222-222222222222";

  useEffect(() => {
    fetch(`http://localhost:8080/api/templates?org_id=${orgId}`, {
      headers: { 'X-User-ID': userId }
    })
      .then(res => res.json())
      .then(data => setTemplates(data || []))
      .catch(err => console.error(err));
  }, []);

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '2rem' }}>
        <div>
          <h1 style={{ margin: 0 }}>Templates</h1>
          <p style={{ color: '#64748b', margin: '0.5rem 0 0 0' }}>Manage your HTML email templates.</p>
        </div>
        <button style={{ background: '#2563eb', color: '#fff', padding: '0.75rem 1.5rem', border: 'none', borderRadius: '6px', cursor: 'pointer', fontWeight: 'bold' }}>
          + New Template
        </button>
      </div>

      <div style={{ background: '#fff', borderRadius: '8px', boxShadow: '0 1px 3px rgba(0,0,0,0.1)', overflow: 'hidden' }}>
        {templates.length === 0 ? (
          <div style={{ padding: '3rem', textAlign: 'center', color: '#94a3b8' }}>
            No templates found. Create one to get started!
          </div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
            <thead style={{ background: '#f8fafc', borderBottom: '1px solid #e2e8f0' }}>
              <tr>
                <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>Name</th>
                <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>Subject</th>
                <th style={{ padding: '1rem', color: '#475569', fontWeight: '500' }}>ID</th>
              </tr>
            </thead>
            <tbody>
              {templates.map(t => (
                <tr key={t.id} style={{ borderBottom: '1px solid #f1f5f9' }}>
                  <td style={{ padding: '1rem', color: '#0f172a' }}>{t.name}</td>
                  <td style={{ padding: '1rem', color: '#64748b' }}>{t.subject_template}</td>
                  <td style={{ padding: '1rem', color: '#94a3b8', fontFamily: 'monospace', fontSize: '0.85rem' }}>{t.id}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

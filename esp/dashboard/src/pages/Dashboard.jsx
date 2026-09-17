import { useEffect, useState } from 'react';

export default function Dashboard() {
  const [stats, setStats] = useState({ total_sent: 0, total_bounces: 0 });

  useEffect(() => {
    // Mock user & org ID
    fetch('http://localhost:8080/api/stats?org_id=11111111-1111-1111-1111-111111111111', {
      headers: { 'X-User-ID': '22222222-2222-2222-2222-222222222222' }
    })
      .then(res => res.json())
      .then(data => setStats(data))
      .catch(err => console.error(err));
  }, []);

  return (
    <div style={{ padding: '2rem' }}>
      <h1>Dashboard Overview</h1>
      <p style={{ color: '#64748b', marginBottom: '2rem' }}>High-level analytics for your organization.</p>
      
      <div style={{ display: 'flex', gap: '2rem' }}>
        <div style={{ background: '#fff', padding: '2rem', borderRadius: '8px', boxShadow: '0 1px 3px rgba(0,0,0,0.1)', flex: 1 }}>
          <h3 style={{ margin: '0 0 1rem 0', color: '#64748b' }}>Total Sent</h3>
          <div style={{ fontSize: '3rem', fontWeight: 'bold', color: '#0f172a' }}>{stats.total_sent}</div>
        </div>
        
        <div style={{ background: '#fff', padding: '2rem', borderRadius: '8px', boxShadow: '0 1px 3px rgba(0,0,0,0.1)', flex: 1 }}>
          <h3 style={{ margin: '0 0 1rem 0', color: '#64748b' }}>Bounces</h3>
          <div style={{ fontSize: '3rem', fontWeight: 'bold', color: '#ef4444' }}>{stats.total_bounces}</div>
        </div>
      </div>
    </div>
  );
}

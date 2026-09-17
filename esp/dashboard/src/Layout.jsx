import { Outlet, Link, useLocation } from 'react-router-dom';

export default function Layout() {
  const location = useLocation();

  const navItems = [
    { path: '/', label: 'Dashboard' },
    { path: '/mailbox', label: 'Webmail' },
    { path: '/templates', label: 'Templates' },
    { path: '/campaigns', label: 'Campaigns' },
  ];

  return (
    <div style={{ display: 'flex', height: '100vh', fontFamily: 'sans-serif', margin: 0, padding: 0 }}>
      {/* Main Sidebar */}
      <div style={{ width: '220px', background: '#1e293b', color: '#fff', display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '2rem 1rem', fontSize: '1.5rem', fontWeight: 'bold', borderBottom: '1px solid #334155' }}>
          ESP Platform
        </div>
        <nav style={{ flex: 1, padding: '1rem 0' }}>
          {navItems.map(item => (
            <Link
              key={item.path}
              to={item.path}
              style={{
                display: 'block',
                padding: '1rem 1.5rem',
                color: location.pathname === item.path ? '#fff' : '#94a3b8',
                textDecoration: 'none',
                background: location.pathname === item.path ? '#334155' : 'transparent',
                fontWeight: location.pathname === item.path ? 'bold' : 'normal',
              }}
            >
              {item.label}
            </Link>
          ))}
        </nav>
      </div>

      {/* Page Content */}
      <div style={{ flex: 1, background: '#f8fafc', overflowY: 'auto' }}>
        <Outlet />
      </div>
    </div>
  );
}

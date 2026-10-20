import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { ROUTES } from '../../constants/routes'
import { brand } from '../../config/brand'
import { useAuth } from '../../providers/auth-provider'

const navItems = [
  { to: ROUTES.inbox, label: 'Mail', icon: '✉️' },
  { to: ROUTES.domains, label: 'Domains', icon: '🌐' },
  { to: ROUTES.settings, label: 'Settings', icon: '⚙️' },
]

export function AppShell({ children }: { children: ReactNode }) {
  const { logout } = useAuth()

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-base-200">
      {/* sm/xs: nav collapses to a bottom bar; fold+: real sidebar */}
      <aside className="hidden fold:flex fold:w-16 lg:w-56 flex-col border-r border-base-300 bg-base-100">
        <div className="flex items-center gap-2 px-4 py-4 font-semibold text-primary">
          <span className="text-xl">{brand.artwork.logoMark ? '📬' : ''}</span>
          <span className="hidden lg:inline">{brand.identity.name}</span>
        </div>
        <nav className="flex-1 px-2">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 rounded-field px-3 py-2 my-1 text-sm ${
                  isActive ? 'bg-primary text-primary-content' : 'hover:bg-base-200'
                }`
              }
            >
              <span>{item.icon}</span>
              <span className="hidden lg:inline">{item.label}</span>
            </NavLink>
          ))}
        </nav>
        <button onClick={logout} className="btn btn-ghost btn-sm m-2 justify-start">
          <span className="hidden lg:inline">Log out</span>
          <span className="lg:hidden">↩</span>
        </button>
      </aside>

      <main className="flex-1 flex flex-col overflow-hidden pb-14 fold:pb-0">{children}</main>

      {/* xs/sm: bottom tab bar */}
      <nav className="fold:hidden fixed bottom-0 left-0 right-0 flex border-t border-base-300 bg-base-100">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) =>
              `flex-1 flex flex-col items-center gap-0.5 py-2 text-xs ${isActive ? 'text-primary' : 'text-base-content/60'}`
            }
          >
            <span className="text-lg">{item.icon}</span>
            {item.label}
          </NavLink>
        ))}
      </nav>
    </div>
  )
}

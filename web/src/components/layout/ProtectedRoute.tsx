import type { ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { ROUTES } from '../../constants/routes'
import { useAuth } from '../../providers/auth-provider'
import { AppShell } from './AppShell'

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth()
  if (!isAuthenticated) return <Navigate to={ROUTES.login} replace />
  return <AppShell>{children}</AppShell>
}

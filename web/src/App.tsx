import { Navigate, Route, BrowserRouter, Routes } from 'react-router-dom'
import { ProtectedRoute } from './components/layout/ProtectedRoute'
import { ROUTES } from './constants/routes'
import { DomainsPage } from './pages/DomainsPage'
import { LoginPage } from './pages/LoginPage'
import { MailPage } from './pages/MailPage'
import { NidCallbackPage } from './pages/NidCallbackPage'
import { SettingsPage } from './pages/SettingsPage'
import { AuthProvider } from './providers/auth-provider'

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path={ROUTES.login} element={<LoginPage />} />
          <Route path={ROUTES.nidCallback} element={<NidCallbackPage />} />
          <Route
            path={ROUTES.inbox}
            element={
              <ProtectedRoute>
                <MailPage />
              </ProtectedRoute>
            }
          />
          <Route
            path={ROUTES.domains}
            element={
              <ProtectedRoute>
                <DomainsPage />
              </ProtectedRoute>
            }
          />
          <Route
            path={ROUTES.settings}
            element={
              <ProtectedRoute>
                <SettingsPage />
              </ProtectedRoute>
            }
          />
          <Route path="*" element={<Navigate to={ROUTES.inbox} replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}

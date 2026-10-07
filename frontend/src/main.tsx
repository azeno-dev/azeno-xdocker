import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import './styles.css';
import { AuthGate } from './components/auth';
import { JobsProvider } from './components/JobToast';
import { Layout } from './components/Layout';
import { ToastProvider } from './components/ui';
import { AuditPage } from './pages/Audit';
import { ContainersPage } from './pages/Containers';
import { DashboardPage } from './pages/Dashboard';
import { ImagesPage } from './pages/Images';
import { JobPage, JobsPage } from './pages/Jobs';
import { PoliciesPage } from './pages/Policies';
import { PolicyEditorPage } from './pages/PolicyEditor';
import { PortsPage } from './pages/Ports';
import { RegistryPage } from './pages/Registry';
import { SettingsPage } from './pages/Settings';
import { StackEditorPage } from './pages/StackEditor';
import { StacksPage } from './pages/Stacks';
import { UsersPage } from './pages/Users';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ToastProvider>
        <AuthGate>
          <JobsProvider>
            <Routes>
              <Route element={<Layout />}>
                <Route index element={<DashboardPage />} />
                <Route path="containers" element={<ContainersPage />} />
                <Route path="images" element={<ImagesPage />} />
                <Route path="stacks" element={<StacksPage />} />
                <Route path="stacks/new" element={<StackEditorPage />} />
                <Route path="stacks/:name" element={<StackEditorPage />} />
                <Route path="ports" element={<PortsPage />} />
                <Route path="registry" element={<RegistryPage />} />
                <Route path="policies" element={<PoliciesPage />} />
                <Route path="policies/new" element={<PolicyEditorPage />} />
                <Route path="policies/:id" element={<PolicyEditorPage />} />
                <Route path="jobs" element={<JobsPage />} />
                <Route path="jobs/:id" element={<JobPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="users" element={<UsersPage />} />
                <Route path="audit" element={<AuditPage />} />
                <Route path="*" element={<p>Page not found.</p>} />
              </Route>
            </Routes>
          </JobsProvider>
        </AuthGate>
      </ToastProvider>
    </BrowserRouter>
  </React.StrictMode>,
);

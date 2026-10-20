import { HashRouter, Navigate, Route, Routes, useParams } from 'react-router';
import { AppShell } from '@/components/app-shell';
import { TooltipProvider } from '@/components/ui/tooltip';
import { ErrorBoundary } from '@/components/error-boundary';
import { useAsync } from '@/api/hooks';
import { ErrorState, LoadingState } from '@/components/page-state';
import { OverviewPage } from '@/pages/overview';
import { SessionsPage } from '@/pages/sessions';
import { SessionDetailPage } from '@/pages/session-detail';
import { FailuresPage } from '@/pages/failures';
import { ToolsPage } from '@/pages/tools';
import { MemoryPage } from '@/pages/memory';
import { EvidencePage } from '@/pages/evidence';
import { SettingsPage } from '@/pages/settings';
import { ConsentPage } from '@/pages/consent';
import { NotFoundPage } from '@/pages/not-found';

const CONSENT_SEEN_KEY = 'leankg-dashboard-consent-seen';

/** Read a per-tab flag. Storage can be blocked, so every access is guarded. */
function consentSeen(): boolean {
  try {
    return window.sessionStorage.getItem(CONSENT_SEEN_KEY) === '1';
  } catch {
    return false;
  }
}

function markConsentSeen(): void {
  try {
    window.sessionStorage.setItem(CONSENT_SEEN_KEY, '1');
  } catch {
    /* storage blocked: the consent screen simply reopens next visit */
  }
}

/**
 * The root route opens on consent once per tab when capture is off (DS-24).
 * After that, the Overview shows the capture banner instead, so a user who
 * skipped consent can still reach the figures and the Settings link.
 */
function IndexRoute() {
  const settings = useAsync((api, signal) => api.consent(signal), []);
  if (settings.error) return <ErrorState error={settings.error} onRetry={settings.reload} />;
  if (!settings.data) return <LoadingState label="Checking capture status" />;
  if (settings.data.capture === 'off' && !consentSeen()) {
    markConsentSeen();
    return <Navigate to="/consent" replace />;
  }
  return <OverviewPage />;
}

function SessionDetailRoute() {
  const { id = '' } = useParams();
  return <SessionDetailPage id={id} />;
}

export default function App() {
  return (
    <TooltipProvider delayDuration={150}>
      <HashRouter>
        <AppShell>
          <ErrorBoundary label="This page">
          <Routes>
            <Route index element={<IndexRoute />} />
            <Route path="consent" element={<ConsentPage />} />
            <Route path="sessions" element={<SessionsPage />} />
            <Route path="sessions/:id" element={<SessionDetailRoute />} />
            <Route path="failures" element={<FailuresPage />} />
            <Route path="tools" element={<ToolsPage />} />
            <Route path="memory" element={<MemoryPage />} />
            <Route path="evidence" element={<EvidencePage />} />
            <Route path="settings" element={<SettingsPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
          </ErrorBoundary>
        </AppShell>
      </HashRouter>
    </TooltipProvider>
  );
}


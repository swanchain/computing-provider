import { useState, useCallback, useEffect } from 'react';
import {
  LayoutDashboard,
  LockKeyhole,
  LogOut,
  ReceiptText,
  RefreshCw,
  Server,
  Settings2,
} from 'lucide-react';
import { usePolling } from './hooks/usePolling';
import { api } from './api/client';
import { EarningsChart } from './components/EarningsChart';
import { ModelDistribution } from './components/ModelDistribution';
import { MetricsPanel } from './components/MetricsPanel';
import { GPUPanel } from './components/GPUPanel';
import { ModelsPanel } from './components/ModelsPanel';
import { RequestManagementPanel } from './components/RequestManagementPanel';
import { ConnectionStatus } from './components/ConnectionStatus';
import { RequestHistoryPanel } from './components/RequestHistoryPanel';
import { HistoricalChart } from './components/HistoricalChart';
import { ModelDetailPanel } from './components/ModelDetailPanel';
import { AuthDialog } from './components/AuthDialog';
import { SettingsPanel } from './components/SettingsPanel';
import { OperationalSummary } from './components/OperationalSummary';

const POLL_INTERVAL = 5000;
type DashboardView = 'overview' | 'transactions' | 'settings';

const views: Array<{ id: DashboardView; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'transactions', label: 'Requests', icon: ReceiptText },
  { id: 'settings', label: 'Settings', icon: Settings2 },
];

function viewFromHash(): DashboardView {
  const candidate = window.location.hash.replace('#', '');
  return views.some((view) => view.id === candidate) ? candidate as DashboardView : 'overview';
}

function App() {
  const [selectedModelId, setSelectedModelId] = useState<string | null>(null);
  const [activeView, setActiveView] = useState<DashboardView>(viewFromHash);
  const [authenticated, setAuthenticated] = useState(false);
  const [showAuthDialog, setShowAuthDialog] = useState(false);
  const [settingsDirty, setSettingsDirty] = useState(false);

  const {
    data: metrics,
    error: metricsError,
    loading: metricsLoading,
    refreshing: metricsRefreshing,
    refetch: refetchMetrics,
  } = usePolling(useCallback(() => api.getMetrics(), []), POLL_INTERVAL);

  const {
    data: status,
    error: statusError,
    loading: statusLoading,
    refreshing: statusRefreshing,
    lastUpdated: statusLastUpdated,
    refetch: refetchStatus,
  } = usePolling(useCallback(() => api.getStatus(), []), POLL_INTERVAL);

  const {
    data: earnings,
    error: earningsError,
    loading: earningsLoading,
    refreshing: earningsRefreshing,
    refetch: refetchEarnings,
  } = usePolling(useCallback(() => api.getEarnings(), []), POLL_INTERVAL);

  const {
    data: models,
    error: modelsError,
    loading: modelsLoading,
    refreshing: modelsRefreshing,
    refetch: refetchModels,
  } = usePolling(useCallback(() => api.getModels(), []), POLL_INTERVAL);

  const {
    data: requestMgmt,
    error: requestMgmtError,
    loading: requestMgmtLoading,
    refreshing: requestMgmtRefreshing,
    refetch: refetchRequestMgmt,
  } = usePolling(useCallback(() => api.getRequestManagement(), []), POLL_INTERVAL);

  useEffect(() => {
    if (!api.hasAccessToken()) return;
    api.getSettings()
      .then(() => setAuthenticated(true))
      .catch(() => {
        api.clearAccessToken();
        setAuthenticated(false);
      });
  }, []);

  useEffect(() => {
    const handleHashChange = () => {
      setActiveView(viewFromHash());
      window.scrollTo({ top: 0 });
    };
    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  const handleRefreshAll = () => {
    refetchMetrics();
    refetchStatus();
    refetchModels();
    refetchRequestMgmt();
    refetchEarnings();
  };

  const handleLock = () => {
    api.clearAccessToken();
    setAuthenticated(false);
  };

  const closeAuthDialog = useCallback(() => setShowAuthDialog(false), []);
  const closeModelDetail = useCallback(() => setSelectedModelId(null), []);
  const handleAuthenticated = useCallback(() => {
    setAuthenticated(true);
    setShowAuthDialog(false);
  }, []);

  const handleViewChange = (view: DashboardView) => {
    if (activeView === 'settings' && view !== 'settings' && settingsDirty) {
      const leave = window.confirm('Leave settings and discard unsaved changes?');
      if (!leave) return;
    }
    setActiveView(view);
    window.history.replaceState(null, '', `#${view}`);
    window.scrollTo({ top: 0 });
    if (view === 'settings' && !authenticated) setShowAuthDialog(true);
  };

  const refreshing = metricsRefreshing || statusRefreshing || earningsRefreshing || modelsRefreshing || requestMgmtRefreshing;

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 bg-slate-900/95">
        <div className="mx-auto flex max-w-7xl flex-col items-stretch gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <div className="rounded-xl bg-blue-500/10 p-2 text-blue-400">
              <Server aria-hidden="true" size={24} />
            </div>
            <div className="min-w-0">
              <h1 className="truncate text-lg font-semibold tracking-tight text-white sm:text-xl">Provider Console</h1>
              <p className="text-xs text-slate-400">
                Inference operations
                {status?.version && (
                  <>
                    {' · '}
                    {/* The full build string carries the network tag and commit,
                        which is what to quote in a bug report. */}
                    <span title={status.build ?? undefined} className="font-mono text-slate-400">
                      v{status.version}
                    </span>
                  </>
                )}
              </p>
            </div>
          </div>

          <div className="flex min-w-0 items-center justify-between gap-2 sm:justify-end sm:gap-3">
            <ConnectionStatus status={status} loading={statusLoading} error={statusError} lastUpdated={statusLastUpdated} />
            <button
              type="button"
              onClick={handleRefreshAll}
              disabled={refreshing}
              className="inline-flex min-h-10 min-w-10 items-center justify-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm text-slate-200 transition hover:border-slate-600 hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
              aria-label="Refresh dashboard data"
            >
              <RefreshCw aria-hidden="true" size={16} className={refreshing ? 'animate-spin' : ''} />
              <span className="hidden sm:inline">{refreshing ? 'Refreshing…' : 'Refresh'}</span>
            </button>
            {authenticated ? (
              <button
                type="button"
                onClick={handleLock}
                className="inline-flex min-h-10 items-center gap-2 rounded-lg border border-slate-700 px-3 text-sm text-slate-300 transition hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <LogOut aria-hidden="true" size={16} />
                <span className="hidden sm:inline">Lock</span>
              </button>
            ) : (
              <button
                type="button"
                onClick={() => setShowAuthDialog(true)}
                className="inline-flex min-h-10 items-center gap-2 rounded-lg bg-blue-600 px-3 text-sm font-medium text-white transition hover:bg-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-400"
              >
                <LockKeyhole aria-hidden="true" size={16} />
                <span className="hidden sm:inline">Unlock controls</span>
                <span className="sm:hidden">Unlock</span>
              </button>
            )}
          </div>
        </div>

        <nav aria-label="Dashboard sections" className="mx-auto max-w-7xl px-4 sm:px-6">
          <div className="flex gap-1 overflow-hidden">
            {views.map((view) => {
              const Icon = view.icon;
              const selected = activeView === view.id;
              return (
                <button
                  key={view.id}
                  type="button"
                  onClick={() => handleViewChange(view.id)}
                  aria-current={selected ? 'page' : undefined}
                  className={`inline-flex min-h-11 min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap border-b-2 px-2 text-xs font-medium transition focus:outline-none focus:ring-2 focus:ring-inset focus:ring-blue-500 sm:flex-none sm:gap-2 sm:px-5 sm:text-sm ${
                    selected
                      ? 'border-blue-500 text-white'
                      : 'border-transparent text-slate-400 hover:border-slate-700 hover:text-slate-200'
                  }`}
                >
                  <Icon aria-hidden="true" size={16} />
                  {view.label}
                </button>
              );
            })}
          </div>
        </nav>
      </header>

      <main className="mx-auto max-w-7xl px-4 py-5 sm:px-6 sm:py-6">
        {activeView === 'overview' && (
          <div className="space-y-6">
            <section aria-labelledby="provider-health-heading">
              <div className="mb-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                <div className="min-w-0">
                  <h2 id="provider-health-heading" className="text-xl font-semibold text-white">Provider overview</h2>
                  <p className="mt-1 text-sm text-slate-300">Current service health, traffic, and earnings.</p>
                </div>
                <OperationalSummary
                  status={status}
                  metrics={metrics}
                  models={models}
                  loading={statusLoading || metricsLoading || modelsLoading}
                  dataIssues={[
                    { label: 'connection', error: statusError },
                    { label: 'metrics', error: metricsError },
                    { label: 'models', error: modelsError },
                    { label: 'request controls', error: requestMgmtError },
                    { label: 'earnings', error: earningsError },
                  ]}
                />
              </div>
              <MetricsPanel
                metrics={metrics}
                loading={metricsLoading}
                error={metricsError}
                earnings={earnings}
                earningsError={earningsError}
                earningsLoading={earningsLoading}
              />
            </section>

            <section aria-labelledby="operations-heading">
              <div className="mb-3">
                <h2 id="operations-heading" className="text-xl font-semibold text-white">Operations</h2>
                <p className="mt-1 text-sm text-slate-400">Model readiness and local resource pressure.</p>
              </div>
              <div className="grid items-start gap-6 lg:grid-cols-2">
                <div className="min-w-0 space-y-6">
                  <ModelsPanel
                    models={models?.models ?? []}
                    healthLog={models?.health_log}
                    prices={models?.prices ?? {}}
                    summary={models?.summary}
                    loading={modelsLoading}
                    error={modelsError}
                    onRefresh={refetchModels}
                    onModelClick={setSelectedModelId}
                    authenticated={authenticated}
                    onUnlock={() => setShowAuthDialog(true)}
                  />
                  <RequestManagementPanel
                    data={requestMgmt}
                    loading={requestMgmtLoading}
                    error={requestMgmtError}
                    onOpenSettings={() => handleViewChange('settings')}
                  />
                </div>
                <div className="min-w-0 space-y-6">
                  <section aria-labelledby="earnings-heading">
                    <div className="mb-3">
                      <h2 id="earnings-heading" className="text-xl font-semibold text-white">Earnings and traffic mix</h2>
                      <p className="mt-1 text-sm text-slate-300">Estimated local history and the models contributing to it.</p>
                    </div>
                    <div className="min-w-0 space-y-4">
                      <EarningsChart models={earnings?.models} />
                      <ModelDistribution earnings={earnings} loading={earningsLoading && !earnings} error={earningsError} />
                    </div>
                  </section>
                  <GPUPanel gpus={metrics?.gpu_metrics ?? []} loading={metricsLoading} error={metricsError} />
                </div>
              </div>
            </section>

            <section aria-labelledby="performance-heading">
              <div className="mb-3">
                <h2 id="performance-heading" className="text-xl font-semibold text-white">Performance</h2>
                <p className="mt-1 text-sm text-slate-300">Persistent trends that remain available after navigation or restart.</p>
              </div>
              <HistoricalChart />
            </section>

          </div>
        )}

        {activeView === 'transactions' && <RequestHistoryPanel models={models?.models ?? []} />}

        {activeView === 'settings' && (
          <SettingsPanel
            authenticated={authenticated}
            onUnlock={() => setShowAuthDialog(true)}
            onModelsSaved={refetchModels}
            onDirtyChange={setSettingsDirty}
          />
        )}
      </main>

      <footer className="mt-8 border-t border-slate-800 bg-slate-900 px-4 py-4 sm:px-6">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-2 text-xs text-slate-400">
          <span>Swan Chain Computing Provider</span>
          <span>Core monitoring refreshes every {POLL_INTERVAL / 1000}s</span>
        </div>
      </footer>

      {selectedModelId && <ModelDetailPanel modelId={selectedModelId} onClose={closeModelDetail} />}

      <AuthDialog
        open={showAuthDialog}
        onClose={closeAuthDialog}
        onAuthenticated={handleAuthenticated}
      />
    </div>
  );
}

export default App;

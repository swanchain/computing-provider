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
import { MetricsPanel } from './components/MetricsPanel';
import { GPUPanel } from './components/GPUPanel';
import { ModelsPanel } from './components/ModelsPanel';
import { RequestManagementPanel } from './components/RequestManagementPanel';
import { ConnectionStatus } from './components/ConnectionStatus';
import { LatencyChart } from './components/LatencyChart';
import { ThroughputChart } from './components/ThroughputChart';
import { RequestHistoryPanel } from './components/RequestHistoryPanel';
import { HistoricalChart } from './components/HistoricalChart';
import { ModelDetailPanel } from './components/ModelDetailPanel';
import { AuthDialog } from './components/AuthDialog';
import { SettingsPanel } from './components/SettingsPanel';

const POLL_INTERVAL = 5000;
type DashboardView = 'overview' | 'transactions' | 'settings';

const views: Array<{ id: DashboardView; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'transactions', label: 'Transactions', icon: ReceiptText },
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

  const {
    data: metrics,
    error: metricsError,
    loading: metricsLoading,
    refetch: refetchMetrics,
  } = usePolling(useCallback(() => api.getMetrics(), []), POLL_INTERVAL);

  const {
    data: status,
    error: statusError,
    loading: statusLoading,
    refetch: refetchStatus,
  } = usePolling(useCallback(() => api.getStatus(), []), POLL_INTERVAL);

  const {
    data: models,
    error: modelsError,
    loading: modelsLoading,
    refetch: refetchModels,
  } = usePolling(useCallback(() => api.getModels(), []), POLL_INTERVAL);

  const {
    data: requestMgmt,
    error: requestMgmtError,
    loading: requestMgmtLoading,
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
  };

  const handleLock = () => {
    api.clearAccessToken();
    setAuthenticated(false);
  };

  const handleViewChange = (view: DashboardView) => {
    setActiveView(view);
    window.history.replaceState(null, '', `#${view}`);
    window.scrollTo({ top: 0 });
    if (view === 'settings' && !authenticated) setShowAuthDialog(true);
  };

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
              <p className="text-xs text-slate-400">Inference operations</p>
            </div>
          </div>

          <div className="flex min-w-0 items-center justify-between gap-2 sm:justify-end sm:gap-3">
            <ConnectionStatus status={status} loading={statusLoading} error={statusError} />
            <button
              type="button"
              onClick={handleRefreshAll}
              className="inline-flex min-h-10 min-w-10 items-center justify-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm text-slate-200 transition hover:border-slate-600 hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
              aria-label="Refresh dashboard data"
            >
              <RefreshCw aria-hidden="true" size={16} />
              <span className="hidden sm:inline">Refresh</span>
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
              <div className="mb-3">
                <h2 id="provider-health-heading" className="text-xl font-semibold text-white">Provider health</h2>
                <p className="mt-1 text-sm text-slate-400">Live service, request, and capacity signals.</p>
              </div>
              <MetricsPanel metrics={metrics} loading={metricsLoading} error={metricsError} />
            </section>

            <section aria-labelledby="operations-heading">
              <div className="mb-3">
                <h2 id="operations-heading" className="text-xl font-semibold text-white">Operations</h2>
                <p className="mt-1 text-sm text-slate-400">Model readiness and local resource pressure.</p>
              </div>
              <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1.25fr)_minmax(340px,0.75fr)]">
                <ModelsPanel
                  models={models?.models ?? []}
                  prices={models?.prices ?? {}}
                  loading={modelsLoading}
                  error={modelsError}
                  onRefresh={refetchModels}
                  onModelClick={setSelectedModelId}
                  authenticated={authenticated}
                  onUnlock={() => setShowAuthDialog(true)}
                />
                <div className="space-y-6">
                  <GPUPanel gpus={metrics?.gpu_metrics ?? []} loading={metricsLoading} error={metricsError} />
                  <RequestManagementPanel
                    data={requestMgmt}
                    loading={requestMgmtLoading}
                    error={requestMgmtError}
                    onOpenSettings={() => handleViewChange('settings')}
                  />
                </div>
              </div>
            </section>

            <section aria-labelledby="live-performance-heading">
              <div className="mb-3">
                <h2 id="live-performance-heading" className="text-xl font-semibold text-white">Live performance</h2>
                <p className="mt-1 text-sm text-slate-400">The last 30 dashboard samples; keep this page open to build the series.</p>
              </div>
              <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                <LatencyChart metrics={metrics} />
                <ThroughputChart metrics={metrics} />
              </div>
            </section>

            <HistoricalChart />
          </div>
        )}

        {activeView === 'transactions' && <RequestHistoryPanel models={models?.models ?? []} />}

        {activeView === 'settings' && (
          <SettingsPanel
            authenticated={authenticated}
            onUnlock={() => setShowAuthDialog(true)}
            onModelsSaved={refetchModels}
          />
        )}
      </main>

      <footer className="mt-8 border-t border-slate-800 bg-slate-900 px-4 py-4 sm:px-6">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-2 text-xs text-slate-500">
          <span>Swan Chain Computing Provider</span>
          <span>Monitoring refreshes every {POLL_INTERVAL / 1000}s</span>
        </div>
      </footer>

      {selectedModelId && <ModelDetailPanel modelId={selectedModelId} onClose={() => setSelectedModelId(null)} />}

      <AuthDialog
        open={showAuthDialog}
        onClose={() => setShowAuthDialog(false)}
        onAuthenticated={() => {
          setAuthenticated(true);
          setShowAuthDialog(false);
        }}
      />
    </div>
  );
}

export default App;

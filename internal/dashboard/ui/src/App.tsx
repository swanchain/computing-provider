import { useState, useCallback, useEffect } from 'react';
import {
  KeyRound,
  LayoutDashboard,
  LockKeyhole,
  LogOut,
  ReceiptText,
  RefreshCw,
  Server,
  Settings2,
} from 'lucide-react';
import { refreshDashboard, usePolling } from './hooks/usePolling';
import { api } from './api/client';
import { EarningsChart } from './components/EarningsChart';
import { UsageChart } from './components/UsageChart';
import type { ModelColourMap } from './lib/modelPalette';
import { ModelDistribution } from './components/ModelDistribution';
import { MetricsPanel } from './components/MetricsPanel';
import { GPUPanel } from './components/GPUPanel';
import { ModelsPanel } from './components/ModelsPanel';
import { HubModelsPanel } from './components/HubModelsPanel';
import { ProxyAccountsPanel, ProxyRoutingPanel } from './components/ProxyAccountsPanel';
import { OverviewAttention } from './components/OverviewAttention';
import { RequestManagementPanel } from './components/RequestManagementPanel';
import { ConnectionStatus } from './components/ConnectionStatus';
import { RequestHistoryPanel } from './components/RequestHistoryPanel';
import { HistoricalChart } from './components/HistoricalChart';
import { ModelDetailPanel } from './components/ModelDetailPanel';
import { AuthDialog } from './components/AuthDialog';
import { SettingsPanel } from './components/SettingsPanel';
import { OperationalSummary } from './components/OperationalSummary';

const POLL_INTERVAL = 5000;
type DashboardView = 'overview' | 'transactions' | 'proxies' | 'settings';

const views: Array<{ id: DashboardView; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'transactions', label: 'Requests', icon: ReceiptText },
  { id: 'proxies', label: 'Proxies', icon: KeyRound },
  { id: 'settings', label: 'Settings', icon: Settings2 },
];

function viewFromHash(): DashboardView {
  const candidate = window.location.hash.replace('#', '');
  return views.some((view) => view.id === candidate) ? candidate as DashboardView : 'overview';
}

function App() {
  const [selectedModelId, setSelectedModelId] = useState<string | null>(null);
  // One model-colour map for the usage and earnings charts, owned by the
  // earnings chart, so a model is the same colour in both.
  const [modelColours, setModelColours] = useState<ModelColourMap | null>(null);
  const [activeView, setActiveView] = useState<DashboardView>(viewFromHash);
  const [authenticated, setAuthenticated] = useState(false);
  const [showAuthDialog, setShowAuthDialog] = useState(false);
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [chartWindow, setChartWindow] = useState('24h');
  const [showMix, setShowMix] = useState(false);
  const [showPerformance, setShowPerformance] = useState(false);

  const {
    data: metrics,
    error: metricsError,
    loading: metricsLoading,
    refreshing: metricsRefreshing,
  } = usePolling(useCallback(() => api.getMetrics(), []), POLL_INTERVAL);

  const {
    data: status,
    error: statusError,
    loading: statusLoading,
    refreshing: statusRefreshing,
    lastUpdated: statusLastUpdated,
  } = usePolling(useCallback(() => api.getStatus(), []), POLL_INTERVAL);

  const {
    data: earnings,
    error: earningsError,
    loading: earningsLoading,
    refreshing: earningsRefreshing,
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
  } = usePolling(useCallback(() => api.getRequestManagement(), []), POLL_INTERVAL);

  const hubReport = usePolling(useCallback(() => api.getHubModels(), []), 120_000);
  const proxyReport = usePolling(useCallback(() => api.getProxyAccounts(), []), 60_000);
  const usageReport = usePolling(useCallback(() => api.getUsageHistory(chartWindow, 'node'), [chartWindow]), 60_000);

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
    refreshDashboard();
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

  const refreshing = metricsRefreshing || statusRefreshing || earningsRefreshing || modelsRefreshing || requestMgmtRefreshing || proxyReport.refreshing || hubReport.refreshing || usageReport.refreshing;
  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 bg-slate-900/95">
        <div className="mx-auto flex max-w-7xl flex-col items-stretch gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <div className="rounded-xl bg-blue-500/10 p-2 text-blue-400">
              <Server aria-hidden="true" size={24} />
            </div>
            <div className="min-w-0">
              <h1 className="truncate text-lg font-semibold tracking-tight text-white sm:text-xl">Computing Provider</h1>
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
          <div className="space-y-5">
            <section aria-labelledby="earnings-heading">
              <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 id="earnings-heading" className="text-xl font-semibold text-white">Usage and earnings</h2>
                  <p className="mt-0.5 text-sm text-slate-400">This node · estimated earnings, not provider payouts</p>
                </div>
                <div className="flex flex-wrap gap-1" role="group" aria-label="Usage and earnings time range">
                  {[['24h', '24h'], ['7d', '7d'], ['30d', '30d']].map(([value, label]) => <button key={value} type="button" aria-pressed={chartWindow === value} onClick={() => setChartWindow(value)} className={`min-h-10 rounded-lg border px-3 text-sm focus-visible:outline focus-visible:outline-sky-400 ${chartWindow === value ? 'border-blue-500 bg-blue-600 text-white' : 'border-slate-700 text-slate-300 hover:bg-slate-800'}`}>{label}</button>)}
                </div>
              </div>
              <div className="grid items-start gap-4 lg:grid-cols-2">
                <UsageChart report={usageReport} window_={chartWindow} models={earnings?.models} colours={modelColours} nodeName={status?.node_name} />
                <EarningsChart window_={chartWindow} models={earnings?.models} onColours={setModelColours} />
              </div>
              <details className="mt-3 rounded-lg border border-slate-800 bg-slate-900/40" onToggle={(event) => setShowMix(event.currentTarget.open)}>
                <summary className="min-h-10 cursor-pointer px-3 py-2 text-sm text-slate-300 focus-visible:outline focus-visible:outline-sky-400">Session mix by model <span className="ml-1 text-slate-500">· since restart</span></summary>
                {showMix && <div className="border-t border-slate-800 p-3"><ModelDistribution earnings={earnings} loading={earningsLoading && !earnings} error={earningsError} /></div>}
              </details>
            </section>

            <section aria-labelledby="operations-heading">
              <div className="mb-2">
                <h2 id="operations-heading" className="text-xl font-semibold text-white">This node</h2>
              </div>
              <div className="grid items-start gap-4 lg:grid-cols-2">
                  <ModelsPanel
                    compact
                    models={models?.models ?? []}
                    healthLog={models?.health_log}
                    prices={models?.prices ?? {}}
                    summary={models?.summary}
                    drops={status?.registration_drops}
                    loading={modelsLoading}
                    error={modelsError}
                    onRefresh={refetchModels}
                    onModelClick={setSelectedModelId}
                    authenticated={authenticated}
                    onUnlock={() => setShowAuthDialog(true)}
                  />

                <GPUPanel gpus={metrics?.gpu_metrics ?? []} loading={metricsLoading} error={metricsError} />
              </div>
            </section>

            <section aria-labelledby="provider-health-heading" className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h2 id="provider-health-heading" className="text-xl font-semibold text-white">Provider overview</h2>
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
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
                <span>{status?.registered_models?.length ?? '—'} registered to hub</span>
                <span>{models ? models.models.filter((m) => m.local_only).length : '—'} local-only models</span>
              </div>
              <OverviewAttention hub={hubReport} usage={usageReport} proxies={proxyReport} onProxies={() => handleViewChange('proxies')} onUsage={() => document.getElementById('earnings-heading')?.scrollIntoView({ behavior: 'smooth' })} />
              <MetricsPanel
                metrics={metrics}
                loading={metricsLoading}
                error={metricsError}
                earnings={earnings}
                earningsError={earningsError}
                earningsLoading={earningsLoading}
              />
            </section>

            <section aria-label="Platform listings and request controls" className="grid items-start gap-5 lg:grid-cols-2">
              <HubModelsPanel report={hubReport} />
              <RequestManagementPanel data={requestMgmt} loading={requestMgmtLoading} error={requestMgmtError} onOpenSettings={() => handleViewChange('settings')} />
            </section>

            <details className="rounded-xl border border-slate-800 bg-slate-900/60" onToggle={(event) => setShowPerformance(event.currentTarget.open)}>
              <summary className="min-h-12 cursor-pointer px-5 py-4 font-semibold text-white focus-visible:outline focus-visible:outline-sky-400">Performance history <span className="ml-2 text-sm font-normal text-slate-400">Latency, success rate, and throughput</span></summary>
              {showPerformance && <div className="border-t border-slate-800 p-3"><HistoricalChart /></div>}
            </details>

          </div>
        )}

        {activeView === 'transactions' && <RequestHistoryPanel models={models?.models ?? []} />}

        {activeView === 'proxies' && (
          <section aria-labelledby="proxies-heading" className="space-y-6">
            <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
              <h2 id="proxies-heading" className="text-xl font-semibold text-white">Proxies</h2>
              <div className="min-w-0 text-sm text-slate-300 sm:text-right">
                <p>{status?.registered_models?.length ?? '—'} models registered to hub · {models ? models.models.filter((m) => m.local_only).length : '—'} local-only models</p>
                <p className="mt-1">Client gateway: <span className="break-all font-mono text-sky-200">{usageReport.data?.local_gateway || 'not reported'}</span></p>
              </div>
            </div>
            <ProxyAccountsPanel report={proxyReport} />
            {modelsError && <p role="status" className="text-sm text-amber-200">Model routes could not be refreshed.{models ? ' Showing previous routes.' : ''}</p>}
            {models ? <ProxyRoutingPanel models={models.models} /> : <p className="text-sm text-slate-400">{modelsLoading ? 'Loading model routes…' : 'Model routes unavailable.'}</p>}
          </section>
        )}

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
          <span>Computing Provider</span>
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

import { useCallback } from 'react';
import { RefreshCw, Server } from 'lucide-react';
import { usePolling } from './hooks/usePolling';
import { api } from './api/client';
import { MetricsPanel } from './components/MetricsPanel';
import { GPUPanel } from './components/GPUPanel';
import { ModelsPanel } from './components/ModelsPanel';
import { RequestManagementPanel } from './components/RequestManagementPanel';
import { ConnectionStatus } from './components/ConnectionStatus';
import { LatencyChart } from './components/LatencyChart';
import { ThroughputChart } from './components/ThroughputChart';

const POLL_INTERVAL = 5000; // 5 seconds

function App() {
  const {
    data: metrics,
    loading: metricsLoading,
    refetch: refetchMetrics,
  } = usePolling(useCallback(() => api.getMetrics(), []), POLL_INTERVAL);

  const {
    data: status,
    loading: statusLoading,
  } = usePolling(useCallback(() => api.getStatus(), []), POLL_INTERVAL);

  const {
    data: models,
    loading: modelsLoading,
    refetch: refetchModels,
  } = usePolling(useCallback(() => api.getModels(), []), POLL_INTERVAL);

  const {
    data: requestMgmt,
    loading: requestMgmtLoading,
    refetch: refetchRequestMgmt,
  } = usePolling(useCallback(() => api.getRequestManagement(), []), POLL_INTERVAL);

  const handleRefreshAll = () => {
    refetchMetrics();
    refetchModels();
    refetchRequestMgmt();
  };

  return (
    <div className="min-h-screen bg-slate-900 text-slate-100">
      {/* Header */}
      <header className="bg-slate-800 border-b border-slate-700 px-6 py-4">
        <div className="max-w-7xl mx-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Server className="text-blue-400" size={28} />
            <div>
              <h1 className="text-xl font-bold text-slate-100">Inference Dashboard</h1>
              <p className="text-xs text-slate-400">Computing Provider v2</p>
            </div>
          </div>

          <div className="flex items-center gap-4">
            <ConnectionStatus status={status} loading={statusLoading} />
            <button
              onClick={handleRefreshAll}
              className="flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors"
            >
              <RefreshCw size={16} />
              <span className="text-sm">Refresh</span>
            </button>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="max-w-7xl mx-auto px-6 py-6 space-y-6">
        {/* Top Stats */}
        <MetricsPanel metrics={metrics} loading={metricsLoading} />

        {/* Charts Row */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <LatencyChart metrics={metrics} />
          <ThroughputChart metrics={metrics} />
        </div>

        {/* Bottom Row */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          <div className="lg:col-span-1">
            <GPUPanel gpu={metrics?.gpu ?? null} loading={metricsLoading} />
          </div>
          <div className="lg:col-span-1">
            <ModelsPanel
              models={models}
              loading={modelsLoading}
              onRefresh={refetchModels}
            />
          </div>
          <div className="lg:col-span-1">
            <RequestManagementPanel
              data={requestMgmt}
              loading={requestMgmtLoading}
              onRefresh={refetchRequestMgmt}
            />
          </div>
        </div>
      </main>

      {/* Footer */}
      <footer className="bg-slate-800 border-t border-slate-700 px-6 py-3 mt-8">
        <div className="max-w-7xl mx-auto flex items-center justify-between text-xs text-slate-500">
          <span>Swan Chain Computing Provider</span>
          <span>Auto-refresh: {POLL_INTERVAL / 1000}s</span>
        </div>
      </footer>
    </div>
  );
}

export default App;

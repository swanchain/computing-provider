import { useState, useCallback } from 'react';
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
import { RequestHistoryPanel } from './components/RequestHistoryPanel';
import { HistoricalChart } from './components/HistoricalChart';
import { ModelDetailPanel } from './components/ModelDetailPanel';

const POLL_INTERVAL = 5000; // 5 seconds

function App() {
  const [selectedModelId, setSelectedModelId] = useState<string | null>(null);

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
            <ConnectionStatus status={status} loading={statusLoading} error={statusError} />
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
        <MetricsPanel metrics={metrics} loading={metricsLoading} error={metricsError} />

        {/* Charts Row */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <LatencyChart metrics={metrics} />
          <ThroughputChart metrics={metrics} />
        </div>

        {/* Middle Row - Models, GPU, Request Management */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          <div className="lg:col-span-1">
            <GPUPanel gpus={metrics?.gpu_metrics ?? []} loading={metricsLoading} error={metricsError} />
          </div>
          <div className="lg:col-span-1">
            <ModelsPanel
              models={models?.models ?? []}
              loading={modelsLoading}
              error={modelsError}
              onRefresh={refetchModels}
              onModelClick={setSelectedModelId}
            />
          </div>
          <div className="lg:col-span-1">
            <RequestManagementPanel
              data={requestMgmt}
              loading={requestMgmtLoading}
              error={requestMgmtError}
              onRefresh={refetchRequestMgmt}
            />
          </div>
        </div>

        {/* Request History */}
        <RequestHistoryPanel models={models?.models ?? []} />

        {/* Historical Trends */}
        <HistoricalChart />
      </main>

      {/* Footer */}
      <footer className="bg-slate-800 border-t border-slate-700 px-6 py-3 mt-8">
        <div className="max-w-7xl mx-auto flex items-center justify-between text-xs text-slate-500">
          <span>Swan Chain Computing Provider</span>
          <span>Auto-refresh: {POLL_INTERVAL / 1000}s</span>
        </div>
      </footer>

      {/* Model Detail Modal */}
      {selectedModelId && (
        <ModelDetailPanel
          modelId={selectedModelId}
          onClose={() => setSelectedModelId(null)}
        />
      )}
    </div>
  );
}

export default App;

import { useState } from 'react';
import { CheckCircle, XCircle, AlertCircle, ChevronDown, ChevronUp, LockKeyhole, Power, RefreshCw, RotateCcw } from 'lucide-react';
import { api } from '../api/client';
import type { ModelPrice, ModelsResponse, ModelStatus } from '../types';

// A strip of recent health samples, oldest to newest. The current state is one
// dot; whether it has been flapping is the shape of the whole row, which a
// single indicator cannot show.
const HEALTH_DOT: Record<string, string> = {
  healthy: 'bg-emerald-400',
  degraded: 'bg-amber-400',
  unhealthy: 'bg-red-500',
  unknown: 'bg-slate-600',
};

function HealthStrip({ samples }: { samples: string[] }) {
  if (!samples || samples.length === 0) return null;
  // Newest on the right, and only the recent tail: an hour of 30-second samples
  // is more dots than a row can carry legibly.
  const shown = samples.slice(-40);
  const counts = shown.reduce<Record<string, number>>((a, s) => {
    a[s] = (a[s] ?? 0) + 1;
    return a;
  }, {});
  const summary = Object.entries(counts).map(([k, v]) => `${v} ${k}`).join(', ');
  return (
    <div className="mt-1.5 flex items-center gap-2">
      <div className="flex gap-px" role="img" aria-label={`Recent health: ${summary}`}>
        {shown.map((state, i) => (
          <span
            key={i}
            title={state}
            className={`block h-3 w-1 rounded-sm ${HEALTH_DOT[state] ?? HEALTH_DOT.unknown}`}
          />
        ))}
      </div>
      <span className="text-[10px] text-slate-400">recent</span>
    </div>
  );
}

interface ModelsPanelProps {
  models: ModelStatus[];
  healthLog?: Record<string, string[]>;
  prices: Record<string, ModelPrice>;
  loading: boolean;
  error?: Error | null;
  onRefresh: () => void;
  onModelClick?: (modelId: string) => void;
  authenticated: boolean;
  onUnlock: () => void;
  summary?: ModelsResponse['summary'];
  compact?: boolean;
}

const priceFormatter = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
});

export function ModelsPanel({ models, healthLog, prices, loading, error, onRefresh, onModelClick, authenticated, onUnlock, summary, compact = false }: ModelsPanelProps) {
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const [showAll, setShowAll] = useState(false);

  const requireAccess = () => {
    if (authenticated) return true;
    onUnlock();
    return false;
  };

  const handleToggle = async (model: ModelStatus) => {
    if (!requireAccess()) return;
    setActionLoading(model.id);
    setActionError('');
    try {
      if (model.enabled) {
        await api.disableModel(model.id);
      } else {
        await api.enableModel(model.id);
      }
      onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to update model');
    } finally {
      setActionLoading(null);
    }
  };

  const handleHealthCheck = async (modelId: string) => {
    if (!requireAccess()) return;
    setActionLoading(`health-${modelId}`);
    setActionError('');
    try {
      await api.forceHealthCheck(modelId);
      onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to run health check');
    } finally {
      setActionLoading(null);
    }
  };

  const handleReload = async () => {
    if (!requireAccess()) return;
    setActionLoading('reload');
    setActionError('');
    try {
      await api.reloadModels();
      onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to reload models');
    } finally {
      setActionLoading(null);
    }
  };

  if (loading) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">Models</h3>
        <div className="animate-pulse space-y-3">
          {[...Array(2)].map((_, i) => (
            <div key={i} className="h-16 bg-slate-700 rounded"></div>
          ))}
        </div>
      </div>
    );
  }

  const isHealthy = (model: ModelStatus) => model.health_string === 'healthy';
  const attentionModels = models.filter((model) => !model.enabled || !isHealthy(model));
  const shownModels = compact && !showAll ? attentionModels : models;
  const readyCount = summary?.ready ?? models.filter((model) => model.enabled && isHealthy(model)).length;
  const unhealthyCount = summary?.unhealthy ?? models.filter((model) => model.enabled && !isHealthy(model)).length;
  const disabledCount = summary?.disabled ?? models.filter((model) => !model.enabled).length;

  return (
    <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-lg font-semibold text-slate-100">Models</h3>
          <p className="mt-0.5 text-xs text-slate-400">
            {readyCount} ready
            {unhealthyCount > 0 && ` · ${unhealthyCount} unhealthy`}
            {disabledCount > 0 && ` · ${disabledCount} disabled`}
          </p>
        </div>
        <button
          type="button"
          onClick={handleReload}
          disabled={actionLoading === 'reload'}
          className="flex min-h-10 items-center gap-1.5 rounded-lg border border-slate-600 bg-slate-800 px-3 text-sm transition-colors hover:bg-slate-700 disabled:opacity-50"
        >
          {authenticated ? <RotateCcw aria-hidden="true" size={14} className={actionLoading === 'reload' ? 'animate-spin' : ''} /> : <LockKeyhole aria-hidden="true" size={14} />}
          Reload Config
        </button>
      </div>

      {actionError && <p role="alert" className="mb-3 rounded-lg border border-red-800/60 bg-red-950/30 px-3 py-2 text-sm text-red-300">{actionError}</p>}

      {!models || models.length === 0 ? (
        <p className="text-slate-400">{error ? 'API unreachable' : 'No models configured'}</p>
      ) : compact && !showAll && attentionModels.length === 0 ? (
        <div className="rounded-lg border border-emerald-900/60 bg-emerald-950/20 px-4 py-5 text-center">
          <CheckCircle aria-hidden="true" size={24} className="mx-auto text-emerald-300" />
          <p className="mt-2 text-sm font-medium text-emerald-100">All configured models are ready</p>
          <p className="mt-1 text-xs text-slate-400">Healthy models are collapsed to keep operational exceptions visible.</p>
        </div>
      ) : (
        <div className="space-y-3">
          {shownModels.map((model) => {
            const price = prices[model.id];
            return (
            <div
              key={model.id}
              className="flex items-start justify-between gap-2 rounded-lg border border-slate-600 bg-slate-700/50 p-3 transition-colors hover:border-slate-500 sm:items-center"
            >
              <button
                type="button"
                className="flex min-w-0 flex-1 items-start gap-3 rounded text-left focus:outline-none focus:ring-2 focus:ring-blue-500 sm:items-center"
                onClick={() => onModelClick?.(model.id)}
                aria-label={`View details for ${model.id}`}
              >
                <div className="flex-shrink-0">
                  {!model.enabled ? (
                    <AlertCircle size={20} className="text-slate-400" />
                  ) : isHealthy(model) ? (
                    <CheckCircle size={20} className="text-green-400" />
                  ) : (
                    <XCircle size={20} className="text-red-400" />
                  )}
                </div>
                <div className="min-w-0">
                  <div className="break-words font-medium text-slate-200">{model.id}</div>
                  <div className="mt-0.5 break-all text-xs text-slate-400">
                    {model.endpoint} • {model.category}
                    {model.gpu_memory > 0 && ` • ${(model.gpu_memory / 1024).toFixed(1)}GB VRAM`}
                  </div>
                  <div className="text-xs text-slate-400 mt-0.5">
                    {model.state_string} • {model.health_string}
                  </div>
                  <HealthStrip samples={healthLog?.[model.id] ?? []} />
                  {price && (
                    <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                      <span className="font-medium text-emerald-300">Provider payout / 1M</span>
                      <span className="text-blue-200">In {priceFormatter.format(price.provider_input_price)}</span>
                      <span className="text-violet-200">Out {priceFormatter.format(price.provider_output_price)}</span>
                    </div>
                  )}
                </div>
              </button>

              <div className="flex flex-shrink-0 items-center gap-1 sm:gap-2">
                <button
                  type="button"
                  onClick={() => handleHealthCheck(model.id)}
                  disabled={actionLoading === `health-${model.id}` || !model.enabled}
                  className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-600 rounded transition-colors disabled:opacity-50"
                  title="Force health check"
                  aria-label={`Run health check for ${model.id}`}
                >
                  <RefreshCw
                    size={16}
                    className={actionLoading === `health-${model.id}` ? 'animate-spin' : ''}
                  />
                </button>
                <button
                  type="button"
                  onClick={() => handleToggle(model)}
                  disabled={actionLoading === model.id}
                  className={`p-2 rounded transition-colors ${
                    model.enabled
                      ? 'text-green-400 hover:text-green-300 hover:bg-green-900/30'
                      : 'text-slate-400 hover:text-slate-300 hover:bg-slate-600'
                  } disabled:opacity-50`}
                  title={model.enabled ? 'Disable model' : 'Enable model'}
                  aria-label={`${model.enabled ? 'Disable' : 'Enable'} ${model.id}`}
                >
                  <Power size={16} />
                </button>
              </div>
            </div>
            );
          })}
        </div>
      )}

      {compact && models.length > 0 && (
        <button
          type="button"
          onClick={() => setShowAll((value) => !value)}
          className="mt-4 inline-flex min-h-10 w-full items-center justify-center gap-2 rounded-lg border border-slate-700 bg-slate-950/40 px-3 text-sm text-slate-200 transition hover:border-slate-600 hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
          aria-expanded={showAll}
        >
          {showAll ? <ChevronUp aria-hidden="true" size={16} /> : <ChevronDown aria-hidden="true" size={16} />}
          {showAll ? 'Hide healthy models' : `Show all ${models.length} models`}
        </button>
      )}
    </div>
  );
}

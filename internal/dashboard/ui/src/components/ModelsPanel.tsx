import { useState } from 'react';
import { CheckCircle, XCircle, AlertCircle, Power, RefreshCw, RotateCcw } from 'lucide-react';
import { api } from '../api/client';
import type { ModelStatus } from '../types';

interface ModelsPanelProps {
  models: ModelStatus[];
  loading: boolean;
  error?: Error | null;
  onRefresh: () => void;
  onModelClick?: (modelId: string) => void;
}

export function ModelsPanel({ models, loading, error, onRefresh, onModelClick }: ModelsPanelProps) {
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  const handleToggle = async (model: ModelStatus) => {
    setActionLoading(model.id);
    try {
      if (model.enabled) {
        await api.disableModel(model.id);
      } else {
        await api.enableModel(model.id);
      }
      onRefresh();
    } catch (err) {
      console.error('Failed to toggle model:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleHealthCheck = async (modelId: string) => {
    setActionLoading(`health-${modelId}`);
    try {
      await api.forceHealthCheck(modelId);
      onRefresh();
    } catch (err) {
      console.error('Failed to force health check:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleReload = async () => {
    setActionLoading('reload');
    try {
      await api.reloadModels();
      onRefresh();
    } catch (err) {
      console.error('Failed to reload models:', err);
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

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-slate-200">Models</h3>
        <button
          onClick={handleReload}
          disabled={actionLoading === 'reload'}
          className="flex items-center gap-1 px-3 py-1 text-sm bg-slate-700 hover:bg-slate-600 rounded transition-colors disabled:opacity-50"
        >
          <RotateCcw size={14} className={actionLoading === 'reload' ? 'animate-spin' : ''} />
          Reload Config
        </button>
      </div>

      {!models || models.length === 0 ? (
        <p className="text-slate-400">{error ? 'API unreachable' : 'No models configured'}</p>
      ) : (
        <div className="space-y-3">
          {models.map((model) => (
            <div
              key={model.id}
              className="flex items-center justify-between p-3 bg-slate-700/50 rounded-lg border border-slate-600 hover:border-slate-500 transition-colors"
            >
              <div
                className="flex items-center gap-3 flex-1 cursor-pointer"
                onClick={() => onModelClick?.(model.id)}
                title="Click for details"
              >
                <div className="flex-shrink-0">
                  {!model.enabled ? (
                    <AlertCircle size={20} className="text-slate-500" />
                  ) : isHealthy(model) ? (
                    <CheckCircle size={20} className="text-green-400" />
                  ) : (
                    <XCircle size={20} className="text-red-400" />
                  )}
                </div>
                <div>
                  <div className="font-medium text-slate-200">{model.id}</div>
                  <div className="text-xs text-slate-400">
                    {model.endpoint} • {model.category}
                    {model.gpu_memory > 0 && ` • ${(model.gpu_memory / 1024).toFixed(1)}GB VRAM`}
                  </div>
                  <div className="text-xs text-slate-500 mt-0.5">
                    {model.state_string} • {model.health_string}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-2">
                <button
                  onClick={() => handleHealthCheck(model.id)}
                  disabled={actionLoading === `health-${model.id}` || !model.enabled}
                  className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-600 rounded transition-colors disabled:opacity-50"
                  title="Force health check"
                >
                  <RefreshCw
                    size={16}
                    className={actionLoading === `health-${model.id}` ? 'animate-spin' : ''}
                  />
                </button>
                <button
                  onClick={() => handleToggle(model)}
                  disabled={actionLoading === model.id}
                  className={`p-2 rounded transition-colors ${
                    model.enabled
                      ? 'text-green-400 hover:text-green-300 hover:bg-green-900/30'
                      : 'text-slate-500 hover:text-slate-300 hover:bg-slate-600'
                  } disabled:opacity-50`}
                  title={model.enabled ? 'Disable model' : 'Enable model'}
                >
                  <Power size={16} />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

import { Gauge, Layers, RotateCcw, Settings } from 'lucide-react';
import type { RequestManagement } from '../types';

interface RequestManagementPanelProps {
  data: RequestManagement | null;
  loading: boolean;
  error?: Error | null;
  onOpenSettings: () => void;
}

export function RequestManagementPanel({ data, loading, error, onOpenSettings }: RequestManagementPanelProps) {
  if (loading) {
    return (
      <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
        <h3 className="mb-4 text-lg font-semibold text-slate-200">Request controls</h3>
        <div className="h-28 animate-pulse rounded-lg bg-slate-800" />
      </div>
    );
  }

  if (!data) {
    return (
      <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
        <h3 className="mb-4 text-lg font-semibold text-slate-200">Request controls</h3>
        <p className="text-sm text-slate-400">{error ? 'API unreachable' : 'No control data available'}</p>
      </div>
    );
  }

  const { rate_limiter, concurrency_limiter, retry_policy } = data;

  return (
    <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h3 className="text-lg font-semibold text-slate-200">Request controls</h3>
          <p className="mt-0.5 text-xs text-slate-400">Current admission and retry state</p>
        </div>
        <button type="button" onClick={onOpenSettings} className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg text-slate-400 transition hover:bg-slate-800 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500" aria-label="Open request limit settings">
          <Settings aria-hidden="true" size={18} />
        </button>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <div className="min-w-0 rounded-lg border border-slate-800 bg-slate-950/50 p-3">
          <div className="flex items-center gap-1.5 text-xs font-medium text-slate-300"><Gauge aria-hidden="true" size={14} className="text-blue-400" /><span>Rate limit</span></div>
          <div className="mt-2 text-lg font-semibold text-white">{rate_limiter.current_rate.toFixed(0)} <span className="text-xs font-normal text-slate-400">req/s</span></div>
          <div className="mt-1 text-xs text-slate-400">{rate_limiter.total_throttled} throttled · burst {rate_limiter.burst_size}</div>
        </div>
        <div className="min-w-0 rounded-lg border border-slate-800 bg-slate-950/50 p-3">
          <div className="flex items-center gap-1.5 text-xs font-medium text-slate-300"><Layers aria-hidden="true" size={14} className="text-emerald-400" /><span>Concurrency</span></div>
          <div className="mt-2 text-lg font-semibold text-white">{concurrency_limiter.global_active}<span className="text-slate-400">/{concurrency_limiter.global_max}</span></div>
          <div className="mt-1 text-xs text-slate-400">active slots · {concurrency_limiter.total_rejected} rejected</div>
        </div>
        <div className="min-w-0 rounded-lg border border-slate-800 bg-slate-950/50 p-3">
          <div className="flex items-center gap-1.5 text-xs font-medium text-slate-300"><RotateCcw aria-hidden="true" size={14} className="text-amber-300" /><span>Retry recovery</span></div>
          <div className="mt-2 text-lg font-semibold text-white">{retry_policy.total_retries > 0 ? `${(retry_policy.retry_success_rate * 100).toFixed(0)}%` : '—'}</div>
          <div className="mt-1 text-xs text-slate-400">{retry_policy.total_successes} recovered · {retry_policy.total_failures} failed</div>
        </div>
      </div>
    </div>
  );
}

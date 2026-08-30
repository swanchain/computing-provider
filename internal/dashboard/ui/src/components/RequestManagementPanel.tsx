import { Gauge, Layers, RotateCcw, Settings } from 'lucide-react';
import type { RequestManagement } from '../types';

interface RequestManagementPanelProps {
  data: RequestManagement | null;
  loading: boolean;
  error?: Error | null;
  onOpenSettings: () => void;
}

function ProgressRing({ value, max, size = 58, color }: { value: number; max: number; size?: number; color: string }) {
  const percent = max > 0 ? Math.min((value / max) * 100, 100) : 0;
  const strokeWidth = 6;
  const radius = (size - strokeWidth) / 2;
  const circumference = radius * 2 * Math.PI;
  const offset = circumference - (percent / 100) * circumference;

  return (
    <div className="relative" style={{ width: size, height: size }} aria-hidden="true">
      <svg className="-rotate-90 transform" width={size} height={size}>
        <circle cx={size / 2} cy={size / 2} r={radius} stroke="currentColor" strokeWidth={strokeWidth} fill="none" className="text-slate-700" />
        <circle cx={size / 2} cy={size / 2} r={radius} stroke="currentColor" strokeWidth={strokeWidth} fill="none" strokeDasharray={circumference} strokeDashoffset={offset} strokeLinecap="round" className={color} />
      </svg>
      <div className="absolute inset-0 flex items-center justify-center"><span className="text-xs font-medium text-slate-300">{percent.toFixed(0)}%</span></div>
    </div>
  );
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
          <p className="mt-0.5 text-xs text-slate-500">Current admission and retry state</p>
        </div>
        <button type="button" onClick={onOpenSettings} className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg text-slate-400 transition hover:bg-slate-800 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500" aria-label="Open request limit settings">
          <Settings aria-hidden="true" size={18} />
        </button>
      </div>

      <div className="grid grid-cols-3 gap-2 sm:gap-4">
        <div className="min-w-0 text-center">
          <div className="mb-2 flex justify-center"><ProgressRing value={rate_limiter.current_tokens} max={rate_limiter.burst_size} color="text-blue-500" /></div>
          <div className="flex items-center justify-center gap-1 text-xs text-slate-300 sm:text-sm"><Gauge aria-hidden="true" size={13} /><span>Rate</span></div>
          <div className="mt-1 text-[11px] leading-4 text-slate-500 sm:text-xs">{rate_limiter.current_rate.toFixed(0)} req/s<br />{rate_limiter.total_throttled} throttled</div>
        </div>
        <div className="min-w-0 text-center">
          <div className="mb-2 flex justify-center"><ProgressRing value={concurrency_limiter.global_active} max={concurrency_limiter.global_max} color="text-emerald-500" /></div>
          <div className="flex items-center justify-center gap-1 text-xs text-slate-300 sm:text-sm"><Layers aria-hidden="true" size={13} /><span>Active</span></div>
          <div className="mt-1 text-[11px] leading-4 text-slate-500 sm:text-xs">{concurrency_limiter.global_active}/{concurrency_limiter.global_max} slots<br />{concurrency_limiter.total_rejected} rejected</div>
        </div>
        <div className="min-w-0 text-center">
          <div className="mb-2 flex justify-center"><ProgressRing value={retry_policy.total_successes} max={retry_policy.total_attempts || 1} color="text-amber-500" /></div>
          <div className="flex items-center justify-center gap-1 text-xs text-slate-300 sm:text-sm"><RotateCcw aria-hidden="true" size={13} /><span>Retries</span></div>
          <div className="mt-1 text-[11px] leading-4 text-slate-500 sm:text-xs">{retry_policy.total_retries} retried<br />{retry_policy.total_failures} failed</div>
        </div>
      </div>
    </div>
  );
}

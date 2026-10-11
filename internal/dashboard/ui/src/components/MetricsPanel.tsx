import type { Earnings, InferenceMetrics } from '../types';

interface MetricsPanelProps {
  metrics: InferenceMetrics | null;
  loading: boolean;
  error?: Error | null;
  earnings?: Earnings | null;
  earningsError?: Error | null;
  earningsLoading?: boolean;
}

function formatNumber(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return n.toFixed(0);
}

export function MetricsPanel({ metrics, loading, error, earnings, earningsError, earningsLoading }: MetricsPanelProps) {
  if (loading) {
    return (
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-slate-800 bg-slate-800 xl:grid-cols-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="animate-pulse bg-slate-900 p-3">
            <div className="mb-2 h-3 w-20 rounded bg-slate-700"></div>
            <div className="h-6 w-16 rounded bg-slate-700"></div>
          </div>
        ))}
      </div>
    );
  }

  const completedRequests = metrics ? metrics.successful_requests + metrics.failed_requests : 0;
  const hasRequests = completedRequests > 0;
  const successRate = hasRequests && metrics
    ? ((metrics.successful_requests / completedRequests) * 100).toFixed(1)
    : null;
  const platformUnavailable = Boolean(earnings?.platform?.unavailable);
  const earningsUnavailable = Boolean(earningsError) || (!earnings && !earningsLoading) || platformUnavailable;
  const uptime = earningsUnavailable ? null : earnings?.platform?.uptime_7d_percent;
  const signalColor = (value: number | null) => value == null ? 'text-slate-300' : value >= 99 ? 'text-emerald-300' : value >= 95 ? 'text-amber-300' : 'text-rose-300';
  const cells = [
    {
      label: '7-day uptime · platform',
      value: uptime == null ? '—' : `${uptime.toFixed(2)}%`,
      detail: earningsLoading && !earnings ? 'Loading…' : earningsUnavailable ? 'Unavailable' : '',
      colour: signalColor(uptime ?? null),
    },
    {
      label: 'Session success',
      value: successRate == null ? '—' : `${successRate}%`,
      detail: !metrics ? (error ? 'Unavailable' : 'No data') : hasRequests ? `${metrics.failed_requests} failed / ${formatNumber(completedRequests)} requests` : 'No requests yet',
      colour: signalColor(successRate == null ? null : parseFloat(successRate)),
    },
    {
      label: 'Session P95 latency',
      value: metrics && hasRequests ? `${metrics.p95_latency_ms.toFixed(0)}ms` : '—',
      detail: !metrics ? (error ? 'Unavailable' : 'No data') : hasRequests ? '' : 'No requests yet',
      colour: 'text-sky-300',
    },
    {
      label: 'Request rate',
      value: metrics ? `${metrics.requests_per_minute.toFixed(1)}/min` : '—',
      detail: !metrics ? (error ? 'Unavailable' : 'No data') : `${metrics.active_requests} active now`,
      colour: 'text-sky-300',
    },
  ];

  return (
    <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-slate-800 bg-slate-800 xl:grid-cols-4">
      {cells.map((cell) => <div key={cell.label} className="min-w-0 bg-slate-900 px-3 py-2.5">
        <dt className="truncate text-xs text-slate-400">{cell.label}</dt>
        <dd className={`mt-0.5 text-lg font-semibold tabular-nums ${cell.colour}`}>{cell.value}</dd>
        {cell.detail && <dd className="truncate text-[11px] text-slate-500">{cell.detail}</dd>}
      </div>)}
    </dl>
  );
}

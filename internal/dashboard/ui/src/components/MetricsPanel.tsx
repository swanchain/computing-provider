import { Activity, Clock, Coins, Gauge, ShieldCheck } from 'lucide-react';
import { StatusCard } from './StatusCard';
import type { Earnings, InferenceMetrics } from '../types';

interface MetricsPanelProps {
  metrics: InferenceMetrics | null;
  loading: boolean;
  error?: Error | null;
  earnings?: Earnings | null;
  earningsError?: Error | null;
  earningsLoading?: boolean;
}

// Sub-cent sums are common on a quiet day; rounding them to $0.00 makes a
// working provider look like it earned nothing.
function formatUSD(v: number): string {
  if (v === 0) return '$0.00';
  if (v < 0.01) return `$${v.toFixed(5)}`;
  if (v < 1) return `$${v.toFixed(4)}`;
  return `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatNumber(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return n.toFixed(0);
}

export function MetricsPanel({ metrics, loading, error, earnings, earningsError, earningsLoading }: MetricsPanelProps) {
  if (loading) {
    return (
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 md:gap-4 xl:grid-cols-5">
        {[...Array(5)].map((_, i) => (
          <div key={i} className="animate-pulse rounded-xl border border-slate-700 bg-slate-900 p-3 sm:p-4">
            <div className="h-4 bg-slate-700 rounded w-20 mb-2"></div>
            <div className="h-8 bg-slate-700 rounded w-16"></div>
          </div>
        ))}
      </div>
    );
  }

  const hasRequests = Boolean(metrics && metrics.total_requests > 0);
  const successRate = hasRequests && metrics
    ? ((metrics.successful_requests / metrics.total_requests) * 100).toFixed(1)
    : null;
  const platformUnavailable = Boolean(earnings?.platform?.unavailable);
  const earningsUnavailable = Boolean(earningsError) || (!earnings && !earningsLoading) || platformUnavailable;
  const uptime = earningsUnavailable ? null : earnings?.platform?.uptime_7d_percent;
  const uptimeColor = uptime == null ? 'gray' : uptime >= 99 ? 'green' : uptime >= 95 ? 'yellow' : 'red';
  const successColor = successRate == null ? 'gray' : parseFloat(successRate) >= 99 ? 'green' : parseFloat(successRate) >= 95 ? 'yellow' : 'red';

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 md:gap-4 xl:grid-cols-5">
      <StatusCard
        title="7-day uptime"
        value={uptime == null ? '--' : `${uptime.toFixed(2)}%`}
        subtitle={earningsLoading && !earnings ? 'Loading platform data' : earningsUnavailable ? 'Platform data unavailable' : 'reported by Swan Inference'}
        icon={<ShieldCheck aria-hidden="true" size={20} />}
        color={uptimeColor}
      />
      <StatusCard
        title="Session success"
        value={successRate == null ? '--' : `${successRate}%`}
        subtitle={!metrics ? (error ? 'Metrics API unavailable' : 'No data') : hasRequests ? `${metrics.failed_requests} failed of ${formatNumber(metrics.total_requests)}` : 'No requests served yet'}
        icon={<Activity aria-hidden="true" size={20} />}
        color={successColor}
      />
      <StatusCard
        title="P95 latency"
        value={metrics && hasRequests ? `${metrics.p95_latency_ms.toFixed(0)}ms` : '--'}
        subtitle={!metrics ? (error ? 'Metrics API unavailable' : 'No data') : hasRequests ? `Average ${metrics.avg_latency_ms.toFixed(0)}ms · no SLA applied` : 'No requests served yet'}
        icon={<Clock aria-hidden="true" size={20} />}
        color="blue"
      />
      <StatusCard
        title="Request rate"
        value={metrics ? `${metrics.requests_per_minute.toFixed(1)}/min` : '--'}
        subtitle={!metrics ? (error ? 'Metrics API unavailable' : 'No data') : `${metrics.active_requests} active now`}
        icon={<Gauge aria-hidden="true" size={20} />}
        color="blue"
      />
      <StatusCard
        title="Lifetime earned"
        value={earningsUnavailable || !earnings ? '--' : formatUSD(earnings.platform.total_usd)}
        // Swan Inference publishes one figure: lifetime gross. There is no
        // paid/outstanding balance to show, so the subtitle says what the
        // number is rather than implying it is money waiting to be drawn.
        subtitle={earningsLoading && !earnings ? 'Loading platform data' : earningsUnavailable ? 'Platform data unavailable' : 'authoritative platform total'}
        icon={<Coins aria-hidden="true" size={20} />}
        color={earningsUnavailable ? 'gray' : 'green'}
      />
    </div>
  );
}

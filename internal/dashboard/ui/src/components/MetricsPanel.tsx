import { Activity, Clock, Coins, Wifi, Zap } from 'lucide-react';
import { StatusCard } from './StatusCard';
import type { Earnings, InferenceMetrics } from '../types';

interface MetricsPanelProps {
  metrics: InferenceMetrics | null;
  loading: boolean;
  error?: Error | null;
  earnings?: Earnings | null;
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

export function MetricsPanel({ metrics, loading, error, earnings }: MetricsPanelProps) {
  if (loading) {
    return (
      <div className="grid grid-cols-2 gap-3 md:grid-cols-5 md:gap-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="animate-pulse rounded-xl border border-slate-700 bg-slate-900 p-3 sm:p-4">
            <div className="h-4 bg-slate-700 rounded w-20 mb-2"></div>
            <div className="h-8 bg-slate-700 rounded w-16"></div>
          </div>
        ))}
      </div>
    );
  }

  if (!metrics) {
    return (
      <div className="grid grid-cols-2 gap-3 md:grid-cols-5 md:gap-4">
        <StatusCard title="Total Requests" value="--" subtitle={error ? "API unreachable" : "No data"} icon={<Activity size={20} />} color="blue" />
        <StatusCard title="Success Rate" value="--" subtitle={error ? "API unreachable" : "No data"} icon={<Zap size={20} />} color="blue" />
        <StatusCard title="Avg Latency" value="--" subtitle={error ? "API unreachable" : "No data"} icon={<Clock size={20} />} color="blue" />
        <StatusCard title="Connection" value="Unknown" subtitle={error ? "API unreachable" : "No data"} icon={<Wifi size={20} />} color="red" />
        <StatusCard title="Earned" value="--" subtitle={error ? "API unreachable" : "No data"} icon={<Coins size={20} />} color="green" />
      </div>
    );
  }

  const successRate = metrics.total_requests > 0
    ? ((metrics.successful_requests / metrics.total_requests) * 100).toFixed(1)
    : '100.0';

  const isConnected = metrics.connection_state === 'connected';

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-5 md:gap-4">
      <StatusCard
        title="Total Requests"
        value={formatNumber(metrics.total_requests)}
        subtitle={`${metrics.successful_requests} successful`}
        icon={<Activity size={20} />}
        color="blue"
      />
      <StatusCard
        title="Success Rate"
        value={`${successRate}%`}
        subtitle={`${metrics.failed_requests} failed`}
        icon={<Zap size={20} />}
        color={parseFloat(successRate) >= 95 ? 'green' : parseFloat(successRate) >= 80 ? 'yellow' : 'red'}
      />
      <StatusCard
        title="Avg Latency"
        value={`${metrics.avg_latency_ms.toFixed(0)}ms`}
        subtitle={`P99: ${metrics.p99_latency_ms.toFixed(0)}ms`}
        icon={<Clock size={20} />}
        color={metrics.avg_latency_ms < 100 ? 'green' : metrics.avg_latency_ms < 500 ? 'yellow' : 'red'}
      />
      <StatusCard
        title="Connection"
        value={isConnected ? 'Connected' : 'Disconnected'}
        subtitle={`${metrics.active_requests} active requests`}
        icon={<Wifi size={20} />}
        color={isConnected ? 'green' : 'red'}
      />
        <StatusCard
          title="Earned"
          value={earnings?.platform?.unavailable ? '--' : formatUSD(earnings?.platform?.total_usd ?? 0)}
          // Swan Inference publishes one figure: lifetime gross. There is no
          // paid/outstanding balance to show, so the subtitle says what the
          // number is rather than implying it is money waiting to be drawn.
          subtitle={earnings?.platform?.unavailable ? 'Swan Inference unreachable' : 'all time, from Swan Inference'}
          icon={<Coins size={20} />}
          color="green"
        />
    </div>
  );
}

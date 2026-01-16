import { Activity, Clock, Zap, Users } from 'lucide-react';
import { StatusCard } from './StatusCard';
import type { InferenceMetrics } from '../types';

interface MetricsPanelProps {
  metrics: InferenceMetrics | null;
  loading: boolean;
}

function formatNumber(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return n.toFixed(0);
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

export function MetricsPanel({ metrics, loading }: MetricsPanelProps) {
  if (loading || !metrics) {
    return (
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="bg-slate-800 rounded-lg p-4 border border-slate-700 animate-pulse">
            <div className="h-4 bg-slate-700 rounded w-20 mb-2"></div>
            <div className="h-8 bg-slate-700 rounded w-16"></div>
          </div>
        ))}
      </div>
    );
  }

  const successRate = metrics.total_requests > 0
    ? ((metrics.successful_requests / metrics.total_requests) * 100).toFixed(1)
    : '0.0';

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
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
        title="Active Connections"
        value={metrics.active_connections}
        subtitle={`Uptime: ${formatUptime(metrics.uptime_seconds)}`}
        icon={<Users size={20} />}
        color="green"
      />
    </div>
  );
}

import { useState, useCallback } from 'react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Legend,
} from 'recharts';
import { Calendar, RefreshCw } from 'lucide-react';
import { usePolling } from '../hooks/usePolling';
import { api } from '../api/client';

type TimeRange = '1h' | '6h' | '24h' | '7d';

interface TimeRangeConfig {
  duration: string;
  resolution: string;
  label: string;
}

const TIME_RANGES: Record<TimeRange, TimeRangeConfig> = {
  '1h': { duration: '1h', resolution: '1m', label: '1 Hour' },
  '6h': { duration: '6h', resolution: '5m', label: '6 Hours' },
  '24h': { duration: '24h', resolution: '15m', label: '24 Hours' },
  '7d': { duration: '168h', resolution: '1h', label: '7 Days' },
};

export function HistoricalChart() {
  const [timeRange, setTimeRange] = useState<TimeRange>('1h');

  const config = TIME_RANGES[timeRange];

  const {
    data: historyData,
    error,
    loading,
    refetch,
  } = usePolling(
    useCallback(
      () => api.getMetricsHistory(config.duration, config.resolution),
      [config.duration, config.resolution]
    ),
    60000 // Refresh every minute
  );

  const dataPoints = historyData?.data ?? [];

  const formatTime = (timestamp: string) => {
    const date = new Date(timestamp);
    if (timeRange === '7d') {
      return date.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' });
    }
    if (timeRange === '24h') {
      return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    }
    return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  };

  const chartData = dataPoints.map((point) => ({
    time: formatTime(point.timestamp),
    requests: point.total_requests,
    successRate: point.success_rate,
    avgLatency: point.avg_latency_ms,
    p99Latency: point.p99_latency_ms,
    tokensPerSec: point.tokens_per_second,
  }));
  const latest = chartData[chartData.length - 1];
  const latencyValues = chartData.flatMap((point) => [point.avgLatency, point.p99Latency]);
  const latencyMin = latencyValues.length > 0 ? Math.min(...latencyValues) : 0;
  const latencyMax = latencyValues.length > 0 ? Math.max(...latencyValues) : 0;
  const successMin = chartData.length > 0 ? Math.min(...chartData.map((point) => point.successRate)) : 0;
  const successMax = chartData.length > 0 ? Math.max(...chartData.map((point) => point.successRate)) : 0;
  const throughputMin = chartData.length > 0 ? Math.min(...chartData.map((point) => point.tokensPerSec)) : 0;
  const throughputMax = chartData.length > 0 ? Math.max(...chartData.map((point) => point.tokensPerSec)) : 0;

  return (
    <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2">
          <Calendar size={20} className="text-purple-400" />
          <div>
            <h3 className="text-lg font-semibold text-slate-100">Performance trends</h3>
            <p className="mt-0.5 text-xs text-slate-400">Persisted service signals across one shared time range</p>
          </div>
        </div>
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex min-w-0 flex-1 overflow-x-auto rounded-lg bg-slate-800 p-0.5 sm:flex-none">
            {(Object.keys(TIME_RANGES) as TimeRange[]).map((range) => (
              <button
                key={range}
                onClick={() => setTimeRange(range)}
                    className={`min-h-10 flex-1 whitespace-nowrap rounded px-2 py-1 text-xs font-medium transition-colors sm:flex-none sm:px-3 ${
                  timeRange === range
                    ? 'bg-blue-600 text-white'
                    : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                {TIME_RANGES[range].label}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={refetch}
            className="inline-flex min-h-10 min-w-10 items-center justify-center rounded text-slate-300 transition-colors hover:bg-slate-700 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            title="Refresh"
            aria-label="Refresh performance trends"
          >
            <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      {error && (
        <div role="alert" className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-800/70 bg-amber-950/30 px-3 py-2 text-sm text-amber-100">
          <span>{historyData ? 'Showing the last loaded trends; refresh failed.' : `Performance trends are unavailable: ${error.message}`}</span>
          <button type="button" onClick={refetch} className="min-h-10 rounded-lg border border-amber-700/70 px-3 text-sm hover:bg-amber-900/30">Try again</button>
        </div>
      )}

      {loading && chartData.length === 0 ? (
        <div className="h-64 flex items-center justify-center">
          <div className="animate-spin w-8 h-8 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        </div>
      ) : chartData.length < 2 ? (
        <div className="h-64 flex items-center justify-center text-slate-400">
          <div className="text-center">
            <Calendar size={32} className="mx-auto mb-2 opacity-50" />
            <p>Not enough historical data yet</p>
            <p className="text-xs mt-1">Data is recorded every minute</p>
          </div>
        </div>
      ) : (
        <div className="grid gap-6 lg:grid-cols-3">
          {/* Latency Chart */}
          <div role="img" aria-label={`Latency ranged from ${latencyMin.toFixed(0)} to ${latencyMax.toFixed(0)} milliseconds. Latest average ${latest?.avgLatency.toFixed(0)} milliseconds and P99 ${latest?.p99Latency.toFixed(0)} milliseconds.`}>
            <h4 className="mb-2 text-sm font-medium text-slate-300">Latency (ms)</h4>
            <div className="h-40" aria-hidden="true">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData} accessibilityLayer={false}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                  <XAxis
                    dataKey="time"
                    stroke="#64748b"
                    fontSize={10}
                    tickLine={false}
                    interval="preserveStartEnd"
                  />
                  <YAxis stroke="#64748b" fontSize={10} tickLine={false} />
                  <Tooltip
                    contentStyle={{
                      backgroundColor: '#1e293b',
                      border: '1px solid #334155',
                      borderRadius: '6px',
                      fontSize: '12px',
                    }}
                    labelStyle={{ color: '#94a3b8' }}
                    formatter={(value) => [(typeof value === 'number' ? value.toFixed(1) : value) + 'ms', '']}
                  />
                  <Legend
                    wrapperStyle={{ fontSize: '10px' }}
                    formatter={(value) => <span className="text-slate-400">{value}</span>}
                  />
                  <Line
                    type="monotone"
                    dataKey="avgLatency"
                    stroke="#3b82f6"
                    strokeWidth={2}
                    dot={false}
                    name="Avg"
                  />
                  <Line
                    type="monotone"
                    dataKey="p99Latency"
                    stroke="#ef4444"
                    strokeWidth={1.5}
                    dot={false}
                    name="P99"
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>

          {/* Success Rate Chart */}
          <div role="img" aria-label={`Success rate ranged from ${successMin.toFixed(1)} to ${successMax.toFixed(1)} percent. Latest ${latest?.successRate.toFixed(1)} percent.`}>
            <h4 className="mb-2 text-sm font-medium text-slate-300">Success rate (%)</h4>
            <div className="h-40" aria-hidden="true">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData} accessibilityLayer={false}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                  <XAxis
                    dataKey="time"
                    stroke="#64748b"
                    fontSize={10}
                    tickLine={false}
                    interval="preserveStartEnd"
                  />
                  <YAxis stroke="#64748b" fontSize={10} tickLine={false} domain={[0, 100]} />
                  <Tooltip
                    contentStyle={{
                      backgroundColor: '#1e293b',
                      border: '1px solid #334155',
                      borderRadius: '6px',
                      fontSize: '12px',
                    }}
                    labelStyle={{ color: '#94a3b8' }}
                    formatter={(value) => [(typeof value === 'number' ? value.toFixed(1) : value) + '%', 'Success Rate']}
                  />
                  <Line
                    type="monotone"
                    dataKey="successRate"
                    stroke="#22c55e"
                    strokeWidth={2}
                    dot={false}
                    name="Success Rate"
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>

          {/* Throughput Chart */}
          <div role="img" aria-label={`Throughput ranged from ${throughputMin.toFixed(1)} to ${throughputMax.toFixed(1)} tokens per second. Latest ${latest?.tokensPerSec.toFixed(1)} tokens per second.`}>
            <h4 className="mb-2 text-sm font-medium text-slate-300">Throughput (tokens/sec)</h4>
            <div className="h-40" aria-hidden="true">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData} accessibilityLayer={false}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                  <XAxis
                    dataKey="time"
                    stroke="#64748b"
                    fontSize={10}
                    tickLine={false}
                    interval="preserveStartEnd"
                  />
                  <YAxis stroke="#64748b" fontSize={10} tickLine={false} />
                  <Tooltip
                    contentStyle={{
                      backgroundColor: '#1e293b',
                      border: '1px solid #334155',
                      borderRadius: '6px',
                      fontSize: '12px',
                    }}
                    labelStyle={{ color: '#94a3b8' }}
                    formatter={(value) => [typeof value === 'number' ? value.toFixed(1) : value, 'Tokens/sec']}
                  />
                  <Line
                    type="monotone"
                    dataKey="tokensPerSec"
                    stroke="#a855f7"
                    strokeWidth={2}
                    dot={false}
                    name="Tokens/sec"
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>
      )}

      <div className="mt-4 text-center text-xs text-slate-400">
        Showing {config.label} of data ({config.resolution} resolution)
      </div>
    </div>
  );
}

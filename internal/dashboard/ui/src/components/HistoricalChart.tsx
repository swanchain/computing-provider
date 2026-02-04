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

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <Calendar size={20} className="text-purple-400" />
          <h3 className="text-lg font-semibold text-slate-200">Historical Trends</h3>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex bg-slate-700 rounded-lg p-0.5">
            {(Object.keys(TIME_RANGES) as TimeRange[]).map((range) => (
              <button
                key={range}
                onClick={() => setTimeRange(range)}
                className={`px-3 py-1 text-xs font-medium rounded transition-colors ${
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
            onClick={refetch}
            className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-700 rounded transition-colors"
            title="Refresh"
          >
            <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      {loading && chartData.length === 0 ? (
        <div className="h-64 flex items-center justify-center">
          <div className="animate-spin w-8 h-8 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        </div>
      ) : chartData.length < 2 ? (
        <div className="h-64 flex items-center justify-center text-slate-500">
          <div className="text-center">
            <Calendar size={32} className="mx-auto mb-2 opacity-50" />
            <p>Not enough historical data yet</p>
            <p className="text-xs mt-1">Data is recorded every minute</p>
          </div>
        </div>
      ) : (
        <div className="space-y-6">
          {/* Latency Chart */}
          <div>
            <h4 className="text-sm font-medium text-slate-400 mb-2">Latency (ms)</h4>
            <div className="h-40">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData}>
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
          <div>
            <h4 className="text-sm font-medium text-slate-400 mb-2">Success Rate (%)</h4>
            <div className="h-32">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData}>
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
          <div>
            <h4 className="text-sm font-medium text-slate-400 mb-2">Throughput (tokens/sec)</h4>
            <div className="h-32">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData}>
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

      <div className="mt-4 text-xs text-slate-500 text-center">
        Showing {config.label} of data ({config.resolution} resolution)
      </div>
    </div>
  );
}

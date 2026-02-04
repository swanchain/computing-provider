import { useState, useEffect } from 'react';
import { X, CheckCircle, XCircle, AlertCircle, Activity, Clock, Zap } from 'lucide-react';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api } from '../api/client';
import type { ModelDetailedMetrics, RequestLog } from '../types';

interface ModelDetailPanelProps {
  modelId: string;
  onClose: () => void;
}

export function ModelDetailPanel({ modelId, onClose }: ModelDetailPanelProps) {
  const [data, setData] = useState<ModelDetailedMetrics | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      setError(null);
      try {
        const result = await api.getModelMetrics(modelId);
        setData(result);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load model metrics');
      } finally {
        setLoading(false);
      }
    };
    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, [modelId]);

  const formatTime = (timeStr: string) => {
    if (!timeStr) return '-';
    const date = new Date(timeStr);
    return date.toLocaleTimeString();
  };

  const formatLatency = (ms: number) => {
    if (ms < 1000) return `${ms.toFixed(0)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  };

  // Generate latency history data from recent requests
  const latencyData = (data?.recent_requests ?? [])
    .slice()
    .reverse()
    .map((req: RequestLog) => ({
      time: formatTime(req.start_time),
      latency: req.latency_ms,
    }));

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-slate-800 rounded-lg border border-slate-700 w-full max-w-3xl max-h-[90vh] overflow-y-auto m-4"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-slate-700">
          <div>
            <h2 className="text-xl font-semibold text-slate-100">{modelId}</h2>
            <p className="text-sm text-slate-400">Model Details</p>
          </div>
          <button
            onClick={onClose}
            className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-700 rounded transition-colors"
          >
            <X size={20} />
          </button>
        </div>

        {loading && !data ? (
          <div className="p-8 text-center">
            <div className="animate-spin w-8 h-8 border-2 border-blue-500 border-t-transparent rounded-full mx-auto"></div>
            <p className="mt-4 text-slate-400">Loading model metrics...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <XCircle size={32} className="mx-auto text-red-400 mb-2" />
            <p className="text-red-400">{error}</p>
          </div>
        ) : (
          <div className="p-4 space-y-6">
            {/* Health Status */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div className="bg-slate-700/50 rounded-lg p-4">
                <div className="flex items-center gap-2 mb-2">
                  {data?.health?.healthy ? (
                    <CheckCircle size={20} className="text-green-400" />
                  ) : (
                    <XCircle size={20} className="text-red-400" />
                  )}
                  <span className="text-sm font-medium text-slate-300">Health Status</span>
                </div>
                <p className={`text-lg font-semibold ${data?.health?.healthy ? 'text-green-400' : 'text-red-400'}`}>
                  {data?.health?.healthy ? 'Healthy' : 'Unhealthy'}
                </p>
                {data?.health?.consecutive_failures ? (
                  <p className="text-xs text-slate-500 mt-1">
                    {data.health.consecutive_failures} consecutive failures
                  </p>
                ) : null}
              </div>

              <div className="bg-slate-700/50 rounded-lg p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Activity size={20} className="text-blue-400" />
                  <span className="text-sm font-medium text-slate-300">Total Requests</span>
                </div>
                <p className="text-lg font-semibold text-slate-100">
                  {data?.metrics?.total_requests?.toLocaleString() ?? 0}
                </p>
                <p className="text-xs text-slate-500 mt-1">
                  {data?.metrics?.successful_requests ?? 0} successful, {data?.metrics?.failed_requests ?? 0} failed
                </p>
              </div>

              <div className="bg-slate-700/50 rounded-lg p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Clock size={20} className="text-yellow-400" />
                  <span className="text-sm font-medium text-slate-300">Avg Latency</span>
                </div>
                <p className="text-lg font-semibold text-slate-100">
                  {formatLatency(data?.metrics?.avg_latency_ms ?? 0)}
                </p>
                <p className="text-xs text-slate-500 mt-1">
                  {data?.metrics?.active_requests ?? 0} active requests
                </p>
              </div>
            </div>

            {/* Token Stats */}
            <div className="bg-slate-700/50 rounded-lg p-4">
              <div className="flex items-center gap-2 mb-3">
                <Zap size={20} className="text-purple-400" />
                <span className="text-sm font-medium text-slate-300">Token Statistics</span>
              </div>
              <div className="grid grid-cols-3 gap-4 text-center">
                <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {((data?.metrics?.total_tokens_in ?? 0) / 1000).toFixed(1)}K
                  </p>
                  <p className="text-xs text-slate-500">Input Tokens</p>
                </div>
                <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {((data?.metrics?.total_tokens_out ?? 0) / 1000).toFixed(1)}K
                  </p>
                  <p className="text-xs text-slate-500">Output Tokens</p>
                </div>
                <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {(data?.metrics?.tokens_per_second ?? 0).toFixed(1)}
                  </p>
                  <p className="text-xs text-slate-500">Tokens/sec</p>
                </div>
              </div>
            </div>

            {/* Latency Chart */}
            {latencyData.length > 1 && (
              <div className="bg-slate-700/50 rounded-lg p-4">
                <h4 className="text-sm font-medium text-slate-300 mb-3">Request Latency (Recent)</h4>
                <div className="h-40">
                  <ResponsiveContainer width="100%" height="100%">
                    <LineChart data={latencyData}>
                      <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                      <XAxis dataKey="time" stroke="#64748b" fontSize={10} tickLine={false} />
                      <YAxis stroke="#64748b" fontSize={10} tickLine={false} unit="ms" />
                      <Tooltip
                        contentStyle={{
                          backgroundColor: '#1e293b',
                          border: '1px solid #334155',
                          borderRadius: '6px',
                          fontSize: '12px',
                        }}
                        labelStyle={{ color: '#94a3b8' }}
                      />
                      <Line
                        type="monotone"
                        dataKey="latency"
                        stroke="#3b82f6"
                        strokeWidth={2}
                        dot={{ fill: '#3b82f6', strokeWidth: 0, r: 3 }}
                        name="Latency"
                      />
                    </LineChart>
                  </ResponsiveContainer>
                </div>
              </div>
            )}

            {/* Recent Requests */}
            <div className="bg-slate-700/50 rounded-lg p-4">
              <h4 className="text-sm font-medium text-slate-300 mb-3">Recent Requests</h4>
              {(data?.recent_requests ?? []).length === 0 ? (
                <p className="text-slate-500 text-center py-4">No recent requests</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="text-slate-400 border-b border-slate-600">
                        <th className="text-left py-2 px-2 font-medium">Time</th>
                        <th className="text-right py-2 px-2 font-medium">Latency</th>
                        <th className="text-right py-2 px-2 font-medium">Tokens</th>
                        <th className="text-center py-2 px-2 font-medium">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(data?.recent_requests ?? []).slice(0, 10).map((req: RequestLog) => (
                        <tr key={req.request_id} className="border-b border-slate-600/50">
                          <td className="py-2 px-2 text-slate-300 text-xs">{formatTime(req.start_time)}</td>
                          <td className="py-2 px-2 text-right font-mono text-xs">
                            <span
                              className={
                                req.latency_ms > 5000
                                  ? 'text-red-400'
                                  : req.latency_ms > 2000
                                  ? 'text-yellow-400'
                                  : 'text-green-400'
                              }
                            >
                              {formatLatency(req.latency_ms)}
                            </span>
                          </td>
                          <td className="py-2 px-2 text-right text-slate-400 font-mono text-xs">
                            {req.tokens_in}/{req.tokens_out}
                          </td>
                          <td className="py-2 px-2 text-center">
                            {req.success ? (
                              <CheckCircle size={14} className="inline text-green-400" />
                            ) : (
                              <XCircle size={14} className="inline text-red-400" />
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>

            {/* Error Info */}
            {data?.health?.last_error && (
              <div className="bg-red-900/20 border border-red-800/50 rounded-lg p-4">
                <div className="flex items-center gap-2 mb-2">
                  <AlertCircle size={20} className="text-red-400" />
                  <span className="text-sm font-medium text-red-300">Last Error</span>
                </div>
                <p className="text-sm text-red-400 font-mono">{data.health.last_error}</p>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

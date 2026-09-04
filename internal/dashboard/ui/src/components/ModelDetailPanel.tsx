import { useState, useEffect, useRef } from 'react';
import { X, CheckCircle, XCircle, AlertCircle, Activity, Clock, Zap, CircleDollarSign } from 'lucide-react';
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
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.setTimeout(() => closeButtonRef.current?.focus(), 0);

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== 'Tab' || !dialogRef.current) return;
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])',
      ));
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      document.body.style.overflow = previousOverflow;
      previousFocusRef.current?.focus();
    };
  }, [onClose]);

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

  const formatTokenCount = (tokens: number) => {
    if (tokens < 1_000) return tokens.toLocaleString();
    if (tokens < 1_000_000) return `${(tokens / 1_000).toFixed(1)}K`;
    return `${(tokens / 1_000_000).toFixed(1)}M`;
  };

  const formatPrice = (amount: number) => new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(amount);

  const estimatedPayout = data?.price
    ? ((data.metrics?.total_tokens_in ?? 0) * data.price.provider_input_price
      + (data.metrics?.total_tokens_out ?? 0) * data.price.provider_output_price) / 1_000_000
    : null;
  const healthy = data?.health?.health_string === 'healthy' || data?.model?.health_string === 'healthy';

  // Generate latency history data from recent requests
  const latencyData = (data?.recent_requests ?? [])
    .slice()
    .reverse()
    .map((req: RequestLog) => ({
      time: formatTime(req.start_time),
      latency: req.latency_ms,
    }));

  return (
    <div ref={dialogRef} className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-2 sm:p-4" onClick={onClose} role="dialog" aria-modal="true" aria-labelledby="model-detail-title" aria-describedby="model-detail-description">
      <div
        className="max-h-[94vh] w-full max-w-4xl overflow-y-auto rounded-xl border border-slate-700 bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="sticky top-0 z-10 flex items-center justify-between border-b border-slate-700 bg-slate-900 p-4">
          <div>
            <h2 id="model-detail-title" className="break-words text-xl font-semibold text-slate-100">{modelId}</h2>
            <p id="model-detail-description" className="text-sm text-slate-300">Health, usage, pricing, and recent requests</p>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            onClick={onClose}
            className="inline-flex min-h-10 min-w-10 items-center justify-center rounded text-slate-300 transition-colors hover:bg-slate-700 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            aria-label="Close model details"
          >
            <X aria-hidden="true" size={20} />
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
                  {healthy ? (
                    <CheckCircle size={20} className="text-green-400" />
                  ) : (
                    <XCircle size={20} className="text-red-400" />
                  )}
                  <span className="text-sm font-medium text-slate-300">Health Status</span>
                </div>
                <p className={`text-lg font-semibold ${healthy ? 'text-green-400' : 'text-red-400'}`}>
                  {healthy ? 'Healthy' : 'Unhealthy'}
                </p>
                {data?.health?.consecutive_fails ? (
                  <p className="text-xs text-slate-400 mt-1">
                    {data.health.consecutive_fails} consecutive failures
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
                <p className="text-xs text-slate-400 mt-1">
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
                <p className="text-xs text-slate-400 mt-1">
                  {data?.metrics?.active_requests ?? 0} active requests
                </p>
              </div>
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              {/* Token Stats */}
              <div className="rounded-lg bg-slate-700/50 p-4">
                <div className="mb-3 flex items-center gap-2">
                  <Zap size={20} className="text-purple-400" />
                  <span className="text-sm font-medium text-slate-300">Token usage</span>
                </div>
                <div className="grid grid-cols-3 gap-2 text-center sm:gap-4">
                  <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {formatTokenCount(data?.metrics?.total_tokens_in ?? 0)}
                  </p>
                    <p className="text-xs text-blue-200">Input tokens</p>
                  </div>
                  <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {formatTokenCount(data?.metrics?.total_tokens_out ?? 0)}
                  </p>
                    <p className="text-xs text-violet-200">Output tokens</p>
                  </div>
                  <div>
                  <p className="text-2xl font-semibold text-slate-100">
                    {(data?.metrics?.tokens_per_second ?? 0).toFixed(1)}
                  </p>
                    <p className="text-xs text-slate-400">Tokens/sec</p>
                  </div>
                </div>
              </div>

              {/* Model pricing */}
              <div className="rounded-lg border border-emerald-800/50 bg-emerald-950/20 p-4">
                <div className="mb-3 flex items-center gap-2">
                  <CircleDollarSign size={20} className="text-emerald-400" />
                  <span className="text-sm font-medium text-slate-300">Provider payout / 1M tokens</span>
                  {data?.price?.tier && (
                    <span className="ml-auto rounded-full border border-slate-600 px-2 py-0.5 text-[10px] uppercase tracking-wide text-slate-400">
                      {data.price.tier}
                    </span>
                  )}
                </div>
                {data?.price ? (
                  <>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <p className="text-xl font-semibold text-blue-100">{formatPrice(data.price.provider_input_price)}</p>
                        <p className="text-xs text-blue-300">Input</p>
                      </div>
                      <div>
                        <p className="text-xl font-semibold text-violet-100">{formatPrice(data.price.provider_output_price)}</p>
                        <p className="text-xs text-violet-300">Output</p>
                      </div>
                    </div>
                    {estimatedPayout !== null && (
                      <p className="mt-3 border-t border-emerald-900/60 pt-2 text-xs text-slate-400">
                        Estimated payout for recorded tokens: <span className="font-medium text-emerald-300">{formatPrice(estimatedPayout)}</span>
                      </p>
                    )}
                  </>
                ) : (
                  <p className="text-sm text-slate-400">Current catalog price is unavailable.</p>
                )}
              </div>
            </div>

            {/* Model transactions */}
            <div className="bg-slate-700/50 rounded-lg p-4">
              <h4 className="text-sm font-medium text-slate-200">Transactions for this model</h4>
              <p className="mb-3 mt-1 text-xs text-slate-400">Latest 20 local requests, with input and output tokens shown separately.</p>
              {(data?.recent_requests ?? []).length === 0 ? (
                <p className="text-slate-400 text-center py-4">No transactions recorded for this model</p>
              ) : (
                <>
                <div className="space-y-2 sm:hidden">
                  {(data?.recent_requests ?? []).map((req: RequestLog) => (
                    <div key={req.request_id} className="rounded-lg border border-slate-600 bg-slate-800/60 p-3">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <p className="truncate font-mono text-xs text-slate-300" title={req.request_id}>{req.request_id}</p>
                          <p className="mt-1 text-xs text-slate-400">{formatTime(req.start_time)} · {formatLatency(req.latency_ms)}</p>
                        </div>
                        {req.success ? <CheckCircle size={16} className="shrink-0 text-green-400" /> : <XCircle size={16} className="shrink-0 text-red-400" />}
                      </div>
                      <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
                        <div className="rounded bg-blue-950/30 px-2 py-1.5 text-blue-200">Input <span className="float-right font-mono">{req.tokens_in.toLocaleString()}</span></div>
                        <div className="rounded bg-violet-950/30 px-2 py-1.5 text-violet-200">Output <span className="float-right font-mono">{req.tokens_out.toLocaleString()}</span></div>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="hidden overflow-x-auto sm:block">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="text-slate-400 border-b border-slate-600">
                        <th className="text-left py-2 px-2 font-medium">Transaction</th>
                        <th className="text-left py-2 px-2 font-medium">Time</th>
                        <th className="text-right py-2 px-2 font-medium">Latency</th>
                        <th className="text-right py-2 px-2 font-medium">Input</th>
                        <th className="text-right py-2 px-2 font-medium">Output</th>
                        <th className="text-center py-2 px-2 font-medium">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(data?.recent_requests ?? []).map((req: RequestLog) => (
                        <tr key={req.request_id} className="border-b border-slate-600/50">
                          <td className="max-w-36 truncate px-2 py-2 font-mono text-xs text-slate-400" title={req.request_id}>{req.request_id}</td>
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
                          <td className="py-2 px-2 text-right text-blue-200 font-mono text-xs">
                            {req.tokens_in.toLocaleString()}
                          </td>
                          <td className="py-2 px-2 text-right text-violet-200 font-mono text-xs">
                            {req.tokens_out.toLocaleString()}
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
                </>
              )}
            </div>

            {/* Latency Chart */}
            {latencyData.length > 1 && (
              <div className="bg-slate-700/50 rounded-lg p-4">
                <h4 className="text-sm font-medium text-slate-300 mb-3">Recent transaction latency</h4>
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

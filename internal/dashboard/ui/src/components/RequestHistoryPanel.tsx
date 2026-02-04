import { useState, useCallback } from 'react';
import { CheckCircle, XCircle, Clock, ChevronDown, ChevronUp, RefreshCw } from 'lucide-react';
import { usePolling } from '../hooks/usePolling';
import { api } from '../api/client';
import type { ModelStatus } from '../types';

interface RequestHistoryPanelProps {
  models: ModelStatus[];
}

export function RequestHistoryPanel({ models }: RequestHistoryPanelProps) {
  const [selectedModel, setSelectedModel] = useState<string>('');
  const [expandedRow, setExpandedRow] = useState<string | null>(null);

  const {
    data: historyData,
    loading,
    refetch,
  } = usePolling(
    useCallback(() => api.getRequestHistory(50, selectedModel || undefined), [selectedModel]),
    10000
  );

  const requests = historyData?.requests ?? [];

  const formatTime = (timeStr: string) => {
    if (!timeStr) return '-';
    const date = new Date(timeStr);
    return date.toLocaleTimeString();
  };

  const formatLatency = (ms: number) => {
    if (ms < 1000) return `${ms.toFixed(0)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  };

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-slate-200">Request History</h3>
        <div className="flex items-center gap-2">
          <select
            value={selectedModel}
            onChange={(e) => setSelectedModel(e.target.value)}
            className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded text-sm text-slate-200 focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="">All Models</option>
            {models.map((model) => (
              <option key={model.id} value={model.id}>
                {model.id}
              </option>
            ))}
          </select>
          <button
            onClick={refetch}
            className="p-2 text-slate-400 hover:text-slate-200 hover:bg-slate-700 rounded transition-colors"
            title="Refresh"
          >
            <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      {loading && requests.length === 0 ? (
        <div className="animate-pulse space-y-2">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-10 bg-slate-700 rounded"></div>
          ))}
        </div>
      ) : requests.length === 0 ? (
        <div className="text-center py-8 text-slate-500">
          <Clock size={32} className="mx-auto mb-2 opacity-50" />
          <p>No requests recorded yet</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-slate-400 border-b border-slate-700">
                <th className="text-left py-2 px-2 font-medium">Time</th>
                <th className="text-left py-2 px-2 font-medium">Model</th>
                <th className="text-right py-2 px-2 font-medium">Latency</th>
                <th className="text-right py-2 px-2 font-medium">Tokens</th>
                <th className="text-center py-2 px-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((req) => (
                <>
                  <tr
                    key={req.request_id}
                    className={`border-b border-slate-700/50 cursor-pointer hover:bg-slate-700/30 transition-colors ${
                      !req.success ? 'bg-red-900/10' : ''
                    }`}
                    onClick={() => setExpandedRow(expandedRow === req.request_id ? null : req.request_id)}
                  >
                    <td className="py-2 px-2 text-slate-300">
                      <div className="flex items-center gap-1">
                        {expandedRow === req.request_id ? (
                          <ChevronUp size={14} className="text-slate-500" />
                        ) : (
                          <ChevronDown size={14} className="text-slate-500" />
                        )}
                        {formatTime(req.start_time)}
                      </div>
                    </td>
                    <td className="py-2 px-2">
                      <span className="text-slate-200 font-mono text-xs">{req.model}</span>
                      {req.streaming && (
                        <span className="ml-1 text-xs text-blue-400">(stream)</span>
                      )}
                    </td>
                    <td className="py-2 px-2 text-right">
                      <span
                        className={`font-mono ${
                          req.latency_ms > 5000
                            ? 'text-red-400'
                            : req.latency_ms > 2000
                            ? 'text-yellow-400'
                            : 'text-green-400'
                        }`}
                      >
                        {formatLatency(req.latency_ms)}
                      </span>
                    </td>
                    <td className="py-2 px-2 text-right text-slate-300 font-mono text-xs">
                      {req.tokens_in}/{req.tokens_out}
                    </td>
                    <td className="py-2 px-2 text-center">
                      {req.success ? (
                        <CheckCircle size={16} className="inline text-green-400" />
                      ) : (
                        <XCircle size={16} className="inline text-red-400" />
                      )}
                    </td>
                  </tr>
                  {expandedRow === req.request_id && (
                    <tr key={`${req.request_id}-details`} className="bg-slate-700/20">
                      <td colSpan={5} className="py-3 px-4">
                        <div className="grid grid-cols-2 gap-4 text-xs">
                          <div>
                            <span className="text-slate-500">Request ID:</span>
                            <span className="ml-2 text-slate-300 font-mono">{req.request_id}</span>
                          </div>
                          <div>
                            <span className="text-slate-500">End Time:</span>
                            <span className="ml-2 text-slate-300">{formatTime(req.end_time)}</span>
                          </div>
                          <div>
                            <span className="text-slate-500">Input Tokens:</span>
                            <span className="ml-2 text-slate-300">{req.tokens_in}</span>
                          </div>
                          <div>
                            <span className="text-slate-500">Output Tokens:</span>
                            <span className="ml-2 text-slate-300">{req.tokens_out}</span>
                          </div>
                          {req.error_reason && (
                            <div className="col-span-2">
                              <span className="text-slate-500">Error:</span>
                              <span className="ml-2 text-red-400">{req.error_reason}</span>
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="mt-3 text-xs text-slate-500 text-center">
        Showing last {requests.length} requests {selectedModel && `for ${selectedModel}`}
      </div>
    </div>
  );
}

import { Fragment, useCallback, useState } from 'react';
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  CheckCircle,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Clock,
  RefreshCw,
  XCircle,
} from 'lucide-react';
import { usePolling } from '../hooks/usePolling';
import { api } from '../api/client';
import type { ModelStatus, RequestLog, RequestSource } from '../types';

interface RequestHistoryPanelProps {
  models: ModelStatus[];
}

function formatStarted(timeStr: string, includeDate = false) {
  if (!timeStr) return '—';
  const date = new Date(timeStr);
  return includeDate
    ? date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' })
    : date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: '2-digit' });
}

function formatLatency(ms: number) {
  if (ms < 1000) return `${ms.toFixed(0)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function latencyClass(ms: number) {
  if (ms > 5000) return 'text-red-300';
  if (ms > 2000) return 'text-amber-300';
  return 'text-emerald-300';
}

// Traffic this node generated to check itself looks identical to routed work in
// every other column, so an operator seeing probe requests against their GPUs
// had no way to tell why. The label says where a request came from, not who
// originated it: a hub request carries no marker distinguishing customer
// traffic from the marketplace's own verification, and a provider client should
// not be guessing at that.
const SOURCE_LABELS: Record<string, { label: string; title: string; className: string }> = {
  hub: {
    label: 'Hub',
    title: 'Routed to this node by Swan Inference',
    className: 'bg-blue-500/10 text-blue-300 ring-blue-500/30',
  },
  health: {
    label: 'Health',
    title: "This node's own engine probe: a one-token completion checking the backend can serve",
    className: 'bg-slate-500/10 text-slate-400 ring-slate-500/30',
  },
  selfcheck: {
    label: 'Self-check',
    title: "This node's periodic audit probe",
    className: 'bg-slate-500/10 text-slate-400 ring-slate-500/30',
  },
};

const PAGE_SIZES = [25, 50, 100];

function SourceBadge({ source }: { source?: string }) {
  // Records written before this field existed all came over the WebSocket,
  // which was the only path that recorded anything.
  const meta = SOURCE_LABELS[source ?? 'hub'] ?? SOURCE_LABELS.hub;
  return (
    <span title={meta.title} className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${meta.className}`}>
      {meta.label}
    </span>
  );
}

function StatusBadge({ success }: { success: boolean }) {
  return success ? (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-800/70 bg-emerald-950/40 px-2 py-1 text-xs font-medium text-emerald-300">
      <CheckCircle aria-hidden="true" size={13} /> Success
    </span>
  ) : (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-red-800/70 bg-red-950/40 px-2 py-1 text-xs font-medium text-red-300">
      <XCircle aria-hidden="true" size={13} /> Failed
    </span>
  );
}

function RequestReceipt({ request }: { request: RequestLog }) {
  return (
    <div className="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
      <div>
        <span className="block text-slate-500">Request ID</span>
        <span className="mt-1 block break-all font-mono text-slate-300">{request.request_id}</span>
      </div>
      <div>
        <span className="block text-slate-500">Completed</span>
        <span className="mt-1 block text-slate-300">{formatStarted(request.end_time, true)}</span>
      </div>
      <div>
        <span className="block text-slate-500">Total tokens</span>
        <span className="mt-1 block font-mono text-slate-300">{(request.tokens_in + request.tokens_out).toLocaleString()}</span>
      </div>
      <div>
        <span className="block text-slate-500">Delivery</span>
        <span className="mt-1 block text-slate-300">{request.streaming ? 'Streaming' : 'Single response'}</span>
      </div>
      {request.error_reason && (
        <div className="sm:col-span-2 lg:col-span-4">
          <span className="block text-slate-500">Error</span>
          <span className="mt-1 block break-words text-red-300">{request.error_reason}</span>
        </div>
      )}
    </div>
  );
}

export function RequestHistoryPanel({ models }: RequestHistoryPanelProps) {
  const [selectedModel, setSelectedModel] = useState('');
  const [selectedSource, setSelectedSource] = useState<RequestSource | ''>('');
  const [pageSize, setPageSize] = useState(PAGE_SIZES[0]);
  const [page, setPage] = useState(0);
  const [expandedRow, setExpandedRow] = useState<string | null>(null);

  // Changing what is being looked at always returns to the first page —
  // staying on page 4 of a filter that now matches six rows shows nothing.
  const applyFilter = (apply: () => void) => {
    apply();
    setPage(0);
    setExpandedRow(null);
  };

  const {
    data: historyData,
    error,
    loading,
    refetch,
  } = usePolling(
    useCallback(
      () => api.getRequestHistory({
        limit: pageSize,
        offset: page * pageSize,
        model: selectedModel || undefined,
        source: selectedSource || undefined,
      }),
      [pageSize, page, selectedModel, selectedSource],
    ),
    // Auto-refresh only on the newest page. Further back, an arriving request
    // would shift every row down by one and the reader would lose their place.
    page === 0 ? 10000 : 0,
  );

  const requests = historyData?.requests ?? [];
  const total = historyData?.total ?? 0;
  const inputTotal = requests.reduce((sum, request) => sum + request.tokens_in, 0);
  const outputTotal = requests.reduce((sum, request) => sum + request.tokens_out, 0);
  const toggleExpanded = (requestID: string) => setExpandedRow((current) => current === requestID ? null : requestID);

  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const firstShown = total === 0 ? 0 : page * pageSize + 1;
  const lastShown = page * pageSize + requests.length;
  const canPrev = page > 0;
  const canNext = lastShown < total;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-white">Transactions</h1>
          <p className="mt-1 text-sm text-slate-400">The latest inference requests, with input and output usage shown separately for every transaction.</p>
        </div>
        <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
          <label htmlFor="transaction-model-filter" className="sr-only">Filter transactions by model</label>
          <select
            id="transaction-model-filter"
            value={selectedModel}
            onChange={(event) => applyFilter(() => setSelectedModel(event.target.value))}
            className="min-h-10 min-w-0 flex-1 rounded-lg border border-slate-700 bg-slate-900 px-3 text-sm text-slate-200 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 sm:min-w-56"
          >
            <option value="">All models</option>
            {models.map((model) => <option key={model.id} value={model.id}>{model.id}</option>)}
          </select>
          <label htmlFor="transaction-source-filter" className="sr-only">Filter transactions by source</label>
          <select
            id="transaction-source-filter"
            value={selectedSource}
            onChange={(event) => applyFilter(() => setSelectedSource(event.target.value as RequestSource | ''))}
            className="min-h-10 min-w-0 rounded-lg border border-slate-700 bg-slate-900 px-3 text-sm text-slate-200 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          >
            <option value="">All sources</option>
            {Object.entries(SOURCE_LABELS).map(([value, meta]) => (
              <option key={value} value={value}>{meta.label}</option>
            ))}
          </select>
          <button
            type="button"
            onClick={refetch}
            className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg border border-slate-700 bg-slate-900 text-slate-300 hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
            aria-label="Refresh transactions"
          >
            <RefreshCw aria-hidden="true" size={16} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div className="rounded-xl border border-slate-800 bg-slate-900 p-3 sm:p-4">
          <p className="text-xs text-slate-500">Matching</p>
          <p className="mt-1 text-lg font-semibold text-white sm:text-xl">{total.toLocaleString()}</p>
          <p className="mt-1 hidden text-xs text-slate-500 sm:block">
            {selectedModel || selectedSource ? 'transactions match the filters' : 'transactions in history'}
          </p>
        </div>
        <div className="rounded-xl border border-blue-900/70 bg-blue-950/20 p-3 sm:p-4">
          <p className="flex items-center gap-1 text-xs text-blue-300"><ArrowDownToLine aria-hidden="true" size={13} /> Input tokens</p>
          <p className="mt-1 text-lg font-semibold text-white sm:text-xl">{inputTotal.toLocaleString()}</p>
          <p className="mt-1 hidden text-xs text-slate-500 sm:block">across rows shown</p>
        </div>
        <div className="rounded-xl border border-violet-900/70 bg-violet-950/20 p-3 sm:p-4">
          <p className="flex items-center gap-1 text-xs text-violet-300"><ArrowUpFromLine aria-hidden="true" size={13} /> Output tokens</p>
          <p className="mt-1 text-lg font-semibold text-white sm:text-xl">{outputTotal.toLocaleString()}</p>
          <p className="mt-1 hidden text-xs text-slate-500 sm:block">across rows shown</p>
        </div>
      </div>

      <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900">
        {loading && requests.length === 0 ? (
          <div className="animate-pulse space-y-3 p-4" role="status" aria-label="Loading transactions">
            {[...Array(6)].map((_, index) => <div key={index} className="h-14 rounded-lg bg-slate-800" />)}
          </div>
        ) : error && requests.length === 0 ? (
          <div className="px-4 py-12 text-center">
            <XCircle aria-hidden="true" size={32} className="mx-auto mb-3 text-red-400" />
            <p className="font-medium text-red-200">Transactions are unavailable</p>
            <p className="mt-1 text-sm text-slate-400">{error.message}</p>
            <button type="button" onClick={refetch} className="mt-4 rounded-lg bg-slate-800 px-4 py-2 text-sm text-white">Try again</button>
          </div>
        ) : requests.length === 0 ? (
          <div className="px-4 py-12 text-center text-slate-400">
            <Clock aria-hidden="true" size={32} className="mx-auto mb-3 text-slate-600" />
            {selectedModel || selectedSource ? (
              <>
                <p className="font-medium text-slate-300">No transactions match these filters</p>
                <p className="mt-1 text-sm">Nothing recorded for {selectedSource ? `${SOURCE_LABELS[selectedSource].label.toLowerCase()} traffic` : 'this source'}{selectedModel ? ` on ${selectedModel}` : ''} yet.</p>
                <button
                  type="button"
                  onClick={() => applyFilter(() => { setSelectedModel(''); setSelectedSource(''); })}
                  className="mt-4 rounded-lg bg-slate-800 px-4 py-2 text-sm text-white hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  Clear filters
                </button>
              </>
            ) : (
              <>
                <p className="font-medium text-slate-300">No transactions yet</p>
                <p className="mt-1 text-sm">Requests will appear here after the provider serves inference.</p>
              </>
            )}
          </div>
        ) : (
          <>
            <div className="hidden overflow-x-auto md:block">
              <table className="w-full min-w-[840px] text-sm">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-950/40 text-xs uppercase tracking-wide text-slate-500">
                    <th className="px-4 py-3 text-left font-medium">Started</th>
                    <th className="px-4 py-3 text-left font-medium">Model</th>
                    <th className="px-4 py-3 text-left font-medium">Source</th>
                    <th className="px-4 py-3 text-right font-medium">Latency</th>
                    <th className="px-4 py-3 text-right font-medium"><span className="inline-flex items-center gap-1"><ArrowDownToLine aria-hidden="true" size={13} /> Input tokens</span></th>
                    <th className="px-4 py-3 text-right font-medium"><span className="inline-flex items-center gap-1"><ArrowUpFromLine aria-hidden="true" size={13} /> Output tokens</span></th>
                    <th className="px-4 py-3 text-right font-medium">Status</th>
                    <th className="w-12 px-3 py-3"><span className="sr-only">Details</span></th>
                  </tr>
                </thead>
                <tbody>
                  {requests.map((request) => {
                    const expanded = expandedRow === request.request_id;
                    return (
                      <Fragment key={request.request_id}>
                        <tr className={`border-b border-slate-800/80 ${request.success ? 'hover:bg-slate-800/35' : 'bg-red-950/10 hover:bg-red-950/20'}`}>
                          <td className="whitespace-nowrap px-4 py-3 text-slate-300" title={new Date(request.start_time).toLocaleString()}>{formatStarted(request.start_time)}</td>
                          <td className="max-w-xs px-4 py-3"><span className="block truncate font-mono text-xs text-slate-200" title={request.model}>{request.model}</span>{request.streaming && <span className="mt-0.5 block text-xs text-blue-300">Streaming</span>}</td>
                          <td className="whitespace-nowrap px-4 py-3"><SourceBadge source={request.source} /></td>
                            <td className={`whitespace-nowrap px-4 py-3 text-right font-mono text-xs ${latencyClass(request.latency_ms)}`}>{formatLatency(request.latency_ms)}</td>
                          <td className="whitespace-nowrap px-4 py-3 text-right font-mono text-sm text-blue-200">{request.tokens_in.toLocaleString()}</td>
                          <td className="whitespace-nowrap px-4 py-3 text-right font-mono text-sm text-violet-200">{request.tokens_out.toLocaleString()}</td>
                          <td className="px-4 py-3 text-right"><StatusBadge success={request.success} /></td>
                          <td className="px-3 py-3 text-right">
                            <button type="button" onClick={() => toggleExpanded(request.request_id)} aria-expanded={expanded} aria-controls={`receipt-${request.request_id}`} className="rounded-lg p-2 text-slate-400 hover:bg-slate-700 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500" aria-label={`${expanded ? 'Hide' : 'Show'} details for request ${request.request_id}`}>
                              {expanded ? <ChevronUp aria-hidden="true" size={16} /> : <ChevronDown aria-hidden="true" size={16} />}
                            </button>
                          </td>
                        </tr>
                        {expanded && (
                          <tr id={`receipt-${request.request_id}`} className="border-b border-slate-800 bg-slate-950/60">
                            <td colSpan={8} className="px-4 py-4"><RequestReceipt request={request} /></td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className="divide-y divide-slate-800 md:hidden">
              {requests.map((request) => {
                const expanded = expandedRow === request.request_id;
                return (
                  <article key={request.request_id} className={request.success ? '' : 'bg-red-950/10'}>
                    <button type="button" onClick={() => toggleExpanded(request.request_id)} aria-expanded={expanded} aria-controls={`mobile-receipt-${request.request_id}`} className="w-full p-4 text-left focus:outline-none focus:ring-2 focus:ring-inset focus:ring-blue-500">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0"><p className="truncate font-mono text-sm text-white">{request.model}</p><p className="mt-1 text-xs text-slate-500">{formatStarted(request.start_time, true)}{request.streaming ? ' · Streaming' : ''}</p></div>
                        <span className="mt-0.5 flex-shrink-0 text-slate-500">{expanded ? <ChevronUp aria-hidden="true" size={18} /> : <ChevronDown aria-hidden="true" size={18} />}</span>
                      </div>
                      <div className="mt-3 grid grid-cols-3 gap-2">
                        <div><span className="block text-[11px] text-slate-500">Latency</span><span className={`mt-0.5 block font-mono text-xs ${latencyClass(request.latency_ms)}`}>{formatLatency(request.latency_ms)}</span></div>
                        <div><span className="flex items-center gap-1 text-[11px] text-blue-300"><ArrowDownToLine aria-hidden="true" size={11} /> Input</span><span className="mt-0.5 block font-mono text-sm text-blue-100">{request.tokens_in.toLocaleString()}</span></div>
                        <div><span className="flex items-center gap-1 text-[11px] text-violet-300"><ArrowUpFromLine aria-hidden="true" size={11} /> Output</span><span className="mt-0.5 block font-mono text-sm text-violet-100">{request.tokens_out.toLocaleString()}</span></div>
                      </div>
                      <div className="mt-3"><StatusBadge success={request.success} /></div>
                    </button>
                    {expanded && <div id={`mobile-receipt-${request.request_id}`} className="border-t border-slate-800 bg-slate-950/60 p-4"><RequestReceipt request={request} /></div>}
                  </article>
                );
              })}
            </div>
          </>
        )}
      </div>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-xs text-slate-500" aria-live="polite">
          {total === 0
            ? 'No transactions to show.'
            : `Showing ${firstShown.toLocaleString()}\u2013${lastShown.toLocaleString()} of ${total.toLocaleString()}`}
          {selectedModel ? ` for ${selectedModel}` : ''}
          {selectedSource ? ` from ${SOURCE_LABELS[selectedSource].label.toLowerCase()}` : ''}
          {'. '}
          {page === 0
            ? 'Auto-refreshes every 10 seconds.'
            : 'Auto-refresh is paused while viewing older pages.'}
        </p>

        <div className="flex items-center gap-2">
          <label htmlFor="transaction-page-size" className="text-xs text-slate-500">Per page</label>
          <select
            id="transaction-page-size"
            value={pageSize}
            onChange={(event) => applyFilter(() => setPageSize(Number(event.target.value)))}
            className="min-h-9 rounded-lg border border-slate-700 bg-slate-900 px-2 text-xs text-slate-200 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          >
            {PAGE_SIZES.map((size) => <option key={size} value={size}>{size}</option>)}
          </select>

          <div className="ml-1 flex items-center gap-1">
            <button
              type="button"
              onClick={() => { setPage((current) => Math.max(0, current - 1)); setExpandedRow(null); }}
              disabled={!canPrev}
              className="inline-flex min-h-9 min-w-9 items-center justify-center rounded-lg border border-slate-700 bg-slate-900 text-slate-300 hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-40 focus:outline-none focus:ring-2 focus:ring-blue-500"
              aria-label="Previous page"
            >
              <ChevronLeft aria-hidden="true" size={16} />
            </button>
            <span className="px-2 text-xs tabular-nums text-slate-400">{page + 1} / {pageCount}</span>
            <button
              type="button"
              onClick={() => { setPage((current) => current + 1); setExpandedRow(null); }}
              disabled={!canNext}
              className="inline-flex min-h-9 min-w-9 items-center justify-center rounded-lg border border-slate-700 bg-slate-900 text-slate-300 hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-40 focus:outline-none focus:ring-2 focus:ring-blue-500"
              aria-label="Next page"
            >
              <ChevronRight aria-hidden="true" size={16} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

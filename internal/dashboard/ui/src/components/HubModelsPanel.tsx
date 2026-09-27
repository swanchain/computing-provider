import { useCallback, useState } from 'react';
import { AlertTriangle, ChevronDown, ChevronUp } from 'lucide-react';
import { api } from '../api/client';
import { usePolling } from '../hooks/usePolling';
import type { HubModel } from '../types';

/**
 * Swan Inference's own record of this provider's models.
 *
 * The node's model panel says what this node is serving. This one says what
 * the platform believes it is serving, and the two disagree in ways that cost
 * money without any local symptom: a model the node registers that the
 * platform does not list can still be sent requests and served, yet have no
 * offering to credit the work to, and a failure that happened before a
 * request was billed is visible only on the platform's side.
 */

const HUB_POLL_MS = 120_000; // the backend caches the platform's answer for two minutes

function formatCount(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

function formatAgo(iso?: string) {
  if (!iso) return 'never';
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 90) return 'just now';
  if (seconds < 5400) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 172_800) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86_400)}d ago`;
}

/**
 * Recent outcomes as counts, not a rate: 3 of 4 and 3,000 of 4,000 are the same
 * percentage and not the same evidence. Dispatch failures are shown beside
 * them because they are the part of the window the graded counts cannot see.
 */
function Outcomes({ m }: { m: HubModel }) {
  const total = m.recent_success_count + m.recent_failure_count;
  if (total === 0 && !m.dispatch_failures) return <span className="text-slate-500">no recent traffic</span>;
  const failing = m.recent_failure_count > 0 && m.recent_failure_count * 10 >= total;
  return (
    <span className={failing ? 'text-amber-300' : 'text-slate-300'}>
      {failing ? '⚠ ' : '✓ '}
      {formatCount(m.recent_success_count)} ok / {formatCount(m.recent_failure_count)} failed
      {(m.dispatch_failures ?? 0) > 0 && (
        <span className="text-slate-400"> · {formatCount(m.dispatch_failures ?? 0)} undelivered</span>
      )}
    </span>
  );
}

function Status({ m }: { m: HubModel }) {
  if (m.offered && m.registered_locally) {
    return <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[11px] text-emerald-300">● offered</span>;
  }
  if (m.offered) {
    // Held as offered by the platform, but not in this node's register list —
    // another machine on the same key, or a stale offering.
    return <span className="rounded bg-sky-500/15 px-1.5 py-0.5 text-[11px] text-sky-300">◐ offered elsewhere</span>;
  }
  return <span className="rounded bg-slate-700/60 px-1.5 py-0.5 text-[11px] text-slate-400">○ history</span>;
}

export function HubModelsPanel() {
  const [showHistory, setShowHistory] = useState(false);
  const { data, loading, error } = usePolling(useCallback(() => api.getHubModels(), []), HUB_POLL_MS);

  const models = data?.models ?? [];
  const offered = models.filter((m) => m.offered);
  const history = models.filter((m) => !m.offered);
  const shown = showHistory ? models : offered;
  const notListed = data?.not_listed ?? [];

  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="border-b border-slate-800 px-4 py-3">
        <h3 className="text-sm font-medium text-slate-300">Swan Inference’s view</h3>
        <p className="text-xs text-slate-400">
          What the platform holds for this provider and how each offering has served recently.
        </p>
      </div>

      {notListed.length > 0 && (
        <div role="alert" className="flex items-start gap-2 border-b border-slate-800 bg-amber-500/10 px-4 py-3 text-xs text-amber-200">
          <AlertTriangle aria-hidden="true" size={14} className="mt-px shrink-0" />
          <span>
            Swan Inference does not list {notListed.length === 1 ? 'this model' : 'these models'} as offered by this
            provider, though this node registers {notListed.length === 1 ? 'it' : 'them'}:{' '}
            <span className="break-all font-mono">{notListed.join(', ')}</span>. Requests can still arrive and be
            served, but with no offering the work may not be credited — compare the earnings chart.
          </span>
        </div>
      )}

      {loading && !data ? (
        <p className="px-4 py-6 text-sm text-slate-400">Loading…</p>
      ) : error && !data ? (
        <p className="px-4 py-6 text-sm text-amber-300">Could not load the platform’s view: {error.message}</p>
      ) : data?.error ? (
        <p className="px-4 py-6 text-sm text-amber-300">Swan Inference could not be reached: {data.error}</p>
      ) : shown.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">The platform lists no offered models for this provider.</p>
      ) : (
        <ul className="divide-y divide-slate-800">
          {shown.map((m) => (
            <li key={m.model_id} className="px-4 py-3 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="min-w-0 break-all font-mono text-sm text-slate-200">{m.model_id}</span>
                <Status m={m} />
              </div>
              <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-slate-400">
                <Outcomes m={m} />
                <span>
                  {m.context_length > 0 ? `${formatCount(m.context_length)} ctx` : 'ctx unknown'}
                  {m.context_source && <span className="text-slate-500"> ({m.context_source})</span>}
                </span>
                {m.throughput_tok_per_s > 0 && <span>{m.throughput_tok_per_s.toFixed(1)} tok/s</span>}
                <span>last request {formatAgo(m.last_request_at)}</span>
              </div>
            </li>
          ))}
        </ul>
      )}

      {history.length > 0 && !data?.error && (
        <button
          type="button"
          onClick={() => setShowHistory((v) => !v)}
          aria-expanded={showHistory}
          className="flex w-full items-center justify-center gap-1.5 border-t border-slate-800 px-4 py-2 text-xs text-slate-400 transition hover:bg-slate-800/60 hover:text-slate-200 focus:outline-none focus:ring-2 focus:ring-inset focus:ring-blue-500"
        >
          {showHistory ? <ChevronUp aria-hidden="true" size={14} /> : <ChevronDown aria-hidden="true" size={14} />}
          {showHistory ? 'Hide' : 'Show'} {history.length} model{history.length === 1 ? '' : 's'} no longer offered
        </button>
      )}
    </div>
  );
}

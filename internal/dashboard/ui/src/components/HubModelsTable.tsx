import { useState } from 'react';
import { Power, SlidersHorizontal } from 'lucide-react';
import { api } from '../api/client';
import type { ConcurrencyMetrics, HubModel, HubModelView, ModelStatus, RegistrationDrops } from '../types';
import { LimitsEditor } from './ModelsPanel';

/**
 * One row per model this node offers to the hub: what the operator looks at
 * every day, on one screen. Each column answers a question the old overview
 * answered somewhere else — the limits line, the platform listings panel, the
 * earnings chart's legend, the registration-drop badge — and the row is where
 * those answers are acted on. Local-only models are not rows: nothing routes
 * to them but this machine's own clients, and they live on Diagnostics.
 */

interface Props {
  models: ModelStatus[];
  hub?: HubModelView | null;
  hubError?: Error | null;
  capacity?: Record<string, number>;
  concurrency?: ConcurrencyMetrics;
  drops?: Record<string, RegistrationDrops>;
  /** Models the node pulled off the hub itself, with the reason. */
  autoDisabled?: Record<string, string>;
  /** Earnings in the chart's window, per model, in USD; absent until loaded. */
  earned?: Record<string, number>;
  earnedWindow: string;
  loading: boolean;
  error?: Error | null;
  authenticated: boolean;
  onUnlock: () => void;
  onRefresh: () => void;
  onModelClick?: (modelId: string) => void;
  onShowLocal: () => void;
}

const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 4 });

function formatCount(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

function formatAgo(iso: string) {
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 90) return 'just now';
  if (seconds < 5400) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 172_800) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86_400)}d ago`;
}

function backendLabel(endpoint: string) {
  try {
    const u = new URL(endpoint);
    return u.port ? `:${u.port}` : u.host;
  } catch {
    return endpoint;
  }
}

/** What the platform says about the model, in one cell. */
function HubCell({ model, hubModel, hubKnown, notListed, drop, pulledReason }: {
  model: ModelStatus; hubModel?: HubModel; hubKnown: boolean; notListed: boolean; drop?: RegistrationDrops; pulledReason?: string;
}) {
  if (pulledReason) {
    return <span className="text-rose-300" title={pulledReason}>○ pulled by this node</span>;
  }
  if (!model.enabled) return <span className="text-slate-500">○ disabled</span>;
  if (!hubKnown) return <span className="text-slate-500" title="The platform's listings could not be read">—</span>;
  if (hubModel?.offered) {
    return (
      <span className="text-emerald-300">
        ● offered
        {drop && <span className="ml-1 text-amber-300" title={`Last left the hub registration ${formatAgo(drop.last_at)}: ${drop.last_reason}`}>· dropped {drop.count}×</span>}
      </span>
    );
  }
  if (notListed) return <span className="text-amber-300" title="Registered by this node but not held as offered by the platform; work served for it may not be credited">⚠ not listed</span>;
  return <span className="text-slate-500">○ not offered</span>;
}

function RecentCell({ hubModel }: { hubModel?: HubModel }) {
  if (!hubModel) return <span className="text-slate-500">—</span>;
  const total = hubModel.recent_success_count + hubModel.recent_failure_count;
  if (total === 0) return <span className="text-slate-500">none</span>;
  const failing = hubModel.recent_failure_count > 0 && hubModel.recent_failure_count * 10 >= total;
  return (
    <span className="tabular-nums">
      {formatCount(hubModel.recent_success_count)}
      {hubModel.recent_failure_count > 0 && (
        <span className={failing ? 'text-rose-300' : 'text-amber-300'}> · {formatCount(hubModel.recent_failure_count)} failed</span>
      )}
    </span>
  );
}

export function HubModelsTable({ models, hub, hubError, capacity, concurrency, drops, autoDisabled, earned, earnedWindow, loading, error, authenticated, onUnlock, onRefresh, onModelClick, onShowLocal }: Props) {
  const [editing, setEditing] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const [showQuiet, setShowQuiet] = useState(false);

  const hubByID = new Map<string, HubModel>((hub?.models ?? []).map((m) => [m.model_id, m]));
  const notListed = new Set(hub?.not_listed ?? []);
  const hubKnown = Boolean(hub) && !hub?.error;

  const rows = models.filter((m) => !m.local_only);
  const localCount = models.length - rows.length;
  const healthy = (m: ModelStatus) => m.enabled && m.health_string === 'healthy';
  // Rows the operator has to act on come first; a model the platform does
  // not list is flagged in its Hub cell but sorts with the others, by traffic.
  const needsLook = (m: ModelStatus) => !healthy(m) || Boolean(autoDisabled?.[m.id]);
  const recentTotal = (m: ModelStatus) => {
    const h = hubByID.get(m.id);
    return h ? h.recent_success_count + h.recent_failure_count : 0;
  };
  // A healthy model that served nothing recently is a line of noise on a page
  // read every morning; it folds into one row until asked for.
  const quiet = rows.filter((m) => !needsLook(m) && !notListed.has(m.id) && recentTotal(m) === 0 && !(earned?.[m.id]));
  const shown = showQuiet ? rows : rows.filter((m) => !quiet.includes(m));
  const ordered = [...shown].sort((a, b) => Number(needsLook(b)) - Number(needsLook(a)) || recentTotal(b) - recentTotal(a) || a.id.localeCompare(b.id));

  const toggle = async (model: ModelStatus) => {
    if (!authenticated) { onUnlock(); return; }
    setBusy(model.id);
    setActionError('');
    try {
      if (model.enabled) await api.disableModel(model.id); else await api.enableModel(model.id);
      onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Action failed');
    } finally {
      setBusy(null);
    }
  };

  const unhealthyCount = rows.filter((m) => m.enabled && !healthy(m)).length;

  return (
    <section aria-labelledby="hub-models-heading" className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-slate-800 px-4 py-3">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="hub-models-heading" className="text-base font-semibold text-white">Serving on the hub</h2>
          <span className="text-sm text-slate-400">
            {rows.length} model{rows.length === 1 ? '' : 's'}
            {unhealthyCount > 0 && <span className="text-rose-300"> · {unhealthyCount} unhealthy</span>}
            {hubError && <span className="text-amber-300"> · platform listings unavailable</span>}
          </span>
        </div>
        <div className="flex gap-4 text-sm">
          {localCount > 0 && (
            <button type="button" onClick={onShowLocal} className="min-h-10 text-slate-400 hover:text-slate-200 focus-visible:outline focus-visible:outline-sky-400">
              {localCount} local-only
            </button>
          )}
        </div>
      </div>

      {actionError && <p role="alert" className="mx-4 mt-3 rounded-lg border border-red-800/60 bg-red-950/30 px-3 py-2 text-sm text-red-300">{actionError}</p>}

      {rows.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">{loading ? 'Loading models…' : error ? 'API unreachable' : 'No models are offered to the hub. Add one to models.json or start one with `models serve`.'}</p>
      ) : (
        <>
          {/* Phone width: one card per model. A seven-column table cannot be
              read at 390 px, and a scrolling box for it still let the page
              itself scroll sideways. */}
          <ul className="divide-y divide-slate-800 md:hidden" aria-label="Models offered to the hub">
            {ordered.map((model) => {
              const hubModel = hubByID.get(model.id);
              const pulled = autoDisabled?.[model.id];
              const attention = needsLook(model);
              const active = concurrency?.per_model_active?.[model.id] ?? 0;
              const slots = capacity?.[model.id] ?? model.max_concurrent;
              const own = Boolean(model.max_concurrent && model.max_concurrent > 0);
              const amount = earned?.[model.id];
              return (
                <li key={model.id} className={`px-4 py-3 ${attention ? 'bg-rose-950/20' : ''}`}>
                  <div className="flex items-start justify-between gap-2">
                    <button type="button" onClick={() => onModelClick?.(model.id)} className="flex min-w-0 items-start gap-2.5 text-left focus-visible:outline focus-visible:outline-sky-400" aria-label={`View details for ${model.id}`}>
                      <span aria-hidden="true" className={`mt-1.5 h-2.5 w-2.5 flex-shrink-0 rounded-full ${!model.enabled ? 'bg-slate-500' : healthy(model) ? 'bg-emerald-400' : 'bg-rose-400'}`} />
                      <span className="min-w-0">
                        <span className="block break-all font-mono text-[13px] text-slate-100">{model.id}</span>
                        <span className={`block text-xs ${attention && model.enabled ? 'text-rose-300' : 'text-slate-500'}`}>
                          {!model.enabled ? 'disabled' : healthy(model) ? `${backendLabel(model.endpoint)} · ${model.category}` : `✗ ${model.state_string} · ${model.health_string}`}
                          {pulled && ` · ${pulled}`}
                        </span>
                      </span>
                    </button>
                    <div className="flex flex-shrink-0 flex-col">
                      <button type="button" onClick={() => (authenticated ? setEditing(editing === model.id ? null : model.id) : onUnlock())} className={`rounded p-2 ${editing === model.id ? 'text-blue-300' : 'text-slate-400'}`} title="Concurrency and rate limits" aria-label={`Edit limits for ${model.id}`} aria-expanded={editing === model.id}><SlidersHorizontal size={16} /></button>
                      <button type="button" onClick={() => toggle(model)} disabled={busy === model.id} className={`rounded p-2 disabled:opacity-50 ${model.enabled ? 'text-emerald-400' : 'text-slate-400'}`} title={model.enabled ? 'Disable model' : 'Enable model'} aria-label={`${model.enabled ? 'Disable' : 'Enable'} ${model.id}`}><Power size={16} /></button>
                    </div>
                  </div>
                  <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                    <div><dt className="text-slate-500">Hub</dt><dd><HubCell model={model} hubModel={hubModel} hubKnown={hubKnown} notListed={notListed.has(model.id)} drop={drops?.[model.id]} pulledReason={pulled} /></dd></div>
                    <div><dt className="text-slate-500">In flight · rate</dt><dd className="font-mono">{slots == null ? active : <>{active} / {slots}{!own && <span className="text-slate-500"> default</span>}</>} · {model.rpm && model.rpm > 0 ? `${model.rpm} rpm` : <span className="text-slate-500">global</span>}</dd></div>
                    <div><dt className="text-slate-500">Recent requests</dt><dd className="font-mono"><RecentCell hubModel={hubModel} /></dd></div>
                    <div><dt className="text-slate-500">Earned · {earnedWindow}</dt><dd className="font-mono">{amount == null ? <span className="text-slate-500">—</span> : amount === 0 ? <span className="text-slate-500">$0</span> : usd.format(amount)}</dd></div>
                  </dl>
                  {editing === model.id && <LimitsEditor model={model} onSaved={() => { setEditing(null); onRefresh(); }} onCancel={() => setEditing(null)} />}
                </li>
              );
            })}
            {quiet.length > 0 && (
              <li className="px-4 py-2 text-xs text-slate-400">
                <button type="button" onClick={() => setShowQuiet((v) => !v)} className="min-h-9 hover:text-slate-200 focus-visible:outline focus-visible:outline-sky-400" aria-expanded={showQuiet}>
                  {showQuiet ? 'Hide' : 'Show'} {quiet.length} healthy model{quiet.length === 1 ? '' : 's'} with no recent traffic
                </button>
              </li>
            )}
          </ul>

          <div className="hidden overflow-x-auto md:block">
          <table className="w-full min-w-[920px] border-collapse text-sm">
            <thead>
              <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                <th scope="col" className="px-4 py-2.5 font-medium">Model</th>
                <th scope="col" className="px-3 py-2.5 font-medium">Hub</th>
                <th scope="col" className="px-3 py-2.5 font-medium">In flight</th>
                <th scope="col" className="px-3 py-2.5 font-medium">Rate</th>
                <th scope="col" className="px-3 py-2.5 text-right font-medium">Recent requests</th>
                <th scope="col" className="px-3 py-2.5 text-right font-medium">Earned · {earnedWindow}</th>
                <th scope="col" className="px-4 py-2.5 text-right font-medium"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {ordered.map((model) => {
                const hubModel = hubByID.get(model.id);
                const pulled = autoDisabled?.[model.id];
                const attention = needsLook(model);
                const active = concurrency?.per_model_active?.[model.id] ?? 0;
                const slots = capacity?.[model.id] ?? model.max_concurrent;
                const own = Boolean(model.max_concurrent && model.max_concurrent > 0);
                const amount = earned?.[model.id];
                return [
                  <tr key={model.id} className={`border-t border-slate-800 ${attention ? 'bg-rose-950/20' : ''}`}>
                    <td className="px-4 py-2.5">
                      <button type="button" onClick={() => onModelClick?.(model.id)} className="flex items-center gap-2.5 text-left focus-visible:outline focus-visible:outline-sky-400" aria-label={`View details for ${model.id}`}>
                        <span aria-hidden="true" className={`h-2.5 w-2.5 flex-shrink-0 rounded-full ${!model.enabled ? 'bg-slate-500' : healthy(model) ? 'bg-emerald-400' : 'bg-rose-400'}`} />
                        <span className="min-w-0">
                          <span className="block break-all font-mono text-[13px] text-slate-100">{model.id}</span>
                          <span className={`block text-xs ${attention && model.enabled ? 'text-rose-300' : 'text-slate-500'}`}>
                            {!model.enabled ? 'disabled' : healthy(model) ? `${backendLabel(model.endpoint)} · ${model.category}` : `✗ ${model.state_string} · ${model.health_string}`}
                            {hubModel && hubModel.context_length > 0 && healthy(model) && ` · ${formatCount(hubModel.context_length)} ctx`}
                            {pulled && ` · ${pulled}`}
                          </span>
                        </span>
                      </button>
                    </td>
                    <td className="px-3 py-2.5 whitespace-nowrap">
                      <HubCell model={model} hubModel={hubModel} hubKnown={hubKnown} notListed={notListed.has(model.id)} drop={drops?.[model.id]} pulledReason={pulled} />
                    </td>
                    <td className="px-3 py-2.5 whitespace-nowrap font-mono text-[13px]" title={own ? 'Own limit from models.json, declared to the hub' : 'Global default; set an own limit to declare it to the hub'}>
                      {slots == null ? active : <>{active} / {slots}{!own && <span className="ml-1 text-slate-500">default</span>}</>}
                    </td>
                    <td className="px-3 py-2.5 whitespace-nowrap font-mono text-[13px]">
                      {model.rpm && model.rpm > 0 ? `${model.rpm} rpm` : <span className="text-slate-500">global</span>}
                    </td>
                    <td className="px-3 py-2.5 text-right whitespace-nowrap font-mono text-[13px]"><RecentCell hubModel={hubModel} /></td>
                    <td className="px-3 py-2.5 text-right whitespace-nowrap font-mono text-[13px]">
                      {amount == null ? <span className="text-slate-500">—</span> : amount === 0 ? <span className="text-slate-500">$0</span> : usd.format(amount)}
                    </td>
                    <td className="px-4 py-1.5 text-right whitespace-nowrap">
                      <button
                        type="button"
                        onClick={() => (authenticated ? setEditing(editing === model.id ? null : model.id) : onUnlock())}
                        className={`rounded p-2 transition-colors hover:bg-slate-700 ${editing === model.id ? 'text-blue-300' : 'text-slate-400 hover:text-slate-200'}`}
                        title="Concurrency and rate limits"
                        aria-label={`Edit limits for ${model.id}`}
                        aria-expanded={editing === model.id}
                      >
                        <SlidersHorizontal size={16} />
                      </button>
                      <button
                        type="button"
                        onClick={() => toggle(model)}
                        disabled={busy === model.id}
                        className={`rounded p-2 transition-colors disabled:opacity-50 ${model.enabled ? 'text-emerald-400 hover:bg-emerald-900/30 hover:text-emerald-300' : 'text-slate-400 hover:bg-slate-700 hover:text-slate-200'}`}
                        title={model.enabled ? 'Disable model' : 'Enable model'}
                        aria-label={`${model.enabled ? 'Disable' : 'Enable'} ${model.id}`}
                      >
                        <Power size={16} />
                      </button>
                    </td>
                  </tr>,
                  editing === model.id && (
                    <tr key={`${model.id}-limits`} className="bg-slate-900">
                      <td colSpan={7} className="px-4 pb-3">
                        <LimitsEditor model={model} onSaved={() => { setEditing(null); onRefresh(); }} onCancel={() => setEditing(null)} />
                      </td>
                    </tr>
                  ),
                ];
              })}
              {quiet.length > 0 && (
                <tr className="border-t border-slate-800">
                  <td colSpan={7} className="px-4 py-2 text-xs text-slate-400">
                    <button type="button" onClick={() => setShowQuiet((v) => !v)} className="min-h-9 hover:text-slate-200 focus-visible:outline focus-visible:outline-sky-400" aria-expanded={showQuiet}>
                      {showQuiet ? 'Hide' : 'Show'} {quiet.length} healthy model{quiet.length === 1 ? '' : 's'} with no recent traffic
                    </button>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          </div>
        </>
      )}
    </section>
  );
}

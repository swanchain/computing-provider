import { useCallback, useMemo, useState } from 'react';
import { AlertCircle } from 'lucide-react';
import { api } from '../api/client';
import { usePolling } from '../hooks/usePolling';
import { OTHER_COLOUR, OTHER_LABEL, SERIES_COLOURS, buildModelColours, colourFor, type ModelColourMap } from '../lib/modelPalette';
import type { ModelEarnings, UsageSource, UsageTotals } from '../types';

/**
 * What the GPUs did, by where the work came from.
 *
 * The earnings chart says what was paid. This says what was served, so the
 * difference — the operator's own benchmarks, the node's probes, and work that
 * reached a model server without passing through the node — is visible rather
 * than being load nobody can account for.
 *
 * Each bar is total tokens, input and output together: input is most of the
 * work (prompts run ten to twenty times the length of replies here), and a
 * chart of output alone made a busy GPU look idle. Every segment is split in
 * its own colour — output solid, input light — and the readout always states
 * both numbers, so the split never rests on shade alone.
 *
 * Direct input is a floor, not an estimate: a model server's prompt counter
 * skips cache hits, so the gap to what the node recorded can only be larger.
 *
 * Two views of the same bars. By model is the default and uses the earnings
 * chart's colours, so a model reads the same in both; by source is how paid
 * work is told apart from everything else.
 */

interface Series {
  key: string;
  label: string;
  colour: string;
  sources: UsageSource[];
  title: string;
}

// Colour carries meaning here, not identity: blue is the only paid series.
const SERIES: Series[] = [
  { key: 'hub', label: 'Hub (paid)', colour: SERIES_COLOURS[0], sources: ['hub'], title: 'Routed by Swan Inference — the only traffic that earns' },
  { key: 'local', label: 'Local gateway', colour: SERIES_COLOURS[1], sources: ['local'], title: 'Your own clients, sent through the node’s local gateway' },
  { key: 'direct', label: 'Direct to backend', colour: SERIES_COLOURS[2], sources: ['direct'], title: 'Served by a model server without passing through the node; input is a lower bound' },
  { key: 'probes', label: 'Probes', colour: OTHER_COLOUR, sources: ['health', 'selfcheck'], title: 'This node’s own health and audit checks' },
];

const SOURCE_SHORT: Record<string, string> = {
  hub: 'hub',
  local: 'local',
  direct: 'direct',
  health: 'probes',
  selfcheck: 'probes',
};

/** The input share of a segment is drawn in its colour at this opacity. */
const INPUT_OPACITY = 0.35;

const WINDOWS = [
  { id: '24h', label: '24 hours' },
  { id: '7d', label: '7 days' },
  { id: '30d', label: '30 days' },
] as const;

type BySource = Partial<Record<UsageSource, UsageTotals>>;
type ModelsBySource = Record<string, BySource>;

interface Segment {
  key: string;
  label: string;
  colour: string;
  tokensIn: number;
  tokensOut: number;
  /** Direct input is a floor, so any figure that includes it is too. */
  inIsFloor: boolean;
  detail: string;
  title?: string;
}

function formatTokens(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return Math.round(v).toLocaleString();
}

function formatBucket(ts: string, bucketSeconds: number, long = false) {
  const d = new Date(ts);
  if (bucketSeconds >= 86_400) {
    return d.toLocaleDateString(undefined, { year: long ? 'numeric' : undefined, month: 'short', day: 'numeric' });
  }
  return long
    ? d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
    : d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
}

function addTotals(into: UsageTotals, t: UsageTotals) {
  into.requests += t.requests;
  into.tokens_in += t.tokens_in;
  into.tokens_out += t.tokens_out;
}

function sumSources(bySource: BySource, sources: UsageSource[]): UsageTotals {
  const out = { requests: 0, tokens_in: 0, tokens_out: 0 };
  for (const src of sources) {
    const t = bySource[src];
    if (t) addTotals(out, t);
  }
  return out;
}

function sumAll(bySource: BySource | undefined): UsageTotals {
  const out = { requests: 0, tokens_in: 0, tokens_out: 0 };
  for (const t of Object.values(bySource ?? {})) if (t) addTotals(out, t);
  return out;
}

/** "hub 3.81M/24.9k · direct ≥10.5M/621k" — input/output per source. */
function sourceBreakdown(bySource: BySource | undefined) {
  const acc = new Map<string, UsageTotals>();
  for (const [src, t] of Object.entries(bySource ?? {})) {
    if (!t || t.tokens_in + t.tokens_out <= 0) continue;
    const k = SOURCE_SHORT[src] ?? src;
    const cur = acc.get(k) ?? { requests: 0, tokens_in: 0, tokens_out: 0 };
    addTotals(cur, t);
    acc.set(k, cur);
  }
  return [...acc]
    .map(([k, t]) => `${k} ${k === 'direct' ? '≥' : ''}${formatTokens(t.tokens_in)}/${formatTokens(t.tokens_out)}`)
    .join(' · ');
}

function inOut(s: Pick<Segment, 'tokensIn' | 'tokensOut' | 'inIsFloor'>) {
  return `${s.inIsFloor ? '≥' : ''}${formatTokens(s.tokensIn)} in · ${formatTokens(s.tokensOut)} out`;
}

interface UsageChartProps {
  /** Lifetime per-model earnings, the fallback ranking before `colours` arrives. */
  models?: ModelEarnings[];
  /** The earnings chart's colour map; used as-is so a model matches in both. */
  colours?: ModelColourMap | null;
  /** This node's name. */
  nodeName?: string;
  /** The provider's other nodes. When there are any, usage can cover every machine. */
  peers?: string[];
}

export function UsageChart({ models, colours: shared, nodeName, peers }: UsageChartProps) {
  const hasPeers = (peers?.length ?? 0) > 0;
  // All machines by default when there are any: that is the scope the earnings
  // chart beside it reports, so the two can be read against each other.
  const [scope, setScope] = useState<'node' | 'all'>('all');
  const effectiveScope = hasPeers ? scope : 'node';
  const [window_, setWindow] = useState<string>('24h');
  const [view, setView] = useState<'model' | 'source' | 'machine'>('model');
  const [hovered, setHovered] = useState<number | null>(null);
  const { data, loading, error } = usePolling(
    useCallback(() => api.getUsageHistory(window_, effectiveScope), [window_, effectiveScope]),
    60_000,
  );

  const points = useMemo(() => data?.points ?? [], [data?.points]);
  const bucketSeconds = data?.bucket_seconds ?? 3600;

  // The window's per-model totals, for the readout when nothing is hovered.
  const windowModels = useMemo(() => {
    const acc: ModelsBySource = {};
    for (const p of points) {
      for (const [model, bySource] of Object.entries(p.models ?? {})) {
        const m = (acc[model] ??= {});
        for (const [src, t] of Object.entries(bySource)) {
          if (!t) continue;
          addTotals((m[src as UsageSource] ??= { requests: 0, tokens_in: 0, tokens_out: 0 }), t);
        }
      }
    }
    return acc;
  }, [points]);

  // Ranked on lifetime earnings like the earnings chart, so the same model is
  // the same colour in both. Models with usage but no earnings are appended at
  // zero, which keeps them eligible for a colour instead of vanishing.
  const own = useMemo(() => {
    const ranked = new Map<string, ModelEarnings>();
    for (const m of models ?? []) ranked.set(m.model, m);
    for (const id of Object.keys(windowModels)) {
      if (!ranked.has(id)) {
        ranked.set(id, { model: id, total_usd: 0, tokens_in: 0, tokens_out: 0, input_usd: 0, output_usd: 0, priced: false });
      }
    }
    return buildModelColours([...ranked.values()]);
  }, [models, windowModels]);
  const colours = shared ?? own;

  const machines = useMemo(() => (effectiveScope === 'all' ? (data?.machines ?? []) : []), [data?.machines, effectiveScope]);
  const segmentsFor = useCallback(
    (sources: BySource, byModel: ModelsBySource, bucket: number | 'total'): Segment[] => {
      if (view === 'machine' && machines.length > 0) {
        return machines
          .filter((m) => !m.error)
          .map((m, k) => {
            const t = bucket === 'total' ? m.totals : (m.points[bucket] ?? { requests: 0, tokens_in: 0, tokens_out: 0 });
            return {
              key: 'machine:' + m.name,
              label: m.self ? `${m.name} (this node)` : m.name,
              colour: SERIES_COLOURS[k] ?? OTHER_COLOUR,
              tokensIn: t.tokens_in,
              tokensOut: t.tokens_out,
              inIsFloor: false,
              detail: `${t.requests.toLocaleString()} req`,
            };
          });
      }
      if (view === 'source') {
        return SERIES.map((s) => {
          const t = sumSources(sources, s.sources);
          return {
            key: s.key,
            label: s.label,
            colour: s.colour,
            title: s.title,
            tokensIn: t.tokens_in,
            tokensOut: t.tokens_out,
            inIsFloor: s.key === 'direct',
            detail: s.key === 'direct' ? 'requests not seen by the node' : `${t.requests.toLocaleString()} req`,
          };
        });
      }
      const named: Segment[] = [];
      const other: BySource = {};
      for (const [model, bySource] of Object.entries(byModel)) {
        const t = sumAll(bySource);
        if (t.tokens_in + t.tokens_out <= 0) continue;
        if (colours.colours.has(model)) {
          named.push({
            key: model,
            label: model,
            colour: colourFor(colours, model),
            tokensIn: t.tokens_in,
            tokensOut: t.tokens_out,
            inIsFloor: (bySource.direct?.tokens_in ?? 0) > 0,
            detail: sourceBreakdown(bySource),
          });
        } else {
          for (const [src, st] of Object.entries(bySource)) {
            if (st) addTotals((other[src as UsageSource] ??= { requests: 0, tokens_in: 0, tokens_out: 0 }), st);
          }
        }
      }
      named.sort((a, b) => b.tokensIn + b.tokensOut - (a.tokensIn + a.tokensOut));
      const o = sumAll(other);
      if (o.tokens_in + o.tokens_out > 0) {
        named.push({
          key: '__other',
          label: OTHER_LABEL,
          colour: OTHER_COLOUR,
          tokensIn: o.tokens_in,
          tokensOut: o.tokens_out,
          inIsFloor: (other.direct?.tokens_in ?? 0) > 0,
          detail: sourceBreakdown(other),
        });
      }
      return named;
    },
    [view, colours, machines],
  );

  const total = (segs: Segment[]) => segs.reduce((a, s) => a + s.tokensIn + s.tokensOut, 0);
  const peak = points.reduce((m, p, i) => Math.max(m, total(segmentsFor(p.sources, p.models ?? {}, i))), 0);
  const active = hovered !== null ? points[hovered] : null;
  const summarySegments = active
    ? segmentsFor(active.sources, active.models ?? {}, hovered ?? 0)
    : data
      ? segmentsFor(data.totals, windowModels, 'total')
      : [];
  const summaryLabel = active
    ? formatBucket(active.timestamp, bucketSeconds, true)
    : (WINDOWS.find((w) => w.id === window_)?.label ?? window_);

  // The legend names models that appear in this window, not every model ever.
  const legendModels = (() => {
    const present = new Set(
      Object.keys(windowModels).filter((m) => {
        const t = sumAll(windowModels[m]);
        return t.tokens_in + t.tokens_out > 0;
      }),
    );
    const entries: { key: string; label: string; colour: string; title?: string }[] = colours.ordered
      .filter((m) => present.has(m))
      .map((m) => ({ key: m, label: m, colour: colourFor(colours, m) }));
    if ([...present].some((m) => !colours.colours.has(m))) {
      entries.push({ key: '__other', label: OTHER_LABEL, colour: OTHER_COLOUR });
    }
    return entries;
  })();

  const unmeasured = (data?.endpoints ?? []).filter((e) => !e.measured);
  const all = data ? sumAll(data.totals) : null;
  const hub = data ? sumSources(data.totals, ['hub']) : null;
  const directIn = data?.totals.direct?.tokens_in ?? 0;

  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <div>
          <h3 className="text-sm font-medium text-slate-300">
            Usage over time{' '}
            <span className="font-normal text-slate-500">
              {effectiveScope === 'all'
                ? `· all machines (${machines.length || (peers?.length ?? 0) + 1})`
                : `· this node${nodeName ? ` (${nodeName})` : ''} only`}
            </span>
          </h3>
          <p className="text-xs text-slate-400">
            {loading && !data
              ? 'Loading…'
              : all && hub && all.tokens_in + all.tokens_out > 0
                ? `${directIn > 0 ? '≥' : ''}${formatTokens(all.tokens_in)} in · ${formatTokens(all.tokens_out)} out · ${Math.round(
                    ((hub.tokens_in + hub.tokens_out) / (all.tokens_in + all.tokens_out)) * 100,
                  )}% for the hub`
                : 'No tokens in this window'}
          </p>
        </div>
        <div className="flex flex-wrap gap-1">
          {hasPeers && (
            <div className="flex gap-1" role="group" aria-label="Machines covered">
              {(['all', 'node'] as const).map((sc) => (
                <button
                  key={sc}
                  type="button"
                  onClick={() => {
                    setScope(sc);
                    if (sc === 'node' && view === 'machine') setView('model');
                  }}
                  aria-pressed={scope === sc}
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium transition focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                    scope === sc ? 'bg-slate-700 text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                  }`}
                >
                  {sc === 'all' ? 'All machines' : 'This node'}
                </button>
              ))}
            </div>
          )}
          <div className="flex gap-1" role="group" aria-label="Split bars by">
            {(effectiveScope === 'all' ? (['model', 'source', 'machine'] as const) : (['model', 'source'] as const)).map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => setView(v)}
                aria-pressed={view === v}
                className={`rounded-lg px-3 py-1.5 text-xs font-medium transition focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                  view === v ? 'bg-slate-700 text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                }`}
              >
                By {v}
              </button>
            ))}
          </div>
          <div className="flex gap-1" role="group" aria-label="Time window">
            {WINDOWS.map((w) => (
              <button
                key={w.id}
                type="button"
                onClick={() => setWindow(w.id)}
                aria-pressed={window_ === w.id}
                className={`rounded-lg px-3 py-1.5 text-xs font-medium transition focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                  window_ === w.id ? 'bg-slate-700 text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                }`}
              >
                {w.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {error && !data ? (
        <p className="px-4 py-6 text-sm text-amber-300">Could not load usage: {error.message}</p>
      ) : points.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">No usage recorded for this window yet.</p>
      ) : (
        <div className="px-4 py-4">
          <div className="mb-2 min-h-32 text-xs" aria-live="polite">
            <div className="flex items-baseline gap-2">
              <span className="text-slate-400">{summaryLabel}</span>
              {!active && <span className="text-slate-500">· hover a bar for one interval</span>}
            </div>
            {summarySegments.some((s) => s.tokensIn + s.tokensOut > 0) ? (
              <ul className="mt-1 space-y-0.5 text-[11px]">
                {summarySegments.map((s) => (
                  <li key={s.key} title={s.title}>
                    <div className="flex items-center gap-2">
                      <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-sm" style={{ backgroundColor: s.colour }} />
                      <span className="min-w-0 flex-1 truncate text-slate-300">{s.label}</span>
                      <span className="shrink-0 font-mono text-slate-400">{inOut(s)}</span>
                    </div>
                    {/* Its own line: at half width, a right-aligned column cut
                        off the direct share, which is the part worth reading. */}
                    {s.detail && <div className="pl-4 font-mono text-[10px] text-slate-500">{s.detail}</div>}
                  </li>
                ))}
              </ul>
            ) : (
              <div className="mt-1 text-slate-500">Nothing served.</div>
            )}
          </div>

          <div
            className="flex h-32 items-end gap-px"
            onMouseLeave={() => setHovered(null)}
            role="group"
            aria-label={`Input and output tokens per interval over ${window_}, split by ${view}`}
          >
            {points.map((p, i) => {
              const segs = segmentsFor(p.sources, p.models ?? {}, i).filter((x) => x.tokensIn + x.tokensOut > 0);
              const t = total(segs);
              const h = peak > 0 && t > 0 ? Math.max(2, (t / peak) * 100) : 0;
              const describe = segs.length ? segs.map((x) => `${x.label} ${inOut(x)}`).join(', ') : 'nothing served';
              return (
                <button
                  key={p.timestamp}
                  type="button"
                  onMouseEnter={() => setHovered(i)}
                  onFocus={() => setHovered(i)}
                  onBlur={() => setHovered(null)}
                  aria-label={`${formatBucket(p.timestamp, bucketSeconds, true)}: ${describe}`}
                  className={`flex h-full flex-1 flex-col justify-end focus:outline-none focus:ring-1 focus:ring-blue-400 ${hovered === i ? 'ring-1 ring-white/40' : ''}`}
                >
                  <span className="flex w-full flex-col-reverse" style={{ height: `${h}%` }}>
                    {segs.map((x, idx) => {
                      const segTotal = x.tokensIn + x.tokensOut;
                      return (
                        <span
                          key={x.key}
                          className={`flex w-full flex-col ${idx === segs.length - 1 ? 'overflow-hidden rounded-t' : ''}`}
                          style={{ height: `${(segTotal / t) * 100}%`, marginTop: idx === segs.length - 1 ? 0 : 2 }}
                        >
                          {/* Output on top, solid; input below, light. */}
                          <span
                            className="block w-full"
                            style={{ height: `${(x.tokensOut / segTotal) * 100}%`, backgroundColor: x.colour, opacity: hovered === i ? 1 : 0.9 }}
                          />
                          <span
                            className="block w-full"
                            style={{ height: `${(x.tokensIn / segTotal) * 100}%`, backgroundColor: x.colour, opacity: INPUT_OPACITY }}
                          />
                        </span>
                      );
                    })}
                  </span>
                </button>
              );
            })}
          </div>

          <div className="mt-2 flex justify-between text-xs text-slate-400">
            <span>{points[0] && formatBucket(points[0].timestamp, bucketSeconds, true)}</span>
            <span>{points[points.length - 1] && formatBucket(points[points.length - 1].timestamp, bucketSeconds, true)}</span>
          </div>

          <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs" aria-label={view === 'model' ? 'Models in this chart' : 'Sources in this chart'}>
            {(view === 'source'
              ? SERIES.map((s) => ({ key: s.key, label: s.label, colour: s.colour, title: s.title }))
              : view === 'machine' && machines.length > 0
                ? machines.filter((m) => !m.error).map((m, k) => ({ key: 'machine:' + m.name, label: m.self ? `${m.name} (this node)` : m.name, colour: SERIES_COLOURS[k] ?? OTHER_COLOUR, title: undefined as string | undefined }))
                : legendModels
            ).map((s) => (
              <li key={s.key} className="flex min-w-0 items-center gap-1.5" title={s.title}>
                <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-sm" style={{ backgroundColor: s.colour }} />
                <span className="break-all text-slate-400">{s.label}</span>
              </li>
            ))}
            <li className="flex items-center gap-1.5 text-slate-500">
              <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-sm bg-slate-300" />
              output
              <span aria-hidden="true" className="ml-2 h-2 w-2 shrink-0 rounded-sm bg-slate-300" style={{ opacity: INPUT_OPACITY }} />
              input
            </li>
          </ul>
        </div>
      )}

      <div className="flex items-start gap-2 border-t border-slate-800 px-4 py-3 text-xs text-slate-400">
        <AlertCircle aria-hidden="true" size={14} className="mt-px shrink-0" />
        <div className="space-y-1">
          <p>
            {effectiveScope === 'all'
              ? 'Covers every machine of this provider, the same scope as the earnings beside it.'
              : 'Counts only this machine; the earnings beside it are for the whole provider account, so models served on another machine appear there and not here.'}{' '}
            Bars are total tokens: output solid, input light. Direct input (≥) is a floor — the model server’s
            prompt counter skips cache hits, so the real figure can only be higher. Only hub traffic is paid.
            {data?.local_gateway && (
              <>
                {' '}Point local clients at <span className="font-mono text-slate-300">{data.local_gateway}</span> to
                record them as local rather than direct.
              </>
            )}
          </p>
          {machines.filter((m) => m.error).map((m) => (
            <p key={m.name} className="text-amber-300/90">
              Usage from <span className="font-mono">{m.name}</span> could not be read ({m.error}); it is missing from these
              bars, not idle.
            </p>
          ))}
          {unmeasured.length > 0 && (
            <p className="text-amber-300/90">
              Direct usage is not measured for{' '}
              {unmeasured.map((e, i) => (
                <span key={e.endpoint}>
                  {i > 0 && '; '}
                  <span className="break-all font-mono">{e.models.join(', ')}</span> ({e.reason})
                </span>
              ))}
              . Work sent straight to {unmeasured.length === 1 ? 'that server' : 'those servers'} is not shown.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

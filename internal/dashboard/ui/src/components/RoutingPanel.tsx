import { useMemo, useState } from 'react';
import type { UsePollingResult } from '../hooks/usePolling';
import type { RouteBackend, RouteLink, RouteMap } from '../types';

/**
 * Where traffic enters the node, which model it asks for, and which backend
 * served it, over a recent window. Every configured backend is drawn whether
 * or not it carried traffic, so a server that stopped or moved shows as a
 * backend with no links and a red dot rather than as nothing at all.
 *
 * "Direct" links are work a backend's own counters show that never passed
 * through the node. They carry tokens only — no request was seen, so there is
 * no count, latency or error — and are drawn dotted and sized by tokens.
 */

export const ROUTE_WINDOWS = ['5m', '15m', '1h', '6h', '24h'] as const;

const SOURCE_LABEL: Record<string, string> = {
  hub: 'Swan hub',
  local: 'Local clients',
  health: 'Health probes',
  selfcheck: 'Self-check',
  direct: 'Direct (bypassed node)',
};
// The diagram's boxes are narrow; the table and tooltips keep the full name.
const SOURCE_SHORT: Record<string, string> = { direct: 'Direct' };
const SOURCE_ORDER = ['hub', 'local', 'direct', 'health', 'selfcheck'];
const SOURCE_COLOUR: Record<string, string> = {
  hub: '#38bdf8',
  local: '#a78bfa',
  health: '#64748b',
  selfcheck: '#64748b',
  direct: '#fbbf24',
};
const DIRECT_DASH = '2 5';
const FAIL_COLOUR = '#f87171';
const HEALTH_COLOUR: Record<string, string> = { up: '#34d399', down: '#f87171', unknown: '#94a3b8' };

const W = 960;
const ROW = 46;
const NODE_H = 34;
const COL = { source: 16, model: 360, backend: 704 };
const COL_W = { source: 200, model: 260, backend: 240 };

function compact(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return String(v);
}

function clip(s: string, n: number) {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

function ago(iso: string) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return 'just now';
  if (s < 5400) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
}

// 1px to 4.5px: wide enough to rank routes at a glance, narrow enough that
// many routes into one box stay readable.
function stroke(requests: number, max: number) {
  return 1 + 3.5 * (Math.log1p(requests) / Math.log1p(Math.max(max, 1)));
}

const isDirect = (l: RouteLink) => l.source === 'direct';
const directTokens = (l: RouteLink) => l.tokens_in + l.tokens_out;

interface Edge {
  from: string;
  to: string;
  requests: number;
  failures: number;
  tokens: number;
  direct: boolean;
  colour: string;
}

function curve(x1: number, y1: number, x2: number, y2: number) {
  const mx = (x1 + x2) / 2;
  return `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`;
}

function RouteDiagram({ map, showProbes }: { map: RouteMap; showProbes: boolean }) {
  const [focus, setFocus] = useState<string | null>(null);
  const links = map.links.filter((l) => showProbes || (l.source !== 'health' && l.source !== 'selfcheck'));

  const sources = SOURCE_ORDER.filter((s) => links.some((l) => l.source === s))
    .concat([...new Set(links.map((l) => l.source))].filter((s) => !SOURCE_ORDER.includes(s)));
  const modelTotals = new Map<string, number>();
  const modelDirect = new Map<string, number>();
  for (const l of links) {
    modelTotals.set(l.model, (modelTotals.get(l.model) ?? 0) + l.requests);
    if (isDirect(l)) modelDirect.set(l.model, (modelDirect.get(l.model) ?? 0) + directTokens(l));
  }
  const models = [...modelTotals.keys()].sort((a, b) => (modelTotals.get(b) ?? 0) - (modelTotals.get(a) ?? 0)
    || (modelDirect.get(b) ?? 0) - (modelDirect.get(a) ?? 0) || a.localeCompare(b));
  const backendWeight = (ep: string) => links.filter((l) => l.backend === ep).reduce((n, l) => n + l.requests, 0);
  const backends: RouteBackend[] = [...map.backends].sort((a, b) => backendWeight(b.endpoint) - backendWeight(a.endpoint) || a.endpoint.localeCompare(b.endpoint));

  const rows = Math.max(sources.length, models.length, backends.length, 1);
  const H = rows * ROW + 40;
  const y = (i: number, n: number) => 30 + ((rows - n) * ROW) / 2 + i * ROW + NODE_H / 2;
  const pos = new Map<string, { x: number; y: number }>();
  sources.forEach((s, i) => pos.set(`s:${s}`, { x: COL.source + COL_W.source, y: y(i, sources.length) }));
  models.forEach((m, i) => pos.set(`m:${m}`, { x: COL.model, y: y(i, models.length) }));
  backends.forEach((b, i) => pos.set(`b:${b.endpoint}`, { x: COL.backend, y: y(i, backends.length) }));

  // Direct work is kept on edges of its own: it is measured in tokens, not
  // requests, and must not thicken or hide the recorded routes beside it.
  const merge = (edges: Map<string, Edge>, from: string, to: string, l: RouteLink, colour: string) => {
    const direct = isDirect(l);
    const key = `${from}|${to}|${direct ? 'd' : 'r'}`;
    const e = edges.get(key) ?? { from, to, requests: 0, failures: 0, tokens: 0, direct, colour };
    e.requests += l.requests;
    e.failures += l.failures;
    e.tokens += directTokens(l);
    edges.set(key, e);
  };
  const left = new Map<string, Edge>();
  const right = new Map<string, Edge>();
  for (const l of links) {
    merge(left, `s:${l.source}`, `m:${l.model}`, l, SOURCE_COLOUR[l.source] ?? '#94a3b8');
    if (l.backend) merge(right, `m:${l.model}`, `b:${l.backend}`, l, isDirect(l) ? SOURCE_COLOUR.direct : '#38bdf8');
  }
  const edges = [...left.values(), ...right.values()];
  const max = Math.max(1, ...edges.filter((e) => !e.direct).map((e) => e.requests));
  const maxTokens = Math.max(1, ...edges.filter((e) => e.direct).map((e) => e.tokens));
  const touches = (e: Edge) => !focus || e.from === focus || e.to === focus;

  // Sources and backends each sit on one side of the edges, so their edges
  // add up to their own total.
  const totalBy = (key: string) => edges.filter((e) => e.from === key || e.to === key).reduce((n, e) => n + e.requests, 0);
  const tokensBy = (key: string) => edges.filter((e) => e.direct && (e.from === key || e.to === key)).reduce((n, e) => n + e.tokens, 0);

  const nodeProps = (key: string, label: string) => ({
    tabIndex: 0,
    role: 'button',
    'aria-label': label,
    'aria-pressed': focus === key,
    onMouseEnter: () => setFocus(key),
    onMouseLeave: () => setFocus(null),
    onFocus: () => setFocus(key),
    onBlur: () => setFocus(null),
    className: 'cursor-pointer outline-none',
  });

  return (
    <div className="overflow-x-auto rounded-xl border border-slate-800 bg-slate-950/60">
      <svg viewBox={`0 0 ${W} ${H}`} className="min-w-[720px] w-full" role="img" aria-label="Request routes from sources through models to backends">
        <text x={COL.source} y={18} className="fill-slate-500 text-[12px] font-semibold uppercase tracking-wider">Entered from</text>
        <text x={COL.model} y={18} className="fill-slate-500 text-[12px] font-semibold uppercase tracking-wider">Model</text>
        <text x={COL.backend} y={18} className="fill-slate-500 text-[12px] font-semibold uppercase tracking-wider">Backend</text>

        {edges.map((e) => {
          const a = pos.get(e.from);
          const b = pos.get(e.to);
          if (!a || !b) return null;
          const fail = e.requests ? e.failures / e.requests : 0;
          const x1 = e.from.startsWith('m:') ? a.x + COL_W.model : a.x;
          return (
            <path
              key={`${e.from}|${e.to}|${e.direct}`}
              d={curve(x1, a.y, b.x, b.y)}
              fill="none"
              stroke={fail >= 0.05 ? FAIL_COLOUR : e.colour}
              strokeWidth={e.direct ? stroke(e.tokens, maxTokens) : stroke(e.requests, max)}
              strokeOpacity={touches(e) ? 0.75 : 0.08}
              strokeDasharray={e.direct ? DIRECT_DASH : fail > 0 && fail < 0.05 ? '6 4' : undefined}
              strokeLinecap={e.direct ? 'round' : undefined}
            >
              <title>{e.direct ? `${e.tokens.toLocaleString()} tokens sent straight to the backend, not through the node` : `${e.requests.toLocaleString()} requests${e.failures ? `, ${e.failures} failed` : ''}`}</title>
            </path>
          );
        })}

        {sources.map((s, i) => {
          const key = `s:${s}`;
          const cy = y(i, sources.length);
          const direct = s === 'direct';
          return (
            <g key={key} {...nodeProps(key, direct ? `${SOURCE_LABEL[s]}: ${tokensBy(key)} tokens` : `${SOURCE_LABEL[s] ?? s}: ${totalBy(key)} requests`)}>
              <rect x={COL.source} y={cy - NODE_H / 2} width={COL_W.source} height={NODE_H} rx={8} fill="#0f172a" stroke={SOURCE_COLOUR[s] ?? '#475569'} strokeWidth={focus === key ? 2 : 1} />
              <title>{SOURCE_LABEL[s] ?? s}</title>
              <text x={COL.source + 12} y={cy + 4} className="fill-slate-100 text-[13px]">{SOURCE_SHORT[s] ?? SOURCE_LABEL[s] ?? s}</text>
              <text x={COL.source + COL_W.source - 12} y={cy + 4} textAnchor="end" className="fill-slate-400 text-[12px] tabular-nums">{direct ? `${compact(tokensBy(key))} tok` : compact(totalBy(key))}</text>
            </g>
          );
        })}

        {models.map((m, i) => {
          const key = `m:${m}`;
          const cy = y(i, models.length);
          const failed = links.filter((l) => l.model === m).reduce((n, l) => n + l.failures, 0);
          const count = `${modelTotals.get(m) || !modelDirect.get(m) ? compact(modelTotals.get(m) ?? 0) : `${compact(modelDirect.get(m) ?? 0)} tok`}${failed ? ` · ${failed}✕` : ''}`;
          return (
            <g key={key} {...nodeProps(key, `${m}: ${modelTotals.get(m)} requests${failed ? `, ${failed} failed` : ''}`)}>
              <title>{m}</title>
              <rect x={COL.model} y={cy - NODE_H / 2} width={COL_W.model} height={NODE_H} rx={8} fill="#0f172a" stroke={failed ? FAIL_COLOUR : '#334155'} strokeWidth={focus === key ? 2 : 1} />
              {/* About 7px per monospace character; the name gives way to the count. */}
              <text x={COL.model + 12} y={cy + 4} className="fill-slate-100 font-mono text-[12px]">{clip(m, Math.min(26, 32 - count.length))}</text>
              <text x={COL.model + COL_W.model - 12} y={cy + 4} textAnchor="end" className={`text-[12px] tabular-nums ${failed ? 'fill-red-300' : 'fill-slate-400'}`}>
                {count}
              </text>
            </g>
          );
        })}

        {backends.map((b, i) => {
          const key = `b:${b.endpoint}`;
          const cy = y(i, backends.length);
          const total = totalBy(key);
          const count = b.health === 'down' ? 'down' : total ? compact(total) : tokensBy(key) ? `${compact(tokensBy(key))} tok` : 'idle';
          return (
            <g key={key} {...nodeProps(key, `${b.endpoint}: ${b.health}, ${total} requests`)}>
              <title>{`${b.endpoint} — ${b.health}${b.error ? `: ${b.error}` : ''}\n${b.models.join('\n')}`}</title>
              <rect x={COL.backend} y={cy - NODE_H / 2} width={COL_W.backend} height={NODE_H} rx={8} fill="#0f172a" stroke={b.health === 'down' ? FAIL_COLOUR : '#334155'} strokeWidth={focus === key ? 2 : 1} />
              <circle cx={COL.backend + 14} cy={cy} r={5} fill={HEALTH_COLOUR[b.health] ?? HEALTH_COLOUR.unknown} />
              <text x={COL.backend + 26} y={cy + 4} className="fill-slate-100 font-mono text-[12px]">{clip(b.endpoint.replace(/^https?:\/\//, ''), Math.min(22, 27 - count.length))}</text>
              <text x={COL.backend + COL_W.backend - 12} y={cy + 4} textAnchor="end" className={`text-[12px] tabular-nums ${b.health === 'down' ? 'fill-red-300' : 'fill-slate-400'}`}>
                {count}
              </text>
            </g>
          );
        })}

        {links.length === 0 && (
          <text x={COL.model} y={y(0, 1) + 4} className="fill-slate-500 text-[13px]">No requests in this window.</text>
        )}
      </svg>
    </div>
  );
}

export function RoutingPanel({
  report,
  window,
  onWindowChange,
}: {
  report: UsePollingResult<RouteMap>;
  window: string;
  onWindowChange: (w: string) => void;
}) {
  const [showProbes, setShowProbes] = useState(false);
  const { data, error, loading, lastUpdated } = report;
  const links = useMemo(
    () => (data?.links ?? []).filter((l) => showProbes || (l.source !== 'health' && l.source !== 'selfcheck')),
    [data, showProbes],
  );
  const down = (data?.backends ?? []).filter((b) => b.health === 'down');
  const total = links.reduce((n, l) => n + l.requests, 0);
  const failed = links.reduce((n, l) => n + l.failures, 0);
  const direct = links.filter(isDirect).reduce((n, l) => n + directTokens(l), 0);

  return (
    <section aria-labelledby="routing-heading" className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 id="routing-heading" className="text-xl font-semibold text-white">Routing</h2>
          <p className="mt-1 text-sm text-slate-400">
            Where requests entered, which model they asked for, and which backend served them.
            {lastUpdated && ` Updated ${lastUpdated.toLocaleTimeString()}.`}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="flex min-h-11 items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={showProbes} onChange={(e) => setShowProbes(e.target.checked)} className="h-4 w-4 accent-sky-400" />
            Include probes
          </label>
          <div role="group" aria-label="Time window" className="flex rounded-lg border border-slate-700 p-0.5">
            {ROUTE_WINDOWS.map((w) => (
              <button
                key={w}
                type="button"
                onClick={() => onWindowChange(w)}
                aria-pressed={window === w}
                className={`min-h-10 rounded-md px-3 text-sm ${window === w ? 'bg-slate-700 text-white' : 'text-slate-400 hover:text-slate-200'}`}
              >
                {w}
              </button>
            ))}
          </div>
        </div>
      </div>

      {error && <p role="alert" className="rounded-lg border border-amber-800/60 p-4 text-sm text-amber-200">{data ? 'Refresh failed. Showing the previous routes.' : `Could not load routes: ${error.message}`}</p>}
      {loading && !data && <p className="text-sm text-slate-400">Loading routes…</p>}

      {data && (
        <>
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-slate-300">
            <span><span className="font-semibold text-white tabular-nums">{total.toLocaleString()}</span> requests</span>
            <span className={failed ? 'text-red-300' : undefined}><span className="font-semibold tabular-nums">{failed.toLocaleString()}</span> failed</span>
            {direct > 0 && <span className="text-amber-200"><span className="font-semibold tabular-nums">{compact(direct)}</span> tokens bypassed the node</span>}
            <span>{data.backends.length} backends{down.length > 0 && <span className="text-red-300"> · {down.length} down</span>}</span>
          </div>

          {down.length > 0 && (
            <ul className="space-y-1 rounded-lg border border-red-900/60 bg-red-950/30 p-3 text-sm text-red-200">
              {down.map((b) => (
                <li key={b.endpoint}>
                  <span aria-hidden="true">✕ </span>
                  <span className="break-all font-mono">{b.endpoint}</span> is down — {b.models.join(', ')}
                  {b.error && <span className="block break-all text-xs text-red-300/80">{b.error}</span>}
                </li>
              ))}
            </ul>
          )}

          <RouteDiagram map={data} showProbes={showProbes} />
          <p className="text-xs text-slate-500">
            Line width grows with request count. Red lines fail 5% or more; dashed lines have some failures. Hover or focus a box to isolate its routes.
            {direct > 0 && ' Amber dotted lines are direct work: tokens a backend counted that never passed through the node, sized by tokens. The node saw no requests, so there is no count, latency or error for them. Point those clients at the local gateway to record them.'}
          </p>

          <div className="overflow-x-auto rounded-xl border border-slate-800">
            <table className="w-full min-w-[720px] text-left text-sm">
              <caption className="sr-only">Routes in the last {data.window}</caption>
              <thead className="bg-slate-900/80 text-xs uppercase tracking-wider text-slate-400">
                <tr>
                  <th scope="col" className="px-3 py-2">From</th>
                  <th scope="col" className="px-3 py-2">Model</th>
                  <th scope="col" className="px-3 py-2">Backend</th>
                  <th scope="col" className="px-3 py-2 text-right">Requests</th>
                  <th scope="col" className="px-3 py-2 text-right">Failed</th>
                  <th scope="col" className="px-3 py-2 text-right">Avg latency</th>
                  <th scope="col" className="px-3 py-2 text-right">Tokens in / out</th>
                  <th scope="col" className="px-3 py-2">Last</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800">
                {links.map((l) => (
                  <tr key={`${l.source}|${l.model}|${l.backend}`} className="align-top">
                    <td className="px-3 py-2 text-slate-300">{SOURCE_LABEL[l.source] ?? l.source}</td>
                    <td className="break-all px-3 py-2 font-mono text-xs text-slate-200">{l.model}</td>
                    <td className="break-all px-3 py-2 font-mono text-xs text-slate-400">{l.backend || <span className="font-sans text-slate-500">not forwarded</span>}</td>
                    {isDirect(l) ? <>
                      <td className="px-3 py-2 text-right text-slate-500" title="Not seen by the node">—</td>
                      <td className="px-3 py-2 text-right text-slate-500">—</td>
                      <td className="px-3 py-2 text-right text-slate-500">—</td>
                    </> : <>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-200">{l.requests.toLocaleString()}</td>
                    <td className={`px-3 py-2 text-right tabular-nums ${l.failures ? 'text-red-300' : 'text-slate-500'}`}>
                      {l.failures ? `✕ ${l.failures}` : '0'}
                      {l.last_error && <span className="block max-w-[16rem] truncate text-left text-xs text-red-300/80" title={l.last_error}>{l.last_error}</span>}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-300">{l.avg_latency_ms >= 1000 ? `${(l.avg_latency_ms / 1000).toFixed(1)}s` : `${Math.round(l.avg_latency_ms)}ms`}</td>
                    </>}
                    <td className="px-3 py-2 text-right tabular-nums text-slate-400">{compact(l.tokens_in)} / {compact(l.tokens_out)}</td>
                    <td className="px-3 py-2 text-slate-400">{l.last_at ? ago(l.last_at) : '—'}</td>
                  </tr>
                ))}
                {links.length === 0 && (
                  <tr><td colSpan={8} className="px-3 py-6 text-center text-slate-500">No requests in the last {data.window}.</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

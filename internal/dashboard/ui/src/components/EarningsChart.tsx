import { useCallback, useMemo, useState } from 'react';
import { AlertCircle } from 'lucide-react';
import { api } from '../api/client';
import { usePolling } from '../hooks/usePolling';
import {
  OTHER_COLOUR,
  OTHER_LABEL,
  UNATTRIBUTED_COLOUR,
  UNATTRIBUTED_LABEL,
  buildModelColours,
  colourFor,
} from '../lib/modelPalette';
import type { EarningsPoint, ModelEarnings } from '../types';

interface EarningsChartProps {
  /**
   * Lifetime per-model earnings, used only to decide which models get a colour.
   * Taken from the same source the donut ranks by, so the two agree and neither
   * repaints when this chart's time window changes.
   */
  models?: ModelEarnings[];
}

const WINDOWS = [
  { id: '24h', label: '24 hours' },
  { id: '7d', label: '7 days' },
  { id: '30d', label: '30 days' },
] as const;

function formatTokens(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

function formatUSD(v: number) {
  if (v === 0) return '$0';
  if (v < 0.01) return `$${v.toFixed(5)}`;
  if (v < 1) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(2)}`;
}

interface Segment {
  key: string;
  label: string;
  colour: string;
  usd: number;
  tokensIn: number;
  tokensOut: number;
}

/**
 * Split one interval into stacked segments, largest first.
 *
 * Models beyond the coloured four are summed into a single Other segment rather
 * than stacked individually — a dozen 1px slivers is not a reading of anything.
 * Whatever the split could not account for becomes its own Unattributed
 * segment, so a bar's segments always sum to its total instead of quietly
 * losing the difference.
 */
function segmentsFor(point: EarningsPoint, colours: ReturnType<typeof buildModelColours>): Segment[] {
  const named: Segment[] = [];
  let otherUSD = 0;
  let otherIn = 0;
  let otherOut = 0;

  for (const [model, m] of Object.entries(point.models ?? {})) {
    if (colours.colours.has(model)) {
      named.push({
        key: model,
        label: model,
        colour: colourFor(colours, model),
        usd: m.usd,
        tokensIn: m.tokens_in,
        tokensOut: m.tokens_out,
      });
    } else {
      otherUSD += m.usd;
      otherIn += m.tokens_in;
      otherOut += m.tokens_out;
    }
  }

  named.sort((a, b) => b.usd - a.usd);
  if (otherUSD > 0 || otherIn > 0 || otherOut > 0) {
    named.push({
      key: '__other',
      label: OTHER_LABEL,
      colour: OTHER_COLOUR,
      usd: otherUSD,
      tokensIn: otherIn,
      tokensOut: otherOut,
    });
  }

  const unattributed = point.unattributed ?? 0;
  if (unattributed > 0.000001) {
    named.push({
      key: '__unattributed',
      label: UNATTRIBUTED_LABEL,
      colour: UNATTRIBUTED_COLOUR,
      usd: unattributed,
      tokensIn: 0,
      tokensOut: 0,
    });
  }
  return named;
}

export function EarningsChart({ models }: EarningsChartProps) {
  const [window_, setWindow] = useState<string>('24h');
  const [hovered, setHovered] = useState<number | null>(null);
  const { data, loading, error } = usePolling(
    useCallback(() => api.getEarningsHistory(window_), [window_]),
    60_000,
  );

  const colours = useMemo(() => buildModelColours(models), [models]);
  const points = useMemo(() => data?.points ?? [], [data?.points]);
  const peak = points.reduce((m, p) => Math.max(m, p.usd), 0);
  const activeIndex = hovered ?? (points.length > 0 ? points.length - 1 : null);
  const active = activeIndex !== null ? points[activeIndex] : null;
  const activeSegments = active ? segmentsFor(active, colours) : [];

  // Which models actually appear anywhere in this window — the legend should
  // name what is on screen, not every model the node has ever served.
  const legend = useMemo(() => {
    const present = new Set<string>();
    let sawOther = false;
    let sawUnattributed = false;
    for (const p of points) {
      for (const model of Object.keys(p.models ?? {})) {
        if (colours.colours.has(model)) present.add(model);
        else sawOther = true;
      }
      if ((p.unattributed ?? 0) > 0.000001) sawUnattributed = true;
    }
    const entries = colours.ordered
      .filter((m) => present.has(m))
      .map((m) => ({ key: m, label: m, colour: colourFor(colours, m) }));
    if (sawOther) entries.push({ key: '__other', label: OTHER_LABEL, colour: OTHER_COLOUR });
    if (sawUnattributed) {
      entries.push({ key: '__unattributed', label: UNATTRIBUTED_LABEL, colour: UNATTRIBUTED_COLOUR });
    }
    return entries;
  }, [points, colours]);

  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <div>
          <h3 className="text-sm font-medium text-slate-300">Earnings over time</h3>
          <p className="text-xs text-slate-400">
            {loading && !data
              ? 'Loading…'
              : error && data
                ? `${formatUSD(data.total_usd)} · showing stale data`
                : `${formatUSD(data?.total_usd ?? 0)} in this window`}
          </p>
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

      {error && !data ? (
        <p className="px-4 py-6 text-sm text-amber-300">Could not load earnings history: {error.message}</p>
      ) : points.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">No history for this window yet.</p>
      ) : (
        <div className="px-4 py-4">
          {/* The readout sits above the bars in fixed space rather than
              floating over them: a tooltip that follows the cursor covers the
              neighbouring bars an operator is trying to compare against, and
              reserving the row stops the chart jumping as it appears. The
              latest interval is selected initially so this space is useful
              before the operator interacts with the chart. */}
          <div className="mb-2 h-36" aria-live="polite">
            {active ? (
              <div className="text-xs">
                <div className="flex items-baseline gap-2">
                  <span className="font-mono text-sm text-white">{formatUSD(active.usd)}</span>
                  <span className="text-slate-400">{new Date(active.timestamp).toLocaleString()}</span>
                  {hovered === null && <span className="ml-auto text-slate-400">Latest interval</span>}
                </div>
                {activeSegments.length > 0 ? (
                  <ul className="mt-1 space-y-0.5">
                    {activeSegments.map((s) => (
                      <li key={s.key} className="flex items-center gap-2">
                        <span
                          aria-hidden="true"
                          className="h-2 w-2 shrink-0 rounded-sm"
                          style={{ backgroundColor: s.colour }}
                        />
                        <span className="min-w-0 flex-1 truncate text-slate-300">{s.label}</span>
                        <span className="font-mono text-slate-400">{formatUSD(s.usd)}</span>
                        {s.key !== '__unattributed' && (
                          <span className="w-28 shrink-0 text-right font-mono text-slate-400">
                            {formatTokens(s.tokensIn)} in / {formatTokens(s.tokensOut)} out
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <div className="mt-1 text-slate-400">
                    {formatTokens(active.tokens_in)} in / {formatTokens(active.tokens_out)} out
                    <span className="ml-2 text-slate-400">— recorded before the per-model split</span>
                  </div>
                )}
              </div>
            ) : (
              <div className="text-xs text-slate-400">
                Hover a bar for its models and usage. {points.length} intervals shown.
              </div>
            )}
          </div>

          <div className="flex h-32 items-end gap-px"
               onMouseLeave={() => setHovered(null)}
               role="group"
               aria-label={`Earnings per interval over ${window_}, split by model, totalling ${formatUSD(data?.total_usd ?? 0)}`}>
            {points.map((p, i) => {
              // Against a zero peak every bar would be full height, which reads
              // as a busy period rather than an idle one.
              const h = peak > 0 ? Math.max(2, (p.usd / peak) * 100) : 2;
              const on = activeIndex === i;
              const segments = segmentsFor(p, colours);
              const describe = segments.length
                ? segments.map((s) => `${s.label} ${formatUSD(s.usd)}`).join(', ')
                : `${p.tokens_in.toLocaleString()} in, ${p.tokens_out.toLocaleString()} out`;
              return (
                <button
                  key={p.timestamp}
                  type="button"
                  // Focusable as well as hoverable, so the figures are reachable
                  // without a mouse.
                  onMouseEnter={() => setHovered(i)}
                  onFocus={() => setHovered(i)}
                  onBlur={() => setHovered(null)}
                  aria-label={`${new Date(p.timestamp).toLocaleString()}: ${formatUSD(p.usd)} — ${describe}`}
                  className={`flex h-full flex-1 flex-col justify-end rounded-t focus:outline-none focus:ring-1 focus:ring-blue-400 ${
                    on ? 'ring-1 ring-white/40' : ''
                  }`}
                  style={{ height: `${h}%` }}
                >
                  {segments.length === 0 ? (
                    // No stored split: one neutral bar, so an unattributed
                    // interval is visibly different from a model's colour
                    // rather than being silently attributed to one.
                    <span
                      className="block h-full w-full rounded-t"
                      style={{ backgroundColor: UNATTRIBUTED_COLOUR }}
                    />
                  ) : (
                    segments.map((s, index) => {
                      const share = p.usd > 0 ? (s.usd / p.usd) * 100 : 0;
                      return (
                        <span
                          key={s.key}
                          className={index === 0 ? 'block w-full rounded-t' : 'block w-full'}
                          // A 2px gap between fills keeps touching segments
                          // readable when two of them are close in hue.
                          style={{
                            height: `${share}%`,
                            backgroundColor: s.colour,
                            marginTop: index === 0 ? 0 : 2,
                            opacity: on ? 1 : 0.85,
                          }}
                        />
                      );
                    })
                  )}
                </button>
              );
            })}
          </div>

          <div className="mt-2 flex justify-between text-xs text-slate-400">
            <span>{points[0] && new Date(points[0].timestamp).toLocaleString()}</span>
            <span>{points[points.length - 1] && new Date(points[points.length - 1].timestamp).toLocaleString()}</span>
          </div>

          {legend.length > 0 && (
            <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs" aria-label="Models in this chart">
              {legend.map((entry) => (
                <li key={entry.key} className="flex min-w-0 items-center gap-1.5">
                  <span
                    aria-hidden="true"
                    className="h-2 w-2 shrink-0 rounded-sm"
                    style={{ backgroundColor: entry.colour }}
                  />
                  <span className="break-all text-slate-400">{entry.label}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <p className="flex items-start gap-2 border-t border-slate-800 px-4 py-3 text-xs text-slate-400">
        <AlertCircle aria-hidden="true" size={14} className="mt-px shrink-0" />
        <span>
          This node’s own estimate, priced from its stored history at current rates — not the
          platform’s ledger.
          {(data?.restarts ?? 0) > 0 &&
            ` The counters reset ${data?.restarts} time(s) in this window, so the total is a floor.`}
          {data?.covers && ` History reaches back ${data.covers}.`}
        </span>
      </p>
    </div>
  );
}

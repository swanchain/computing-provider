import { useCallback, useState } from 'react';
import { AlertCircle } from 'lucide-react';
import { api } from '../api/client';
import { usePolling } from '../hooks/usePolling';

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

export function EarningsChart() {
  const [window_, setWindow] = useState<string>('24h');
  const [hovered, setHovered] = useState<number | null>(null);
  const { data, loading, error } = usePolling(
    useCallback(() => api.getEarningsHistory(window_), [window_]),
    60_000,
  );

  const points = data?.points ?? [];
  const peak = points.reduce((m, p) => Math.max(m, p.usd), 0);
  const active = hovered !== null ? points[hovered] : null;

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <div>
          <h3 className="text-sm font-medium text-slate-300">Earnings over time</h3>
          <p className="text-xs text-slate-500">
            {loading && !data ? 'Loading…' : `${formatUSD(data?.total_usd ?? 0)} in this window`}
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
              reserving the row stops the chart jumping as it appears. */}
          <div className="mb-2 flex min-h-[2.5rem] items-start" aria-live="polite">
            {active ? (
              <div className="text-xs">
                <div className="font-mono text-sm text-emerald-300">{formatUSD(active.usd)}</div>
                <div className="text-slate-400">
                  {new Date(active.timestamp).toLocaleString()} · {formatTokens(active.tokens_in)} in
                  {' / '}{formatTokens(active.tokens_out)} out
                </div>
              </div>
            ) : (
              <div className="text-xs text-slate-500">
                Hover a bar for its interval. {points.length} intervals shown.
              </div>
            )}
          </div>

          <div className="flex h-32 items-end gap-px"
               onMouseLeave={() => setHovered(null)}
               role="img"
               aria-label={`Earnings per interval over ${window_}, totalling ${formatUSD(data?.total_usd ?? 0)}`}>
            {points.map((p, i) => {
              // Against a zero peak every bar would be full height, which reads
              // as a busy period rather than an idle one.
              const h = peak > 0 ? Math.max(2, (p.usd / peak) * 100) : 2;
              const on = hovered === i;
              return (
                <button
                  key={p.timestamp}
                  type="button"
                  // Focusable as well as hoverable, so the figures are reachable
                  // without a mouse.
                  onMouseEnter={() => setHovered(i)}
                  onFocus={() => setHovered(i)}
                  onBlur={() => setHovered(null)}
                  aria-label={`${new Date(p.timestamp).toLocaleString()}: ${formatUSD(p.usd)}, ${p.tokens_in.toLocaleString()} in, ${p.tokens_out.toLocaleString()} out`}
                  className={`flex-1 rounded-t transition focus:outline-none focus:ring-1 focus:ring-blue-400 ${
                    on ? 'bg-emerald-300' : 'bg-emerald-500/70 hover:bg-emerald-400'
                  }`}
                  style={{ height: `${h}%` }}
                />
              );
            })}
          </div>
          <div className="mt-2 flex justify-between text-xs text-slate-500">
            <span>{points[0] && new Date(points[0].timestamp).toLocaleString()}</span>
            <span>{points[points.length - 1] && new Date(points[points.length - 1].timestamp).toLocaleString()}</span>
          </div>
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

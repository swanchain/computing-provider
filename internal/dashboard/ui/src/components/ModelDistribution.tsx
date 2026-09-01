import { useState } from 'react';
import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts';
import { AlertCircle } from 'lucide-react';
import type { Earnings } from '../types';

interface ModelDistributionProps {
  earnings: Earnings | null;
  loading: boolean;
}

type Metric = 'earnings' | 'tokens';

// Distinguishable at small sizes and in sequence, rather than a gradient where
// adjacent slices are hard to tell apart.
const COLOURS = ['#34d399', '#60a5fa', '#a78bfa', '#fbbf24', '#f87171', '#22d3ee', '#f472b6', '#a3e635'];

function formatUSD(v: number) {
  if (v === 0) return '$0.00';
  if (v < 0.01) return `$${v.toFixed(5)}`;
  if (v < 1) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(2)}`;
}

function formatTokens(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

export function ModelDistribution({ earnings, loading }: ModelDistributionProps) {
  const [metric, setMetric] = useState<Metric>('earnings');
  const [active, setActive] = useState<number | null>(null);

  const rows = (earnings?.models ?? [])
    .map((m) => ({
      model: m.model,
      // Unpriced models contribute nothing to an earnings split — showing them
      // as a zero slice would imply they served nothing, which is not what
      // "we could not price this" means. They still appear by token share.
      value: metric === 'earnings' ? m.total_usd : m.tokens_in + m.tokens_out,
      usd: m.total_usd,
      tokens: m.tokens_in + m.tokens_out,
      priced: m.priced,
    }))
    .filter((r) => r.value > 0)
    .sort((a, b) => b.value - a.value);

  const total = rows.reduce((s, r) => s + r.value, 0);
  const unpriced = (earnings?.models ?? []).filter((m) => !m.priced).length;

  if (loading && !earnings) {
    return <div className="h-64 animate-pulse rounded-xl bg-slate-800/50" />;
  }

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <h3 className="text-sm font-medium text-slate-300">Share by model</h3>
        <div className="flex gap-1" role="group" aria-label="Distribute by">
          {(['earnings', 'tokens'] as Metric[]).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMetric(m)}
              aria-pressed={metric === m}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium capitalize transition focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                metric === m ? 'bg-slate-700 text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
              }`}
            >
              {m}
            </button>
          ))}
        </div>
      </div>

      {rows.length === 0 ? (
        <p className="px-4 py-8 text-sm text-slate-400">
          No {metric === 'earnings' ? 'priced earnings' : 'traffic'} recorded since the node started.
        </p>
      ) : (
        <div className="flex flex-col gap-4 px-4 py-4 sm:flex-row sm:items-center">
          <div className="h-40 w-40 shrink-0 self-center">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={rows}
                  dataKey="value"
                  nameKey="model"
                  innerRadius="55%"
                  outerRadius="100%"
                  paddingAngle={1}
                  stroke="none"
                  isAnimationActive={false}
                  onMouseEnter={(_, i) => setActive(i)}
                  onMouseLeave={() => setActive(null)}
                >
                  {rows.map((_, i) => (
                    <Cell key={i} fill={COLOURS[i % COLOURS.length]} opacity={active === null || active === i ? 1 : 0.35} />
                  ))}
                </Pie>
              </PieChart>
            </ResponsiveContainer>
          </div>

          {/* The legend carries the figures rather than a floating tooltip: with
              eight models the labels will not fit on the slices, and a reader
              should not have to hover each one to learn what it is. */}
          <ul className="min-w-0 flex-1 space-y-1">
            {rows.map((r, i) => {
              const pct = total > 0 ? (r.value / total) * 100 : 0;
              return (
                <li
                  key={r.model}
                  onMouseEnter={() => setActive(i)}
                  onMouseLeave={() => setActive(null)}
                  className={`flex items-center gap-2 rounded px-1 py-0.5 text-xs transition ${active === i ? 'bg-slate-800' : ''}`}
                >
                  <span className="block h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: COLOURS[i % COLOURS.length] }} />
                  <span className="min-w-0 flex-1 truncate font-mono text-slate-300" title={r.model}>{r.model}</span>
                  <span className="shrink-0 tabular-nums text-slate-400">{pct.toFixed(1)}%</span>
                  <span className="w-20 shrink-0 text-right tabular-nums text-slate-500">
                    {metric === 'earnings' ? formatUSD(r.usd) : formatTokens(r.tokens)}
                  </span>
                </li>
              );
            })}
          </ul>
        </div>
      )}

      <p className="flex items-start gap-2 border-t border-slate-800 px-4 py-3 text-xs text-slate-400">
        <AlertCircle aria-hidden="true" size={14} className="mt-px shrink-0" />
        <span>
          Share of traffic served since this node last started — its counters reset on restart, so
          this is the recent mix rather than an all-time split. Probes are excluded.
          {unpriced > 0 && metric === 'earnings' &&
            ` ${unpriced} model(s) had no rate available and are absent from the earnings split; switch to tokens to see them.`}
        </span>
      </p>
    </div>
  );
}

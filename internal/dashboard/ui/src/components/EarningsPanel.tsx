import { AlertCircle, Coins } from 'lucide-react';
import type { Earnings } from '../types';

interface EarningsPanelProps {
  earnings: Earnings | null;
  loading: boolean;
  error?: Error | null;
}

// Sub-cent sums are common on a quiet day, and rounding them to $0.00 makes a
// working provider look like it earned nothing. Small amounts get more places.
function formatUSD(v: number) {
  if (v === 0) return '$0.00';
  if (v < 0.01) return `$${v.toFixed(5)}`;
  if (v < 1) return `$${v.toFixed(4)}`;
  return `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatTokens(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

export function EarningsPanel({ earnings, loading, error }: EarningsPanelProps) {
  if (loading && !earnings) {
    return <div className="h-32 animate-pulse rounded-xl bg-slate-800/50" />;
  }
  if (error && !earnings) {
    return (
      <div className="rounded-xl border border-amber-800 bg-amber-900/20 p-4 text-sm text-amber-300">
        Earnings are unavailable: {error.message}
      </div>
    );
  }
  const rows = earnings?.models ?? [];

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-slate-800 px-4 py-4">
        <div className="flex items-center gap-2">
          <Coins aria-hidden="true" size={18} className="text-emerald-400" />
          <h3 className="text-sm font-medium text-slate-300">Earned from served traffic</h3>
        </div>
        <div className="font-mono text-2xl font-semibold text-emerald-300">
          {formatUSD(earnings?.total_usd ?? 0)}
        </div>
      </div>

      {rows.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">No served traffic yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs uppercase tracking-wide text-slate-400">
                <th className="px-4 py-2 text-left font-medium">Model</th>
                <th className="px-4 py-2 text-right font-medium">Input</th>
                <th className="px-4 py-2 text-right font-medium">Output</th>
                <th className="px-4 py-2 text-right font-medium">Earned</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((m) => (
                <tr key={m.model} className="border-b border-slate-800/70 last:border-0">
                  <td className="max-w-xs px-4 py-2">
                    <span className="block truncate font-mono text-xs text-slate-200" title={m.model}>{m.model}</span>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-xs text-blue-200">{formatTokens(m.tokens_in)}</td>
                  <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-xs text-violet-200">{formatTokens(m.tokens_out)}</td>
                  <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-sm">
                    {m.priced ? (
                      <span className="text-emerald-300">{formatUSD(m.total_usd)}</span>
                    ) : (
                      // Not $0.00: this model served work we could not price,
                      // which is a different statement from earning nothing.
                      <span className="text-slate-500" title="No rate available for this model">unpriced</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="flex items-start gap-2 border-t border-slate-800 px-4 py-3 text-xs text-slate-400">
        <AlertCircle aria-hidden="true" size={14} className="mt-px shrink-0" />
        <span>
          This node’s own reckoning, from tokens it served at provider payout rates. Health and
          self-check probes are excluded. It is not a statement of account — the platform’s figure
          is authoritative, and a difference between the two is worth asking about.
          {(earnings?.unpriced_models ?? 0) > 0 && ` ${earnings?.unpriced_models} model(s) had no rate available.`}
        </span>
      </p>
    </div>
  );
}

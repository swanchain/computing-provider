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
  const platform = earnings?.platform;

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="border-b border-slate-800 px-4 py-4">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <div className="flex items-center gap-2">
            <Coins aria-hidden="true" size={18} className="text-emerald-400" />
            <h3 className="text-sm font-medium text-slate-300">Total earned</h3>
          </div>
          {platform?.unavailable ? (
            // Never fall back to the session figure here. It covers one process
            // and would be read as lifetime earnings, which is a worse error
            // than admitting the platform could not be reached.
            <span className="text-sm text-amber-300" title={platform.unavailable}>unavailable</span>
          ) : (
            <div className="font-mono text-3xl font-semibold text-emerald-300">
              {formatUSD(platform?.total_usd ?? 0)}
            </div>
          )}
        </div>
        <p className="mt-1 text-xs text-slate-400">
          {platform?.unavailable
            ? `Swan Inference could not be reached: ${platform.unavailable}`
            : `Lifetime, from Swan Inference · ${platform ? formatTokens(platform.total_tokens) : '0'} tokens over ${platform?.total_inferences?.toLocaleString() ?? 0} requests`}
        </p>
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
                <th className="px-4 py-2 text-right font-medium">This session</th>
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
            <tfoot>
              <tr className="border-t border-slate-700">
                <td className="px-4 py-2 text-xs uppercase tracking-wide text-slate-400" colSpan={3}>This session</td>
                <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-sm text-slate-300">
                  {formatUSD(earnings?.session_usd ?? 0)}
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}

      <p className="flex items-start gap-2 border-t border-slate-800 px-4 py-3 text-xs text-slate-400">
        <AlertCircle aria-hidden="true" size={14} className="mt-px shrink-0" />
        <span>
          The table is this node’s own reckoning <strong className="text-slate-300">since it last
          started</strong> — its counters reset on restart, so it will read far below the lifetime
          total above. Probes are excluded. Useful for spotting a new divergence, not for knowing
          what you have earned.
          {(earnings?.unpriced_models ?? 0) > 0 && ` ${earnings?.unpriced_models} model(s) had no rate available.`}
        </span>
      </p>
    </div>
  );
}

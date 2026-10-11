import { useCallback } from 'react';
import { api } from '../api/client';
import { usePolling } from '../hooks/usePolling';
import type { ModelStatus, ProxyAccount, ProxyAccountsReport, ProxyInstance, ProxyQuotaWindow } from '../types';

/**
 * The subscription accounts behind each CLIProxyAPI instance.
 *
 * Every outage of a proxied model so far has been an account problem — a
 * credential that stopped refreshing, a usage limit reached, a cooldown held
 * after the limit reset — and none of it shows in model health, because the
 * proxy keeps answering throughout. This shows each account's usage windows
 * as the upstream last reported them, so a limit is seen coming rather than
 * found from failed requests.
 */

const PROXY_POLL_MS = 60_000;

const ROLE_LABEL: Record<string, string> = {
  hub: 'Hub traffic',
  local: 'Local gateway',
};

function formatCount(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return v.toLocaleString();
}

function formatIn(iso?: string) {
  if (!iso) return '';
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000;
  const abs = Math.abs(seconds);
  const text = abs < 5400 ? `${Math.round(abs / 60)}m` : abs < 172_800 ? `${Math.round(abs / 3600)}h` : `${Math.round(abs / 86_400)}d`;
  return seconds >= 0 ? `in ${text}` : `${text} ago`;
}

function isPast(iso?: string) {
  return Boolean(iso) && new Date(iso as string).getTime() < Date.now();
}

function formatPlan(plan?: string) {
  if (!plan) return null;
  return plan.replace(/_/g, ' ');
}

/** A usage window as a bar. Colour follows the glyph, never stands alone. */
function WindowBar({ w }: { w: ProxyQuotaWindow }) {
  const pct = Math.max(0, Math.min(100, w.used_percent));
  const level = pct >= 90 ? 'high' : pct >= 70 ? 'warn' : 'ok';
  const bar = level === 'high' ? 'bg-red-400' : level === 'warn' ? 'bg-amber-400' : 'bg-emerald-400';
  const glyph = level === 'high' ? '✗' : level === 'warn' ? '⚠' : '✓';
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-[11px] text-slate-400">
        <span>
          {glyph} {w.label} window
        </span>
        <span className="tabular-nums text-slate-300">
          {pct.toFixed(0)}% used{w.reset_at && <span className="text-slate-500"> · resets {formatIn(w.reset_at)}</span>}
        </span>
      </div>
      <div
        className="mt-1 h-1.5 overflow-hidden rounded bg-slate-700"
        role="meter"
        aria-label={`${w.label} usage window`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
      >
        <div className={`h-full ${bar}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

/**
 * A cooldown on one model is not the account cooling down: a plan that does
 * not include a model, or has spent that model's quota, holds back that model
 * alone while the rest serve normally. Only one with no model, or the proxy
 * marking the whole account unavailable, makes the account itself unusable.
 */
function accountWideCooldown(a: ProxyAccount) {
  return a.unavailable || a.cooldowns.some((c) => !c.model);
}

function AccountStatus({ a }: { a: ProxyAccount }) {
  const expired = isPast(a.expires);
  if (a.disabled) {
    return <span className="rounded bg-slate-700/60 px-1.5 py-0.5 text-[11px] text-slate-400">○ disabled</span>;
  }
  if (expired) {
    return <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-[11px] text-red-300">✗ token expired</span>;
  }
  if (accountWideCooldown(a)) {
    return <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[11px] text-amber-300">⚠ cooling down</span>;
  }
  const held = a.cooldowns.length;
  return (
    <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[11px] text-emerald-300">
      ✓ {a.status || 'active'}
      {held > 0 && <span className="text-amber-300"> · {held} model{held === 1 ? '' : 's'} held back</span>}
    </span>
  );
}

function Account({ a }: { a: ProxyAccount }) {
  const plan = formatPlan(a.plan);
  const expired = isPast(a.expires);
  return (
    <li className="px-4 py-3 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="min-w-0 break-all text-sm text-slate-200">
          {a.account}
          {plan && <span className="ml-2 rounded bg-slate-700/60 px-1.5 py-0.5 text-[11px] text-slate-300">{plan}</span>}
        </span>
        <AccountStatus a={a} />
      </div>

      {!a.disabled && a.windows.length > 0 && (
        <div className="mt-2 grid gap-2 sm:grid-cols-2">
          {a.windows.map((w) => (
            <WindowBar key={w.label} w={w} />
          ))}
        </div>
      )}
      {!a.disabled && a.windows.length === 0 && (
        <p className="mt-1.5 text-slate-500">
          {a.provider === 'codex'
            ? 'No usage reported yet — it appears after the account serves a request.'
            : 'This provider does not report usage windows to the proxy.'}
        </p>
      )}

      {a.cooldowns.length > 0 && (
        <ul className="mt-2 space-y-0.5 text-amber-200">
          {a.cooldowns.map((c, i) => (
            <li key={`${c.model}-${i}`}>
              ⚠ {c.model || 'all models'} held back{c.reason && ` (${c.reason.replace(/_/g, ' ')})`} · retries {formatIn(c.retry_at)}
            </li>
          ))}
        </ul>
      )}

      <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-slate-400">
        <span>
          {a.success.toLocaleString()} ok / {a.failed.toLocaleString()} failed
          <span className="text-slate-500"> since proxy start</span>
        </span>
        {a.expires && !a.disabled && (
          <span className={expired ? 'text-red-300' : undefined}>
            {expired
              ? `✗ token expired ${formatIn(a.expires)} — refreshing has stopped; log in again`
              : `access token renews automatically before ${new Date(a.expires).toLocaleString([], { dateStyle: 'short', timeStyle: 'short' })}`}
          </span>
        )}
        {a.quota_observed_at && a.windows.length > 0 && (
          <span className="text-slate-500">usage as of {formatIn(a.quota_observed_at)}</span>
        )}
      </div>
    </li>
  );
}

function Instance({ inst }: { inst: ProxyInstance }) {
  return (
    <section className="border-b border-slate-800 last:border-b-0">
      <div className="flex flex-wrap items-center justify-between gap-2 bg-slate-800/40 px-4 py-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="break-all font-mono text-xs text-slate-300">{inst.endpoint}</span>
          {inst.roles.map((r) => (
            <span key={r} className="rounded bg-sky-500/15 px-1.5 py-0.5 text-[11px] text-sky-300">
              {ROLE_LABEL[r] ?? r}
            </span>
          ))}
        </div>
        <span className="text-[11px] text-slate-400">
          {inst.measured_24h
            ? `24h: ${formatCount(inst.tokens_in_24h)} in · ${formatCount(inst.tokens_out_24h)} out`
            : '24h usage not measured yet'}
        </span>
      </div>
      {inst.error ? (
        <p role="alert" className="px-4 py-3 text-xs text-red-300">
          ✗ Could not read this proxy: {inst.error}
        </p>
      ) : inst.accounts.length === 0 ? (
        <p className="px-4 py-3 text-xs text-amber-300">⚠ No accounts — every request to this proxy will fail.</p>
      ) : (
        <ul className="divide-y divide-slate-800">
          {inst.accounts.map((a) => (
            <Account key={a.account} a={a} />
          ))}
        </ul>
      )}
    </section>
  );
}

function useProxyAccounts() {
  return usePolling(useCallback(() => api.getProxyAccounts(), []), PROXY_POLL_MS);
}

/** What needs the operator's attention, one line each; empty when nothing does. */
function proxyIssues(report: ProxyAccountsReport | null | undefined): string[] {
  const out: string[] = [];
  for (const inst of report?.instances ?? []) {
    if (inst.error) {
      out.push(`${inst.endpoint} could not be read`);
      continue;
    }
    if (inst.accounts.length === 0) out.push(`${inst.endpoint} has no accounts`);
    for (const a of inst.accounts) {
      if (a.disabled) continue;
      if (isPast(a.expires)) out.push(`${a.account}: token expired`);
      else if (accountWideCooldown(a)) out.push(`${a.account}: cooling down`);
      for (const w of a.windows) {
        if (w.used_percent >= 90) out.push(`${a.account}: ${w.label} window ${w.used_percent.toFixed(0)}% used`);
      }
    }
  }
  return out;
}

/** A one-line warning for the overview, shown only when an account needs attention. */
export function ProxyAlert({ onOpen }: { onOpen: () => void }) {
  const { data } = useProxyAccounts();
  const issues = proxyIssues(data);
  if (issues.length === 0) return null;
  return (
    <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
      <span>⚠ Proxy accounts need attention: {issues.join('; ')}</span>
      <button type="button" onClick={onOpen} className="rounded border border-amber-400/40 px-2 py-1 text-xs text-amber-100 hover:bg-amber-500/20">
        Open Proxies
      </button>
    </div>
  );
}

/**
 * Which backend each proxied model uses for routed and for local work. Only
 * models with a separate local backend are listed; the rest use one backend
 * for both, shown on the Overview.
 */
export function ProxyRoutingPanel({ models }: { models: ModelStatus[] }) {
  const split = models.filter((m) => m.local_endpoint).sort((a, b) => a.id.localeCompare(b.id));
  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="border-b border-slate-800 px-4 py-3">
        <h3 className="text-sm font-medium text-slate-300">Model routing</h3>
        <p className="text-xs text-slate-400">
          Hub requests go to the model’s endpoint; requests to the local gateway (127.0.0.1:9088) go to its local backend.
        </p>
      </div>
      {split.length === 0 ? (
        <p className="px-4 py-6 text-sm text-slate-400">
          No model has a separate local backend. Set <span className="font-mono">local_endpoint</span> in models.json to give one.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-slate-400">
              <tr className="border-b border-slate-800">
                <th scope="col" className="px-4 py-2 font-medium">Model</th>
                <th scope="col" className="px-4 py-2 font-medium">Hub traffic</th>
                <th scope="col" className="px-4 py-2 font-medium">Local gateway</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800">
              {split.map((m) => (
                <tr key={m.id}>
                  <td className="break-all px-4 py-2 font-mono text-slate-200">{m.id}</td>
                  <td className="px-4 py-2 text-slate-300">
                    {m.local_only ? (
                      <span className="text-slate-400">○ not offered — local only</span>
                    ) : (
                      <>
                        <span className="font-mono">{m.endpoint}</span>{' '}
                        <span className={m.health_string === 'healthy' ? 'text-green-300' : 'text-red-300'}>
                          {m.health_string === 'healthy' ? '✓' : '✗'} {m.health_string}
                        </span>
                      </>
                    )}
                  </td>
                  <td className="px-4 py-2 text-slate-300" title={m.local_error || undefined}>
                    <span className="font-mono">{m.local_endpoint}</span>{' '}
                    <span
                      className={
                        m.local_health === 'healthy' ? 'text-green-300' : m.local_health === 'unreachable' ? 'text-red-300' : 'text-slate-400'
                      }
                    >
                      {m.local_health === 'healthy' ? '✓ reachable' : m.local_health === 'unreachable' ? `✗ ${m.local_error || 'unreachable'}` : '… not checked yet'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function ProxyAccountsPanel() {
  const { data, loading, error } = useProxyAccounts();
  const instances = data?.instances ?? [];

  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="border-b border-slate-800 px-4 py-3">
        <h3 className="text-sm font-medium text-slate-300">Proxy accounts</h3>
        <p className="text-xs text-slate-400">
          The subscription accounts behind each CLIProxyAPI instance, and how much of each usage window is spent.
        </p>
      </div>
      {loading && !data ? (
        <p className="px-4 py-6 text-sm text-slate-400">Loading…</p>
      ) : error && !data ? (
        <p className="px-4 py-6 text-sm text-amber-300">Could not load proxy accounts: {error.message}</p>
      ) : data?.note ? (
        <p className="px-4 py-6 text-sm text-slate-400">{data.note}</p>
      ) : (
        instances.length === 0 ? (
          <p className="px-4 py-6 text-sm text-slate-400">None of this node’s models is served through a CLIProxyAPI instance.</p>
        ) : (
          instances.map((inst) => <Instance key={inst.endpoint} inst={inst} />)
        )
      )}
    </div>
  );
}

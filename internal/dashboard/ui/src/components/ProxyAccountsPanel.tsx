import { useState } from 'react';
import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts';
import type { UsePollingResult } from '../hooks/usePolling';
import { MAX_COLOURED_MODELS, OTHER_COLOUR, OTHER_LABEL, SERIES_COLOURS } from '../lib/modelPalette';
import { accountWideCooldown, isPast, providerName } from '../lib/proxyStatus';
import type { ModelStatus, ProxyAccount, ProxyAccountsReport, ProxyInstance, ProxyQuotaWindow } from '../types';

function dateLabel(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return 'time unknown';
  return new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' });
}

// The proxy learns an account's usage only from the headers on responses it
// relays, so the figure stops moving whenever this proxy is idle — while the
// same account can still be used elsewhere. Past this age it is flagged.
const QUOTA_STALE_MS = 30 * 60 * 1000;

function ageLabel(value?: string) {
  const ms = value ? Date.now() - Date.parse(value) : NaN;
  if (!Number.isFinite(ms)) return 'time unknown';
  const mins = Math.max(0, Math.floor(ms / 60000));
  if (mins < 1) return 'just now';
  if (mins < 60) return mins + 'm ago';
  const hours = Math.floor(mins / 60);
  if (hours < 48) return hours + 'h ' + (mins % 60) + 'm ago';
  return Math.floor(hours / 24) + 'd ago';
}

function quotaStale(value?: string) {
  const t = value ? Date.parse(value) : NaN;
  return !Number.isFinite(t) || Date.now() - t > QUOTA_STALE_MS;
}

function compactCount(n: number) {
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);
}

function WindowBar({ w, stale }: { w: ProxyQuotaWindow; stale: boolean }) {
  const pct = Math.max(0, Math.min(100, w.used_percent));
  // A window that has reset since the report says nothing about the current one.
  if (stale && w.reset_at && isPast(w.reset_at)) return <div className="min-w-0 text-sm">
    <p className="text-slate-300">{w.label} usage</p>
    <p className="mt-1 text-xs text-slate-400">Reset {dateLabel(w.reset_at)}, after the last report. Current usage is unknown until this account serves a request.</p>
  </div>;
  return <div className="min-w-0">
    <div className="mb-1 flex flex-wrap justify-between gap-x-3 text-sm">
      <span className="text-slate-300">{w.label} usage</span>
      <span className={pct >= 90 ? 'text-amber-200' : 'text-slate-200'}>{stale ? 'at least ' : ''}{pct.toFixed(0)}% used{pct >= 90 ? ' · near limit' : ''}</span>
    </div>
    <div role="meter" aria-label={w.label + ' usage'} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="h-2 overflow-hidden rounded bg-slate-700">
      <div className={'h-full ' + (pct >= 90 ? 'bg-amber-400' : 'bg-emerald-400') + (stale ? ' opacity-50' : '')} style={{ width: pct + '%' }} />
    </div>
    {w.reset_at && <p className="mt-1 text-xs text-slate-400">Resets {dateLabel(w.reset_at)}</p>}
  </div>;
}

function Account({ a, modelIds }: { a: ProxyAccount; modelIds: string[] }) {
  const expired = isPast(a.expires);
  const unavailable = accountWideCooldown(a);
  const claude = a.provider === 'claude' || a.provider === 'anthropic';
  const stale = quotaStale(a.quota_observed_at);
  const status = a.disabled ? 'Disabled' : expired ? 'Check sign-in' : unavailable ? 'Temporarily unavailable' : a.status === 'active' ? 'Signed in' : a.status || 'Status unknown';
  const style = a.disabled ? 'bg-slate-800 text-slate-400' : expired || unavailable ? 'bg-amber-500/10 text-amber-200' : 'bg-emerald-500/10 text-emerald-200';
  return <li className="min-w-0 p-4 sm:p-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h4 className="text-base font-semibold text-white">{providerName(a.provider)}</h4>
        <p className="mt-1 break-all text-sm text-slate-300">{a.account}</p>
        {a.plan && <p className="mt-1 break-words text-xs text-slate-400">Plan: {a.plan.replace(/_/g, ' ')}</p>}
      </div>
      <span className={'rounded-full px-3 py-1 text-xs font-medium ' + style}>{status}</span>
    </div>
    {!a.disabled && <div className="mt-4">
      {a.windows.length ? <div className="grid gap-4 xl:grid-cols-2">{a.windows.map((w) => <WindowBar key={w.label} w={w} stale={stale} />)}</div> :
        <p className="text-sm leading-6 text-slate-400">{claude ? <>Subscription usage isn’t available here. <a className="text-sky-300 underline underline-offset-4" href="https://claude.ai/settings/usage" target="_blank" rel="noreferrer">View usage in Claude ↗</a></> : a.provider === 'codex' ? 'Usage has not been reported yet. It appears after this account serves a request.' : 'This provider does not report subscription usage to the proxy.'}</p>}
      {a.windows.length > 0 && (stale
        ? <p className="mt-2 text-xs leading-5 text-amber-200">⚠ Usage last reported {ageLabel(a.quota_observed_at)} ({dateLabel(a.quota_observed_at)}). It updates only when this proxy serves a request, so any use of this account elsewhere since then is not counted.</p>
        : <p className="mt-2 text-xs text-slate-400">Usage last reported {ageLabel(a.quota_observed_at)} ({dateLabel(a.quota_observed_at)})</p>)}
    </div>}
    {!!a.cooldowns.length && <div className="mt-4 rounded-lg border border-amber-800/50 bg-amber-950/20 p-3">
      <p className="text-sm font-medium text-amber-200">{unavailable ? 'Account restriction' : 'Model restrictions'}</p>
      <ul className="mt-2 space-y-2 text-sm text-amber-100">
        {a.cooldowns.map((c, i) => {
          const listed = !c.model || modelIds.some((id) => id === c.model || id.split('/').slice(1).join('/') === c.model);
          return <li key={i}><p className="break-words">{c.model || 'All models'} · {c.reason === 'quota' ? 'quota blocked' : c.reason?.replace(/_/g, ' ') || 'temporarily restricted'}</p>
            {!listed && <p className="mt-1 text-xs text-slate-300">No matching configured model ID. Check the full model list under Token consumption.</p>}
            <p className="mt-1 text-xs text-slate-400">Proxy retry: {dateLabel(c.retry_at)}</p></li>;
        })}
      </ul>
    </div>}
    {expired && !a.disabled && <p className="mt-3 text-sm text-amber-200">Stored access token has expired. Check whether automatic renewal is working; sign in again if requests fail.</p>}
    <details className="mt-3 text-sm text-slate-400">
      <summary className="min-h-10 cursor-pointer py-2 focus-visible:outline focus-visible:outline-sky-400">Account details</summary>
      <div className="space-y-2 border-t border-slate-800 pt-3">
        <p>{a.success.toLocaleString()} successful / {a.failed.toLocaleString()} failed requests · since proxy restart</p>
        {a.expires && !a.disabled && <p>Access token {expired ? 'expired' : 'expires'} {dateLabel(a.expires)}. Automatic renewal is handled by the proxy.</p>}
        {a.status_message && <p className="break-words">{a.status_message}</p>}
      </div>
    </details>
  </li>;
}

function Instance({ inst }: { inst: ProxyInstance }) {
  const hub = inst.roles.includes('hub');
  const local = inst.roles.includes('local');
  const title = hub && local ? 'Hub and local proxy' : hub ? 'Hub proxy' : 'Local proxy';
  const enabled = inst.accounts.filter((a) => !a.disabled);
  const disabled = inst.accounts.filter((a) => a.disabled);
  return <section className="min-w-0 overflow-hidden rounded-xl border border-slate-700 bg-slate-900/60">
    <div className="border-b border-slate-800 bg-slate-800/40 p-4 sm:p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 className="text-lg font-semibold text-white">{title}</h3>
        <span className="break-all font-mono text-xs text-sky-200">{inst.endpoint}</span>
      </div>
      <p className="mt-1 text-sm text-slate-300">{hub && local ? 'Serves hub requests and your own clients.' : hub ? 'Serves requests routed by Swan Inference.' : 'Serves your own clients through the client gateway.'}</p>
      <p className="mt-3 text-sm text-slate-300">{enabled.length} enabled account{enabled.length === 1 ? '' : 's'} · {inst.models.length} configured models</p>
      <div className="mt-4 border-t border-slate-700 pt-4 text-sm">
        <p className="text-xs text-slate-400">Last 24 hours · {inst.measured_24h ? compactCount(inst.tokens_in_24h) + ' input / ' + compactCount(inst.tokens_out_24h) + ' output tokens' : 'token usage not measured yet'}</p>
      </div>
    </div>
    {inst.error ? <p role="alert" className="break-words p-5 text-sm text-amber-200">Account status unavailable: {inst.error}</p> : <>
      {!inst.accounts.length && <p className="p-5 text-sm text-amber-200">No accounts configured. Requests through this proxy will fail.</p>}
      {!!inst.accounts.length && !enabled.length && <p className="p-5 text-sm text-amber-200">All accounts are disabled. This proxy cannot serve requests.</p>}
      <ul className="divide-y divide-slate-800">{enabled.map((a) => <Account key={a.provider + ':' + a.account} a={a} modelIds={inst.models} />)}</ul>
      {!!disabled.length && <details className="border-t border-slate-800">
        <summary className="min-h-11 cursor-pointer px-5 py-3 text-sm text-slate-400">{disabled.length} disabled account{disabled.length === 1 ? '' : 's'}</summary>
        <ul>{disabled.map((a) => <Account key={a.provider + ':' + a.account} a={a} modelIds={inst.models} />)}</ul>
      </details>}
    </>}
  </section>;
}

interface UsageRow { model: string; tokensIn: number; tokensOut: number; total: number; configured: boolean }

function usageRows(inst: ProxyInstance): UsageRow[] {
  const usage = new Map((inst.model_usage_24h ?? []).filter((r) => r.measured_24h).map((r) => [r.model, r]));
  const configured = new Set(inst.models);
  return [...new Set([...inst.models, ...usage.keys()])].map((model) => {
    const r = usage.get(model);
    const tokensIn = r?.tokens_in_24h ?? 0;
    const tokensOut = r?.tokens_out_24h ?? 0;
    return { model, tokensIn, tokensOut, total: tokensIn + tokensOut, configured: configured.has(model) };
  }).sort((a, b) => b.total - a.total || a.model.localeCompare(b.model));
}

function UsagePie({ inst }: { inst: ProxyInstance }) {
  const [active, setActive] = useState<string | null>(null);
  const hub = inst.roles.includes('hub');
  const local = inst.roles.includes('local');
  const title = hub && local ? 'Hub and local proxy' : hub ? 'Hub proxy' : 'Local proxy';
  const rows = usageRows(inst);
  const used = rows.filter((r) => r.total > 0);
  // Ranked within this proxy: the two proxies serve different mixes, and a
  // shared ranking left most of one chart in Other. Past the palette's four
  // hues the rest fold into Other, as on the earnings charts.
  const colours = new Map<string, string>(used.slice(0, MAX_COLOURED_MODELS).map((r, i) => [r.model, SERIES_COLOURS[i]]));
  const slices = used.filter((r) => colours.has(r.model)).map((r) => ({ name: r.model, value: r.total, fill: colours.get(r.model)! }));
  const otherTotal = used.filter((r) => !colours.has(r.model)).reduce((s, r) => s + r.total, 0);
  if (otherTotal > 0) slices.push({ name: OTHER_LABEL, value: otherTotal, fill: OTHER_COLOUR });
  const total = used.reduce((s, r) => s + r.total, 0);
  return <section className="min-w-0 rounded-xl border border-slate-700 bg-slate-900/60 p-4">
    <div className="flex flex-wrap items-baseline justify-between gap-x-3">
      <h3 className="text-base font-semibold text-white">{title}</h3>
      <p className="text-xs text-slate-400">{inst.measured_24h ? compactCount(inst.tokens_in_24h) + ' in / ' + compactCount(inst.tokens_out_24h) + ' out' : 'not measured yet'}</p>
    </div>
    {inst.model_usage_24h === undefined ? <p className="mt-3 text-xs text-slate-400">Per-model counts are available after updating this provider.</p> : !total ? <p className="mt-3 text-sm text-slate-400">No tokens through this proxy in the last 24 hours.</p> :
      <div className="mt-3 flex flex-col gap-4 sm:flex-row sm:items-center">
        <div className="relative h-36 w-36 shrink-0 self-center">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie data={slices} dataKey="value" nameKey="name" innerRadius="60%" outerRadius="100%" paddingAngle={1} stroke="none" isAnimationActive={false}
                onMouseEnter={(_, i) => setActive(slices[i]?.name ?? null)} onMouseLeave={() => setActive(null)}>
                {slices.map((s) => <Cell key={s.name} fill={s.fill} opacity={active === null || active === s.name ? 1 : 0.35} />)}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
            <span className="text-lg font-semibold tabular-nums text-white">{compactCount(total)}</span>
            <span className="text-[11px] text-slate-400">tokens</span>
          </div>
        </div>
        <ul className="min-w-0 flex-1 space-y-1">
          {slices.map((s) => <li key={s.name} onMouseEnter={() => setActive(s.name)} onMouseLeave={() => setActive(null)}
            className={'flex items-start gap-2 rounded px-1 py-0.5 text-xs ' + (active === s.name ? 'bg-slate-800' : '')}>
            <span className="mt-0.5 block h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: s.fill }} />
            <span className="min-w-0 flex-1 break-all font-mono text-slate-300">{s.name === OTHER_LABEL ? OTHER_LABEL + ' (' + (used.length - slices.length + 1) + ' models)' : s.name}</span>
            <span className="shrink-0 tabular-nums text-slate-300">{((s.value / total) * 100).toFixed(1)}%</span>
            <span className="w-14 shrink-0 text-right tabular-nums text-slate-400">{compactCount(s.value)}</span>
          </li>)}
        </ul>
      </div>}
    {!!rows.length && <details className="mt-3 text-xs">
      <summary className="min-h-10 cursor-pointer py-2 text-slate-400 focus-visible:outline focus-visible:outline-sky-400">All {rows.length} models · input / output</summary>
      <table className="w-full border-t border-slate-800">
        <tbody>{rows.map((r) => <tr key={r.model} className="border-b border-slate-800/70">
          <td className="break-all py-1.5 pr-2 font-mono text-slate-300">{r.model}{!r.configured && <span className="ml-2 font-sans text-slate-400">not configured here</span>}</td>
          <td className="whitespace-nowrap py-1.5 text-right tabular-nums text-slate-400">{r.total ? compactCount(r.tokensIn) + ' / ' + compactCount(r.tokensOut) : '—'}</td>
        </tr>)}</tbody>
      </table>
    </details>}
  </section>;
}

// Gateway backends are not probed; their status is the last request the local
// gateway sent them.
function localBackendLabel(health?: string): string {
  if (health === 'healthy') return 'last request answered';
  if (health === 'unreachable') return 'last request failed';
  return 'no requests yet';
}

export function ProxyUsageCharts({ instances }: { instances: ProxyInstance[] }) {
  return <div>
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3">
      <h3 className="text-base font-semibold text-white">Token consumption by model</h3>
      <p className="text-xs text-slate-400">Last 24 hours · all clients through each proxy · separate from subscription limits</p>
    </div>
    <div className={'grid items-start gap-5 ' + (instances.length > 1 ? 'lg:grid-cols-2' : '')}>{instances.map((inst) => <UsagePie key={inst.endpoint} inst={inst} />)}</div>
  </div>;
}

export function ProxyRoutingPanel({ models }: { models: ModelStatus[] }) {
  const split = models.filter((m) => m.local_endpoint || m.local_only).sort((a, b) => Number(!!b.local_only) - Number(!!a.local_only) || a.id.localeCompare(b.id));
  return <details className="rounded-xl border border-slate-700 bg-slate-900/60">
    <summary className="min-h-12 cursor-pointer p-4 text-base font-semibold text-white focus-visible:outline focus-visible:outline-sky-400">Model routing · {split.length} models with local routing</summary>
    <div className="border-t border-slate-800 p-4">
      <p className="mb-4 text-sm leading-6 text-slate-400">The client gateway forwards your requests to these backends. Reachability confirms a responding endpoint; it does not confirm remaining account quota.</p>
      {!split.length && <p className="text-sm text-slate-400">No separate local routes configured. Models use their standard endpoint for both hub and client requests.</p>}
      <div className="grid gap-3 lg:grid-cols-2">{split.map((m) => <article key={m.id} className="min-w-0 rounded-lg border border-slate-800 bg-slate-950/40 p-4">
        <h4 className="break-words text-sm font-medium text-white">{m.id}</h4>
        <p className={'mt-2 text-xs ' + (m.local_only ? 'text-sky-300' : 'text-slate-300')}>{m.local_only ? 'Local only · not registered to hub' : 'Hub and local routing configured'}</p>
        <dl className="mt-3 space-y-3 text-sm">
          {!m.local_only && <div><dt className="text-slate-400">Hub backend · {m.health_string}</dt><dd className="mt-1 break-all font-mono text-xs text-slate-300">{m.endpoint}</dd></div>}
          <div><dt className="text-slate-400">Local backend · {localBackendLabel(m.local_health)}</dt><dd className="mt-1 break-all font-mono text-xs text-slate-300">{m.local_endpoint || m.endpoint}</dd></div>
        </dl>
        {m.local_error && <p className="mt-2 break-words text-sm text-amber-200">{m.local_error}</p>}
      </article>)}</div>
    </div>
  </details>;
}

export function ProxyAccountsPanel({ report }: { report: UsePollingResult<ProxyAccountsReport> }) {
  const { data, loading, error, refreshing, refetch, lastUpdated } = report;
  const instances = [...(data?.instances ?? [])].sort((a, b) => Number(b.roles.includes('hub')) - Number(a.roles.includes('hub')));
  return <div className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <p className="text-sm text-slate-400">{lastUpdated ? 'Accounts checked ' + lastUpdated.toLocaleTimeString() : 'Checking accounts…'} · updates every minute</p>
      <button type="button" onClick={() => void refetch()} disabled={refreshing} className="min-h-11 rounded-lg border border-slate-700 px-4 text-sm text-slate-200 hover:bg-slate-800 focus-visible:outline focus-visible:outline-sky-400 disabled:opacity-50">{refreshing ? 'Refreshing accounts…' : 'Refresh accounts'}</button>
    </div>
    {error && <p role="alert" className="rounded-lg border border-amber-800/60 p-4 text-sm text-amber-200">{data ? 'Account refresh failed. Showing previously fetched status.' : 'Could not load proxy accounts: ' + error.message}</p>}
    {loading && !data ? <p role="status" className="text-slate-400">Loading accounts…</p> : data?.note ? <p className="text-sm text-slate-400">{data.note}</p> : data && !instances.length ? <p className="text-slate-400">No subscription proxies configured.</p> : <>
      <ProxyUsageCharts instances={instances} />
      <h3 className="pt-2 text-base font-semibold text-white">Accounts</h3>
      <div className={`grid items-start gap-5 ${instances.length > 1 ? 'lg:grid-cols-2' : ''}`}>{instances.map((inst) => <Instance key={inst.endpoint} inst={inst} />)}</div>
    </>}
  </div>;
}

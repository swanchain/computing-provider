import type { UsePollingResult } from '../hooks/usePolling';
import { accountWideCooldown, isPast, providerName } from '../lib/proxyStatus';
import type { ModelStatus, ProxyAccount, ProxyAccountsReport, ProxyInstance, ProxyQuotaWindow } from '../types';

function dateLabel(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return 'time unknown';
  return new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' });
}

function WindowBar({ w }: { w: ProxyQuotaWindow }) {
  const pct = Math.max(0, Math.min(100, w.used_percent));
  return <div className="min-w-0">
    <div className="mb-1 flex flex-wrap justify-between gap-x-3 text-sm">
      <span className="text-slate-300">{w.label} usage</span>
      <span className={pct >= 90 ? 'text-amber-200' : 'text-slate-200'}>{pct.toFixed(0)}% used{pct >= 90 ? ' · near limit' : ''}</span>
    </div>
    <div role="meter" aria-label={w.label + ' usage'} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="h-2 overflow-hidden rounded bg-slate-700">
      <div className={'h-full ' + (pct >= 90 ? 'bg-amber-400' : 'bg-emerald-400')} style={{ width: pct + '%' }} />
    </div>
    {w.reset_at && <p className="mt-1 text-xs text-slate-400">Resets {dateLabel(w.reset_at)}</p>}
  </div>;
}

function Account({ a, modelIds }: { a: ProxyAccount; modelIds: string[] }) {
  const expired = isPast(a.expires);
  const unavailable = accountWideCooldown(a);
  const claude = a.provider === 'claude' || a.provider === 'anthropic';
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
      {a.windows.length ? <div className="grid gap-4 xl:grid-cols-2">{a.windows.map((w) => <WindowBar key={w.label} w={w} />)}</div> :
        <p className="text-sm leading-6 text-slate-400">{claude ? <>Subscription usage isn’t available here. <a className="text-sky-300 underline underline-offset-4" href="https://claude.ai/settings/usage" target="_blank" rel="noreferrer">View usage in Claude ↗</a></> : a.provider === 'codex' ? 'Usage has not been reported yet. It appears after this account serves a request.' : 'This provider does not report subscription usage to the proxy.'}</p>}
      {a.windows.length > 0 && <p className="mt-2 text-xs text-slate-400">Usage last reported: {dateLabel(a.quota_observed_at)}</p>}
    </div>}
    {!!a.cooldowns.length && <div className="mt-4 rounded-lg border border-amber-800/50 bg-amber-950/20 p-3">
      <p className="text-sm font-medium text-amber-200">{unavailable ? 'Account restriction' : 'Model restrictions'}</p>
      <ul className="mt-2 space-y-2 text-sm text-amber-100">
        {a.cooldowns.map((c, i) => {
          const listed = !c.model || modelIds.some((id) => id === c.model || id.split('/').slice(1).join('/') === c.model);
          return <li key={i}><p className="break-words">{c.model || 'All models'} · {c.reason === 'quota' ? 'quota blocked' : c.reason?.replace(/_/g, ' ') || 'temporarily restricted'}</p>
            {!listed && <p className="mt-1 text-xs text-slate-300">No matching configured model ID. Check configured models in the connection details.</p>}
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
  const count = (n: number) => new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);
  return <section className="min-w-0 overflow-hidden rounded-xl border border-slate-700 bg-slate-900/60">
    <div className="border-b border-slate-800 bg-slate-800/40 p-4 sm:p-5">
      <h3 className="text-lg font-semibold text-white">{title}</h3>
      <p className="mt-1 text-sm text-slate-300">{hub && local ? 'Serves hub requests and your own clients.' : hub ? 'Serves requests routed by Swan Inference.' : 'Serves your own clients through the client gateway.'}</p>
      <p className="mt-3 text-sm text-slate-300">{enabled.length} enabled account{enabled.length === 1 ? '' : 's'} · {inst.models.length} configured models</p>
      <details className="mt-2 text-sm text-slate-400">
        <summary className="min-h-10 cursor-pointer py-2 focus-visible:outline focus-visible:outline-sky-400">Connection and configured models</summary>
        <p className="break-all font-mono text-xs">{inst.endpoint}</p>
        <ul className="mt-2 space-y-1 text-xs">{inst.models.map((id) => <li className="break-words" key={id}>{id}</li>)}</ul>
      </details>
      <p className="mt-2 text-xs text-slate-400">Last 24 hours · {inst.measured_24h ? count(inst.tokens_in_24h) + ' input / ' + count(inst.tokens_out_24h) + ' output tokens' : 'token usage not measured yet'}</p>
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
          <div><dt className="text-slate-400">Local backend · {m.local_endpoint ? m.local_health === 'healthy' ? 'reachable' : m.local_health === 'unreachable' ? 'unreachable' : 'not checked yet' : m.health_string}</dt><dd className="mt-1 break-all font-mono text-xs text-slate-300">{m.local_endpoint || m.endpoint}</dd></div>
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
    {loading && !data ? <p role="status" className="text-slate-400">Loading accounts…</p> : data?.note ? <p className="text-sm text-slate-400">{data.note}</p> : data && !instances.length ? <p className="text-slate-400">No subscription proxies configured.</p> : <div className="grid items-start gap-5 lg:grid-cols-2">{instances.map((inst) => <Instance key={inst.endpoint} inst={inst} />)}</div>}
  </div>;
}

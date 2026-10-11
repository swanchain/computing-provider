import type { UsePollingResult } from '../hooks/usePolling';
import type { HubModelView, ProxyAccountsReport, UsageSeries } from '../types';
import { proxyIssues } from '../lib/proxyStatus';

interface Props {
  hub: UsePollingResult<HubModelView>;
  usage: UsePollingResult<UsageSeries>;
  proxies: UsePollingResult<ProxyAccountsReport>;
  onProxies: () => void;
  onUsage: () => void;
}

export function OverviewAttention({ hub, usage, proxies, onProxies, onUsage }: Props) {
  const storageKey = 'provider-dashboard-dismissed-attention';
  const [dismissed, setDismissed] = useState(() => sessionStorage.getItem(storageKey) ?? '');
  const issues: { text: string; target?: string; proxy?: boolean }[] = [];
  if (hub.error || hub.data?.error) issues.push({ text: 'Platform listings could not be refreshed. Registration agreement is unverified.', target: 'platform-models' });
  if (hub.data?.not_listed.length) issues.push({ text: `${hub.data.not_listed.length} hub-registered models are missing from platform offerings. Review the listing discrepancy.`, target: 'platform-models' });
  if (usage.error) issues.push({ text: 'Usage could not be refreshed. Displayed totals may be stale.', target: 'earnings-heading' });
  for (const machine of usage.data?.machines ?? []) {
    if (machine.error) issues.push({ text: `${machine.name}: usage unavailable. Totals exclude this machine.`, target: 'earnings-heading' });
  }
  if (proxies.error) issues.push({ text: 'Proxy account status could not be refreshed.', proxy: true });
  if (proxies.data?.note) issues.push({ text: 'Proxy account monitoring is not configured. Account health is unverified.', proxy: true });
  for (const text of proxyIssues(proxies.data)) issues.push({ text, proxy: true });
  if (!issues.length) return <p role="status" className="text-sm text-slate-400">{hub.loading || usage.loading || proxies.loading ? 'Checking platform listings, usage coverage, and proxy accounts…' : 'No platform, usage-reporting, or account alerts in the latest checks.'}</p>;
  const signature = issues.map((issue) => issue.text).join('\n');
  if (dismissed === signature) return <button type="button" onClick={() => { sessionStorage.removeItem(storageKey); setDismissed(''); }} className="min-h-10 rounded-lg border border-amber-800/60 px-3 text-sm text-amber-200 hover:bg-amber-950/30 focus-visible:outline focus-visible:outline-sky-400">Show {issues.length} dismissed alert{issues.length === 1 ? '' : 's'}</button>;
  return <section aria-labelledby="attention-heading" className="rounded-xl border border-amber-800/60 bg-amber-950/20 p-4">
    <div className="flex items-start justify-between gap-3">
      <h3 id="attention-heading" className="font-semibold text-amber-100">Needs attention</h3>
      <button type="button" aria-label="Close needs attention" onClick={() => { sessionStorage.setItem(storageKey, signature); setDismissed(signature); }} className="-m-1 inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg text-amber-200 hover:bg-amber-900/30 focus-visible:outline focus-visible:outline-sky-400"><X aria-hidden="true" size={18} /></button>
    </div>
    <ul className="mt-2 divide-y divide-amber-800/30">{issues.map((issue) => <li key={issue.text} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2 text-sm text-amber-100">
      <span className="min-w-0 flex-1 basis-64 break-words">{issue.text}</span>
      <button type="button" onClick={() => issue.proxy ? onProxies() : issue.target === 'earnings-heading' ? onUsage() : document.getElementById(issue.target!)?.scrollIntoView({ behavior: 'smooth' })} className="min-h-10 rounded-lg border border-amber-700/60 px-3 text-sm hover:bg-amber-900/30 focus-visible:outline focus-visible:outline-sky-400">{issue.proxy ? 'Review accounts' : 'View details'}</button>
    </li>)}</ul>
  </section>;
}
import { useState } from 'react';
import { X } from 'lucide-react';

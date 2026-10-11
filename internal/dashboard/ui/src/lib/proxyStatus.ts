import type { ProxyAccount, ProxyAccountsReport } from '../types';

export function isPast(value?: string) { return !!value && Date.parse(value) < Date.now(); }
export function providerName(provider?: string) {
  return ({ claude: 'Claude', anthropic: 'Claude', codex: 'Codex' } as Record<string, string>)[provider ?? ''] ?? provider ?? 'Subscription account';
}
export function accountWideCooldown(a: ProxyAccount) {
  return a.unavailable || a.cooldowns.some((c) => !c.model);
}

export function proxyIssues(report: ProxyAccountsReport | null | undefined): string[] {
  const issues: string[] = [];
  for (const inst of report?.instances ?? []) {
    const label = inst.roles.includes('hub') ? 'Hub proxy' : 'Local proxy';
    if (inst.error) { issues.push(label + ': account status unavailable'); continue; }
    if (!inst.accounts.length) issues.push(label + ': no accounts configured');
    else if (inst.accounts.every((a) => a.disabled)) issues.push(label + ': all accounts disabled');
    for (const a of inst.accounts) {
      if (a.disabled) continue;
      const name = label + ' · ' + providerName(a.provider);
      if (isPast(a.expires)) issues.push(name + ': check sign-in; stored token expired');
      else if (accountWideCooldown(a)) issues.push(name + ': temporarily unavailable');
      for (const c of a.cooldowns) {
        if (c.model && inst.models.some((id) => id === c.model || id.split('/').slice(1).join('/') === c.model)) {
          issues.push(name + ': ' + c.model + ' restricted');
        }
      }
      for (const w of a.windows) if (w.used_percent >= 90) issues.push(name + ': ' + w.label + ' usage ' + w.used_percent.toFixed(0) + '%');
    }
  }
  return issues;
}

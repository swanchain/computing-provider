import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import {
  Bell,
  Check,
  Gauge,
  KeyRound,
  LockKeyhole,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  ServerCog,
  Settings2,
  ShieldCheck,
  Trash2,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  AlertSettings,
  DashboardModel,
  DashboardSettings,
  LogSettings,
  RequestLimitSettings,
  SelfCheckSettings,
  SettingsSaveResult,
} from '../types';

interface SettingsPanelProps {
  authenticated: boolean;
  onUnlock: () => void;
  onModelsSaved: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

type SaveKey = 'limits' | 'models' | 'alerts' | 'self-check' | 'logging';
type SaveState = { key: SaveKey; type: 'success' | 'error'; message: string } | null;
type EditableModel = DashboardModel & { isNew?: boolean };

const SETTINGS_NAV: Array<{ id: SaveKey; label: string }> = [
  { id: 'limits', label: 'Limits' },
  { id: 'models', label: 'Models' },
  { id: 'alerts', label: 'Alerts' },
  { id: 'self-check', label: 'Self-check' },
  { id: 'logging', label: 'Logging' },
];

const inputClass = 'min-h-11 w-full rounded-lg border border-slate-600 bg-slate-950 px-3 text-sm text-slate-100 outline-none transition placeholder:text-slate-400 focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20';
const labelClass = 'mb-1.5 block text-sm font-medium text-slate-200';

function ApplyBadge({ restartRequired }: { restartRequired: boolean }) {
  return (
    <span className={`inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-medium ${
      restartRequired
        ? 'border-amber-800/70 bg-amber-950/40 text-amber-300'
        : 'border-emerald-800/70 bg-emerald-950/40 text-emerald-300'
    }`}>
      {restartRequired ? 'Restart required' : 'Applies now'}
    </span>
  );
}

function SettingsSection({
  id,
  title,
  description,
  icon,
  restartRequired,
  dirty,
  children,
}: {
  id: string;
  title: string;
  description: string;
  icon: ReactNode;
  restartRequired: boolean;
  dirty?: boolean;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={`${id}-title`} className="scroll-mt-14 overflow-hidden rounded-xl border border-slate-800 bg-slate-900">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-slate-800 px-4 py-4 sm:px-5">
        <div className="flex min-w-0 gap-3">
          <div className="mt-0.5 rounded-lg bg-slate-800 p-2 text-blue-400">{icon}</div>
          <div>
            <h2 id={`${id}-title`} className="font-semibold text-white">{title}</h2>
            <p className="mt-1 max-w-2xl text-sm leading-5 text-slate-400">{description}</p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {dirty && <span className="inline-flex items-center rounded-full border border-blue-800/70 bg-blue-950/40 px-2.5 py-1 text-xs font-medium text-blue-200">Unsaved</span>}
          <ApplyBadge restartRequired={restartRequired} />
        </div>
      </div>
      <div className="p-4 sm:p-5">{children}</div>
    </section>
  );
}

function SaveFeedback({ state, section }: { state: SaveState; section: SaveKey }) {
  if (!state || state.key !== section) return null;
  return (
    <p
      role={state.type === 'error' ? 'alert' : 'status'}
      className={`rounded-lg border px-3 py-2 text-sm ${
        state.type === 'error'
          ? 'border-red-800/70 bg-red-950/30 text-red-300'
          : 'border-emerald-800/70 bg-emerald-950/30 text-emerald-300'
      }`}
    >
      {state.message}
    </p>
  );
}

function Toggle({
  checked,
  onChange,
  label,
  description,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  description?: string;
}) {
  return (
    <label className="flex min-h-11 cursor-pointer items-start justify-between gap-4 rounded-lg border border-slate-700 bg-slate-950/60 px-3 py-2.5">
      <span>
        <span className="block text-sm font-medium text-slate-200">{label}</span>
        {description && <span className="mt-0.5 block text-xs leading-4 text-slate-400">{description}</span>}
      </span>
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 h-5 w-5 rounded border-slate-600 bg-slate-900 text-blue-600 focus:ring-2 focus:ring-blue-500"
      />
    </label>
  );
}

function SaveButton({ saving, label = 'Save settings' }: { saving: boolean; label?: string }) {
  return (
    <button
      type="submit"
      disabled={saving}
      className="inline-flex min-h-10 items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 text-sm font-medium text-white transition hover:bg-blue-500 disabled:cursor-wait disabled:opacity-60"
    >
      {saving ? <RefreshCw aria-hidden="true" className="animate-spin" size={16} /> : <Save aria-hidden="true" size={16} />}
      {saving ? 'Saving…' : label}
    </button>
  );
}

export function SettingsPanel({ authenticated, onUnlock, onModelsSaved, onDirtyChange }: SettingsPanelProps) {
  const [settings, setSettings] = useState<DashboardSettings | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState<SaveKey | null>(null);
  const [saveState, setSaveState] = useState<SaveState>(null);
  const [initialModelIDs, setInitialModelIDs] = useState<string[]>([]);
  const [dirtySections, setDirtySections] = useState<Set<SaveKey>>(() => new Set());
  const [restartPending, setRestartPending] = useState(() => sessionStorage.getItem('computing-provider-restart-pending') === 'true');

  const markDirty = (key: SaveKey) => {
    setDirtySections((current) => {
      const next = new Set(current);
      next.add(key);
      return next;
    });
    setSaveState((current) => current?.key === key ? null : current);
  };

  const loadSettings = useCallback(async () => {
    if (!authenticated) return;
    setLoading(true);
    setLoadError('');
    try {
      const response = await api.getSettings();
      setSettings({ ...response, models: response.models.map((model) => ({ ...model })) });
      setInitialModelIDs(response.models.map((model) => model.id));
      setDirtySections(new Set());
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : 'Unable to load settings');
    } finally {
      setLoading(false);
    }
  }, [authenticated]);

  useEffect(() => {
    if (authenticated) loadSettings();
    else {
      setSettings(null);
      setDirtySections(new Set());
    }
  }, [authenticated, loadSettings]);

  useEffect(() => {
    const dirty = dirtySections.size > 0;
    onDirtyChange(dirty);
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warnBeforeUnload);
    return () => {
      window.removeEventListener('beforeunload', warnBeforeUnload);
      onDirtyChange(false);
    };
  }, [dirtySections, onDirtyChange]);

  const runSave = async (key: SaveKey, action: () => Promise<SettingsSaveResult>) => {
    setSaving(key);
    setSaveState(null);
    try {
      const result = await action();
      setSaveState({
        key,
        type: 'success',
        message: result.restart_required
          ? 'Saved. Restart computing-provider when convenient to apply this section.'
          : 'Saved and applied to the running provider.',
      });
      setDirtySections((current) => {
        const next = new Set(current);
        next.delete(key);
        return next;
      });
      if (result.restart_required) {
        sessionStorage.setItem('computing-provider-restart-pending', 'true');
        setRestartPending(true);
      }
      return true;
    } catch (error) {
      setSaveState({ key, type: 'error', message: error instanceof Error ? error.message : 'Save failed' });
      return false;
    } finally {
      setSaving(null);
    }
  };

  const removedModelCount = useMemo(() => {
    if (!settings) return 0;
    const current = new Set(settings.models.map((model) => model.id));
    return initialModelIDs.filter((id) => !current.has(id)).length;
  }, [initialModelIDs, settings]);

  if (!authenticated) {
    return (
      <div className="mx-auto max-w-2xl py-8 sm:py-16">
        <div className="rounded-2xl border border-slate-800 bg-slate-900 p-6 text-center sm:p-10">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-500/10 text-blue-400">
            <LockKeyhole aria-hidden="true" size={24} />
          </div>
          <h2 className="text-xl font-semibold text-white">Settings are locked</h2>
          <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-slate-400">
            Unlock this browser tab with the local control token before reading or changing provider configuration. Monitoring remains available without it.
          </p>
          <button type="button" onClick={onUnlock} className="mt-5 inline-flex min-h-11 items-center gap-2 rounded-lg bg-blue-600 px-4 text-sm font-medium text-white hover:bg-blue-500">
            <KeyRound aria-hidden="true" size={17} /> Unlock settings
          </button>
        </div>
      </div>
    );
  }

  if (loading && !settings) {
    return (
      <div className="flex min-h-64 items-center justify-center text-slate-400" role="status">
        <RefreshCw aria-hidden="true" className="mr-2 animate-spin" size={18} /> Loading settings…
      </div>
    );
  }

  if (!settings) {
    return (
      <div className="rounded-xl border border-red-800/60 bg-red-950/20 p-5">
        <h2 className="font-semibold text-red-200">Settings could not be loaded</h2>
        <p className="mt-1 text-sm text-red-300">{loadError}</p>
        <button type="button" onClick={loadSettings} className="mt-4 rounded-lg bg-slate-800 px-4 py-2 text-sm text-white">Try again</button>
      </div>
    );
  }

  const updateAlerts = (patch: Partial<AlertSettings>) => { markDirty('alerts'); setSettings((current) => current ? ({ ...current, alerts: { ...current.alerts, ...patch } }) : current); };
  const updateSelfCheck = (patch: Partial<SelfCheckSettings>) => { markDirty('self-check'); setSettings((current) => current ? ({ ...current, self_check: { ...current.self_check, ...patch } }) : current); };
  const updateLog = (patch: Partial<LogSettings>) => { markDirty('logging'); setSettings((current) => current ? ({ ...current, log: { ...current.log, ...patch } }) : current); };
  const updateLimits = (patch: Partial<RequestLimitSettings>) => { markDirty('limits'); setSettings((current) => current ? ({ ...current, limits: { ...current.limits, ...patch } }) : current); };
  const updateEmail = (patch: Partial<AlertSettings['email']>) => updateAlerts({ email: { ...settings.alerts.email, ...patch } });
  const updateModel = (index: number, patch: Partial<EditableModel>) => {
    markDirty('models');
    setSettings((current) => {
      if (!current) return current;
      const models = current.models.map((model, modelIndex) => modelIndex === index ? { ...model, ...patch } : model);
      return { ...current, models };
    });
  };

  const saveAlerts = async (event: FormEvent) => {
    event.preventDefault();
    const recipients = settings.alerts.email.to
      .flatMap((value) => value.split(/[\n,]/))
      .map((value) => value.trim())
      .filter(Boolean);
    const payload = { ...settings.alerts, email: { ...settings.alerts.email, to: recipients } };
    if (await runSave('alerts', () => api.updateAlerts(payload))) {
      setSettings((current) => current ? ({
        ...current,
        alerts: {
          ...current.alerts,
          email: {
            ...current.alerts.email,
            password: '',
            clear_password: false,
            to: recipients,
            password_set: payload.email.clear_password ? false : payload.email.password_set || Boolean(payload.email.password),
          },
        },
      }) : current);
    }
  };

  const saveModels = async (event: FormEvent) => {
    event.preventDefault();
    if (removedModelCount > 0 && !window.confirm(`Save and remove ${removedModelCount} model${removedModelCount === 1 ? '' : 's'} from routing?`)) return;
    if (await runSave('models', () => api.updateModels(settings.models))) {
      onModelsSaved();
      await loadSettings();
    }
  };

  const reloadFromDisk = () => {
    if (dirtySections.size > 0 && !window.confirm('Reload settings from disk and discard unsaved changes?')) return;
    loadSettings();
  };

  const dismissRestartReminder = () => {
    sessionStorage.removeItem('computing-provider-restart-pending');
    setRestartPending(false);
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold text-white">Provider settings</h1>
          <p className="mt-1 text-sm text-slate-400">Validated edits to config.toml and models.json. Secrets are write-only.</p>
        </div>
        <button type="button" onClick={reloadFromDisk} disabled={loading} className="inline-flex min-h-10 items-center gap-2 rounded-lg border border-slate-700 bg-slate-900 px-3 text-sm text-slate-200 hover:bg-slate-800 disabled:opacity-50">
          <RotateCcw aria-hidden="true" className={loading ? 'animate-spin' : ''} size={16} /> Reload from disk
        </button>
      </div>

      <nav aria-label="Settings sections" className="sticky top-0 z-20 -mx-1 overflow-x-auto rounded-xl border border-slate-800 bg-slate-950/95 p-1 shadow-lg shadow-slate-950/30 backdrop-blur">
        <div className="flex min-w-max gap-1">
          {SETTINGS_NAV.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => document.getElementById(`${item.id}-title`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
              className="inline-flex min-h-10 items-center rounded-lg px-3 text-sm text-slate-300 hover:bg-slate-800 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              {item.label}
              {dirtySections.has(item.id) && <span className="ml-2 h-2 w-2 rounded-full bg-blue-400" aria-label="Unsaved changes" />}
            </button>
          ))}
        </div>
      </nav>

      {dirtySections.size > 0 && (
        <p role="status" className="rounded-lg border border-blue-800/70 bg-blue-950/30 px-4 py-3 text-sm text-blue-100">
          Unsaved changes in {dirtySections.size} section{dirtySections.size === 1 ? '' : 's'}. Save each marked section before leaving Settings.
        </p>
      )}
      {restartPending && (
        <div role="status" className="flex flex-col gap-3 rounded-lg border border-amber-800/70 bg-amber-950/30 px-4 py-3 text-sm text-amber-100 sm:flex-row sm:items-center sm:justify-between">
          <span>Saved configuration is waiting for a provider-daemon restart before it takes effect.</span>
          <button type="button" onClick={dismissRestartReminder} className="min-h-10 self-start rounded-lg border border-amber-700/70 px-3 text-amber-100 hover:bg-amber-900/30 sm:self-auto">Dismiss</button>
        </div>
      )}
      {loadError && <p role="alert" className="rounded-lg border border-red-800/60 bg-red-950/20 px-4 py-3 text-sm text-red-300">{loadError}</p>}

      <SettingsSection id="limits" title="Request limits" description="Protect the provider from more work than it can serve. Both values are persisted and applied immediately." icon={<Gauge aria-hidden="true" size={19} />} restartRequired={false} dirty={dirtySections.has('limits')}>
        <form onSubmit={(event) => { event.preventDefault(); runSave('limits', () => api.updateLimits(settings.limits)); }} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className={labelClass} htmlFor="requests-per-second">Requests per second</label>
              <input id="requests-per-second" type="number" min="0.1" max="100000" step="0.1" required value={settings.limits.requests_per_second} onChange={(event) => updateLimits({ requests_per_second: Number(event.target.value) })} className={inputClass} />
              <p className="mt-1 text-xs text-slate-400">Base global rate; GPU-aware adaptation may lower or raise the live rate.</p>
            </div>
            <div>
              <label className={labelClass} htmlFor="max-concurrent">Maximum concurrent requests</label>
              <input id="max-concurrent" type="number" min="1" max="100000" required value={settings.limits.max_concurrent} onChange={(event) => updateLimits({ max_concurrent: Number(event.target.value) })} className={inputClass} />
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3"><SaveFeedback state={saveState} section="limits" /><SaveButton saving={saving === 'limits'} /></div>
        </form>
      </SettingsSection>

      <SettingsSection id="models" title="Model endpoint map" description="Add, repoint, or remove local inference endpoints. Saving hot-reloads models.json and updates the advertised model list." icon={<ServerCog aria-hidden="true" size={19} />} restartRequired={false} dirty={dirtySections.has('models')}>
        <form onSubmit={saveModels} className="space-y-4">
          {settings.models.length === 0 ? (
            <div className="rounded-lg border border-dashed border-slate-700 px-4 py-8 text-center text-sm text-slate-400">No models configured. Add one to begin serving inference.</div>
          ) : (
            <div className="space-y-3">
              {(settings.models as EditableModel[]).map((model, index) => (
                <div key={`${model.id}-${index}`} className="rounded-xl border border-slate-700 bg-slate-950/50 p-4">
                  <div className="grid gap-4 lg:grid-cols-[minmax(180px,0.8fr)_minmax(240px,1.2fr)_auto]">
                    <div>
                      <label className={labelClass} htmlFor={`model-id-${index}`}>Model ID</label>
                      <input id={`model-id-${index}`} required readOnly={!model.isNew} value={model.id} onChange={(event) => updateModel(index, { id: event.target.value })} className={`${inputClass} font-mono ${!model.isNew ? 'cursor-not-allowed bg-slate-900 text-slate-400' : ''}`} />
                    </div>
                    <div>
                      <label className={labelClass} htmlFor={`model-endpoint-${index}`}>Endpoint</label>
                      <input id={`model-endpoint-${index}`} type="url" required value={model.endpoint} onChange={(event) => updateModel(index, { endpoint: event.target.value })} placeholder="http://127.0.0.1:8000" className={`${inputClass} font-mono`} />
                    </div>
                    <button
                      type="button"
                      onClick={() => {
                        if (!window.confirm(`Remove ${model.id || 'this model'} from the configuration? The change takes effect when you save.`)) return;
                        markDirty('models');
                        setSettings((current) => current ? ({ ...current, models: current.models.filter((_, modelIndex) => modelIndex !== index) }) : current);
                      }}
                      className="mt-auto inline-flex min-h-11 items-center justify-center gap-2 rounded-lg border border-red-900/70 px-3 text-sm text-red-300 hover:bg-red-950/40"
                      aria-label={`Remove ${model.id || 'new model'}`}
                    >
                      <Trash2 aria-hidden="true" size={16} /> <span className="lg:hidden">Remove</span>
                    </button>
                  </div>
                  <div className="mt-4 grid gap-4 sm:grid-cols-3">
                    <div>
                      <label className={labelClass} htmlFor={`model-category-${index}`}>Category</label>
                      <input id={`model-category-${index}`} required value={model.category} onChange={(event) => updateModel(index, { category: event.target.value })} placeholder="text-generation" className={inputClass} />
                    </div>
                    <div>
                      <label className={labelClass} htmlFor={`local-model-${index}`}>Local model name</label>
                      <input id={`local-model-${index}`} value={model.local_model ?? ''} onChange={(event) => updateModel(index, { local_model: event.target.value })} placeholder="Optional Ollama name" className={inputClass} />
                    </div>
                    <div>
                      <label className={labelClass} htmlFor={`context-length-${index}`}>Context length</label>
                      <input id={`context-length-${index}`} type="number" min="0" value={model.context_length ?? 0} onChange={(event) => updateModel(index, { context_length: Number(event.target.value) })} className={inputClass} />
                    </div>
                  </div>
                  <details className="mt-4 rounded-lg border border-slate-800 bg-slate-950/60">
                    <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-300">Advanced endpoint details</summary>
                    <div className="grid gap-4 border-t border-slate-800 p-3 sm:grid-cols-2 lg:grid-cols-4">
                      <div><label className={labelClass} htmlFor={`gpu-memory-${index}`}>GPU memory (MB)</label><input id={`gpu-memory-${index}`} type="number" min="0" value={model.gpu_memory} onChange={(event) => updateModel(index, { gpu_memory: Number(event.target.value) })} className={inputClass} /></div>
                      <div><label className={labelClass} htmlFor={`container-${index}`}>Container</label><input id={`container-${index}`} value={model.container ?? ''} onChange={(event) => updateModel(index, { container: event.target.value })} className={inputClass} /></div>
                      <div><label className={labelClass} htmlFor={`format-${index}`}>Format</label><input id={`format-${index}`} value={model.format ?? ''} onChange={(event) => updateModel(index, { format: event.target.value })} placeholder="awq, gguf…" className={inputClass} /></div>
                      <div><label className={labelClass} htmlFor={`quantization-${index}`}>Quantization</label><input id={`quantization-${index}`} value={model.quantization ?? ''} onChange={(event) => updateModel(index, { quantization: event.target.value })} className={inputClass} /></div>
                      <div className="sm:col-span-2 lg:col-span-4">
                        <label className={labelClass} htmlFor={`endpoint-key-${index}`}>Endpoint API key</label>
                        <input id={`endpoint-key-${index}`} type="password" autoComplete="new-password" value={model.api_key ?? ''} onChange={(event) => updateModel(index, { api_key: event.target.value, clear_api_key: false })} placeholder={model.api_key_set ? 'Configured •••• — leave blank to keep' : 'Optional write-only replacement'} className={inputClass} />
                        {model.api_key_set && <label className="mt-2 inline-flex items-center gap-2 text-xs text-slate-400"><input type="checkbox" checked={Boolean(model.clear_api_key)} onChange={(event) => updateModel(index, { clear_api_key: event.target.checked, api_key: '' })} /> Clear stored endpoint key</label>}
                      </div>
                    </div>
                  </details>
                </div>
              ))}
            </div>
          )}
          <button type="button" onClick={() => { markDirty('models'); setSettings((current) => current ? ({ ...current, models: [...current.models, { id: '', endpoint: '', gpu_memory: 0, category: 'text-generation', api_key_set: false, context_length: 0, isNew: true } as EditableModel] }) : current); }} className="inline-flex min-h-10 items-center gap-2 rounded-lg border border-dashed border-slate-600 px-3 text-sm text-slate-200 hover:border-blue-500 hover:text-white">
            <Plus aria-hidden="true" size={16} /> Add model
          </button>
          <div className="flex flex-wrap items-center justify-between gap-3"><SaveFeedback state={saveState} section="models" /><div className="ml-auto flex items-center gap-3">{removedModelCount > 0 && <span className="text-xs text-amber-300">{removedModelCount} removal pending</span>}<SaveButton saving={saving === 'models'} label="Save and hot-reload" /></div></div>
        </form>
      </SettingsSection>

      <SettingsSection id="alerts" title="Alert delivery" description="Configure webhook and SMTP delivery. Stored passwords are never returned to the browser." icon={<Bell aria-hidden="true" size={19} />} restartRequired={true} dirty={dirtySections.has('alerts')}>
        <form onSubmit={saveAlerts} className="space-y-5">
          <div>
            <label className={labelClass} htmlFor="webhook-url">Webhook URL</label>
            <input id="webhook-url" type="url" value={settings.alerts.webhook_url} onChange={(event) => updateAlerts({ webhook_url: event.target.value })} placeholder="https://alerts.example.com/provider" className={inputClass} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div><label className={labelClass} htmlFor="cooldown">Repeat cooldown (minutes)</label><input id="cooldown" type="number" min="1" max="10080" required value={settings.alerts.cooldown_minutes} onChange={(event) => updateAlerts({ cooldown_minutes: Number(event.target.value) })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="disconnect-delay">Disconnect alert after (minutes)</label><input id="disconnect-delay" type="number" min="1" max="10080" required value={settings.alerts.disconnect_after_min} onChange={(event) => updateAlerts({ disconnect_after_min: Number(event.target.value) })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="failure-threshold">Failure threshold (%)</label><input id="failure-threshold" type="number" min="1" max="100" step="1" required value={Math.round(settings.alerts.error_rate_threshold * 100)} onChange={(event) => updateAlerts({ error_rate_threshold: Number(event.target.value) / 100 })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="minimum-requests">Minimum requests</label><input id="minimum-requests" type="number" min="1" required value={settings.alerts.error_rate_min_requests} onChange={(event) => updateAlerts({ error_rate_min_requests: Number(event.target.value) })} className={inputClass} /></div>
          </div>

          <fieldset className="rounded-xl border border-slate-700 p-4">
            <legend className="px-2 text-sm font-semibold text-slate-200">Email (SMTP)</legend>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <div className="sm:col-span-2"><label className={labelClass} htmlFor="smtp-host">SMTP host</label><input id="smtp-host" value={settings.alerts.email.host} onChange={(event) => updateEmail({ host: event.target.value })} placeholder="smtp.example.com" className={inputClass} /></div>
              <div><label className={labelClass} htmlFor="smtp-port">Port</label><input id="smtp-port" type="number" min="1" max="65535" value={settings.alerts.email.port} onChange={(event) => updateEmail({ port: Number(event.target.value) })} className={inputClass} /></div>
              <div><label className={labelClass} htmlFor="smtp-username">Username</label><input id="smtp-username" value={settings.alerts.email.username} onChange={(event) => updateEmail({ username: event.target.value })} className={inputClass} /></div>
              <div><label className={labelClass} htmlFor="smtp-from">From address</label><input id="smtp-from" type="email" value={settings.alerts.email.from} onChange={(event) => updateEmail({ from: event.target.value })} className={inputClass} /></div>
              <div><label className={labelClass} htmlFor="smtp-recipients">Recipients</label><textarea id="smtp-recipients" rows={2} value={settings.alerts.email.to.join('\n')} onChange={(event) => updateEmail({ to: event.target.value.split('\n') })} placeholder="One address per line" className={`${inputClass} py-2`} /></div>
              <div className="sm:col-span-2 lg:col-span-3">
                <label className={labelClass} htmlFor="smtp-password">SMTP password</label>
                <input id="smtp-password" type="password" autoComplete="new-password" value={settings.alerts.email.password ?? ''} onChange={(event) => updateEmail({ password: event.target.value, clear_password: false })} placeholder={settings.alerts.email.password_set ? 'Configured •••• — leave blank to keep' : 'Write-only password'} className={inputClass} />
                {settings.alerts.email.password_set && <label className="mt-2 inline-flex items-center gap-2 text-xs text-slate-400"><input type="checkbox" checked={Boolean(settings.alerts.email.clear_password)} onChange={(event) => updateEmail({ clear_password: event.target.checked, password: '' })} /> Clear password stored in config.toml</label>}
              </div>
            </div>
          </fieldset>
          <div className="flex flex-wrap items-center justify-between gap-3"><SaveFeedback state={saveState} section="alerts" /><SaveButton saving={saving === 'alerts'} /></div>
        </form>
      </SettingsSection>

      <SettingsSection id="self-check" title="Self-check behavior" description="Control periodic inference audits and automatic routing recovery." icon={<ShieldCheck aria-hidden="true" size={19} />} restartRequired={true} dirty={dirtySections.has('self-check')}>
        <form onSubmit={(event) => { event.preventDefault(); runSave('self-check', () => api.updateSelfCheck(settings.self_check)); }} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Toggle checked={settings.self_check.enable} onChange={(enable) => updateSelfCheck({ enable })} label="Periodic self-check" description="Audit configured models on a schedule." />
            <Toggle checked={settings.self_check.auto_disable} onChange={(auto_disable) => updateSelfCheck({ auto_disable })} label="Auto-disable failing models" description="Remove repeatedly failing backends from routing." />
            <Toggle checked={settings.self_check.auto_recover} onChange={(auto_recover) => updateSelfCheck({ auto_recover })} label="Auto-recover healthy models" description="Return recovered backends to routing." />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div><label className={labelClass} htmlFor="self-check-interval">Interval (minutes)</label><input id="self-check-interval" type="number" min="1" max="10080" required value={settings.self_check.interval_minutes} onChange={(event) => updateSelfCheck({ interval_minutes: Number(event.target.value) })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="failures-before-disable">Failures before disable</label><input id="failures-before-disable" type="number" min="1" max="100" required value={settings.self_check.failures_before_disable} onChange={(event) => updateSelfCheck({ failures_before_disable: Number(event.target.value) })} className={inputClass} /></div>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3"><SaveFeedback state={saveState} section="self-check" /><SaveButton saving={saving === 'self-check'} /></div>
        </form>
      </SettingsSection>

      <SettingsSection id="logging" title="Logging and retention" description="Choose log verbosity, destination, rotation, and retention." icon={<Settings2 aria-hidden="true" size={19} />} restartRequired={true} dirty={dirtySections.has('logging')}>
        <form onSubmit={(event) => { event.preventDefault(); runSave('logging', () => api.updateLogging(settings.log)); }} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="sm:col-span-2 lg:col-span-3"><label className={labelClass} htmlFor="log-dir">Log directory</label><input id="log-dir" required value={settings.log.dir} onChange={(event) => updateLog({ dir: event.target.value })} className={`${inputClass} font-mono`} /></div>
            <div><label className={labelClass} htmlFor="log-level">Level</label><select id="log-level" value={settings.log.level} onChange={(event) => updateLog({ level: event.target.value })} className={inputClass}>{['trace', 'debug', 'info', 'warn', 'error'].map((level) => <option key={level} value={level}>{level}</option>)}</select></div>
            <div><label className={labelClass} htmlFor="log-max-size">Rotate at (MB)</label><input id="log-max-size" type="number" min="1" max="102400" required value={settings.log.max_size_mb} onChange={(event) => updateLog({ max_size_mb: Number(event.target.value) })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="log-backups">Backups to keep</label><input id="log-backups" type="number" min="1" max="1000" required value={settings.log.max_backups} onChange={(event) => updateLog({ max_backups: Number(event.target.value) })} className={inputClass} /></div>
            <div><label className={labelClass} htmlFor="log-age">Retention days (-1 = forever)</label><input id="log-age" type="number" min="-1" max="36500" required value={settings.log.max_age_days} onChange={(event) => updateLog({ max_age_days: Number(event.target.value) })} className={inputClass} /></div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2"><Toggle checked={settings.log.compress} onChange={(compress) => updateLog({ compress })} label="Compress rotated logs" /><Toggle checked={settings.log.stdout} onChange={(stdout) => updateLog({ stdout })} label="Also write to stdout" /></div>
          <div className="flex flex-wrap items-center justify-between gap-3"><SaveFeedback state={saveState} section="logging" /><SaveButton saving={saving === 'logging'} /></div>
        </form>
      </SettingsSection>

      <div className="flex items-center gap-2 rounded-lg border border-slate-800 bg-slate-900/60 px-4 py-3 text-xs text-slate-400">
        <Check aria-hidden="true" size={15} className="text-emerald-400" /> All saves are validated server-side and use atomic file replacement.
      </div>
    </div>
  );
}

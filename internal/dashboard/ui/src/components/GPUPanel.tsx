import { Cpu, Thermometer } from 'lucide-react';
import type { GPUInfo } from '../types';

interface GPUPanelProps {
  gpus: GPUInfo[];
  loading: boolean;
  error?: Error | null;
}

function ProgressBar({ value, max, color, label }: { value: number; max: number; color: string; label: string }) {
  const percent = max > 0 ? (value / max) * 100 : 0;
  return (
    <div
      className="h-2 w-full rounded-full bg-slate-700"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(Math.min(percent, 100))}
    >
      <div
        className={`h-2 rounded-full ${color}`}
        style={{ width: `${Math.min(percent, 100)}%` }}
      />
    </div>
  );
}

export function GPUPanel({ gpus, loading, error }: GPUPanelProps) {
  if (loading) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">GPU Status</h3>
        <div className="animate-pulse space-y-4">
          <div className="h-20 bg-slate-700 rounded"></div>
        </div>
      </div>
    );
  }

  if (!gpus || gpus.length === 0) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">GPU Status</h3>
        <p className="text-slate-400">{error ? 'API unreachable' : 'No GPUs detected'}</p>
      </div>
    );
  }

  const peakTemperature = Math.max(...gpus.map((gpu) => gpu.temperature_c));
  const activeGPUs = gpus.filter((gpu) => gpu.utilization_percent > 5).length;

  return (
    <div className="rounded-xl border border-slate-700 bg-slate-900 p-4">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-lg font-semibold text-slate-100">GPU capacity</h3>
          <p className="mt-0.5 text-xs text-slate-400">{activeGPUs} active · peak {peakTemperature}°C</p>
        </div>
        <div className="flex items-center gap-2 text-sm text-slate-300">
          <Cpu aria-hidden="true" size={16} />
          <span>{gpus.length} GPU{gpus.length > 1 ? 's' : ''}</span>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        {gpus.map((g) => (
          <div key={g.index} className="border border-slate-700 rounded-lg p-3">
            <div className="flex items-center justify-between mb-2">
              <span className="min-w-0 text-sm font-medium text-slate-200"><span className="block text-xs text-slate-400">GPU {g.index}</span>{g.name}</span>
              <div className="flex items-center gap-1 text-sm">
                <Thermometer aria-hidden="true" size={14} className={g.temperature_c >= 90 ? 'text-red-300' : g.temperature_c >= 85 ? 'text-amber-300' : 'text-slate-300'} />
                <span className={g.temperature_c >= 90 ? 'text-red-300' : g.temperature_c >= 85 ? 'text-amber-300' : 'text-slate-300'}>
                  {g.temperature_c}°C
                </span>
              </div>
            </div>

            <div className="space-y-2">
              <div>
                <div className="mb-1 flex justify-between text-xs text-slate-300">
                  <span>Utilization</span>
                  <span>{g.utilization_percent.toFixed(0)}%</span>
                </div>
                <ProgressBar
                  value={g.utilization_percent}
                  max={100}
                  color="bg-blue-500"
                  label={`${g.name} utilization`}
                />
              </div>

              {g.memory_total_mb > 0 && (
                <div>
                  <div className="mb-1 flex justify-between text-xs text-slate-300">
                    <span>VRAM</span>
                    <span>{(g.memory_used_mb / 1024).toFixed(1)} / {(g.memory_total_mb / 1024).toFixed(1)} GB</span>
                  </div>
                  <ProgressBar
                    value={g.memory_used_mb}
                    max={g.memory_total_mb}
                    color={g.memory_used_mb / g.memory_total_mb >= 0.98 ? 'bg-red-500' : g.memory_used_mb / g.memory_total_mb >= 0.95 ? 'bg-amber-400' : 'bg-blue-500'}
                    label={`${g.name} memory allocation`}
                  />
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

/**
 * One line per GPU for the overview: utilisation, VRAM and temperature at a
 * glance, with the full panel on Diagnostics. Temperature and VRAM colour at
 * the same thresholds as the panel so the two never disagree.
 */
export function GPUStrip({ gpus, loading, error }: GPUPanelProps) {
  if (loading && (!gpus || gpus.length === 0)) return null;
  if (!gpus || gpus.length === 0) {
    return <p className="text-xs text-slate-500">{error ? 'GPU status unavailable' : 'No GPUs detected'}</p>;
  }
  const tempClass = (c: number) => (c >= 90 ? 'text-red-300' : c >= 85 ? 'text-amber-300' : 'text-slate-200');
  return (
    <ul aria-label="GPUs" className="grid gap-px overflow-hidden rounded-xl border border-slate-800 bg-slate-800 sm:grid-cols-2 xl:grid-cols-4">
      {gpus.map((g) => {
        const vramShare = g.memory_total_mb > 0 ? g.memory_used_mb / g.memory_total_mb : 0;
        return (
          <li key={g.index} className="min-w-0 bg-slate-900 px-3 py-2.5">
            <div className="flex items-baseline justify-between gap-2 text-xs text-slate-400">
              <span className="truncate">GPU {g.index} · {g.name}</span>
              <span className={`flex items-center gap-1 tabular-nums ${tempClass(g.temperature_c)}`}><Thermometer aria-hidden="true" size={12} />{g.temperature_c}°C</span>
            </div>
            <div className="mt-1.5 flex items-center gap-2 text-sm tabular-nums">
              <span className="w-12 font-semibold text-sky-300">{g.utilization_percent.toFixed(0)}%</span>
              <ProgressBar value={g.utilization_percent} max={100} color="bg-blue-500" label={`GPU ${g.index} utilization`} />
            </div>
            {g.memory_total_mb > 0 && (
              <div className="mt-1 flex items-center gap-2 text-xs tabular-nums text-slate-300">
                <span className="w-12 truncate">{(g.memory_used_mb / 1024).toFixed(1)}/{(g.memory_total_mb / 1024).toFixed(0)} GB</span>
                <ProgressBar value={g.memory_used_mb} max={g.memory_total_mb} color={vramShare >= 0.98 ? 'bg-red-500' : vramShare >= 0.95 ? 'bg-amber-400' : 'bg-slate-500'} label={`GPU ${g.index} memory allocation`} />
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

import { Cpu, Thermometer } from 'lucide-react';
import type { GPUMetrics } from '../types';

interface GPUPanelProps {
  gpu: GPUMetrics | null;
  loading: boolean;
}

function ProgressBar({ value, max, color }: { value: number; max: number; color: string }) {
  const percent = max > 0 ? (value / max) * 100 : 0;
  return (
    <div className="w-full bg-slate-700 rounded-full h-2">
      <div
        className={`h-2 rounded-full ${color}`}
        style={{ width: `${Math.min(percent, 100)}%` }}
      />
    </div>
  );
}

export function GPUPanel({ gpu, loading }: GPUPanelProps) {
  if (loading || !gpu) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">GPU Status</h3>
        <div className="animate-pulse space-y-4">
          <div className="h-20 bg-slate-700 rounded"></div>
        </div>
      </div>
    );
  }

  if (gpu.gpu_count === 0) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">GPU Status</h3>
        <p className="text-slate-400">No GPUs detected</p>
      </div>
    );
  }

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-slate-200">GPU Status</h3>
        <div className="flex items-center gap-2 text-sm text-slate-400">
          <Cpu size={16} />
          <span>{gpu.gpu_count} GPU{gpu.gpu_count > 1 ? 's' : ''}</span>
        </div>
      </div>

      <div className="space-y-4">
        {gpu.gpus.map((g) => (
          <div key={g.index} className="border border-slate-700 rounded-lg p-3">
            <div className="flex items-center justify-between mb-2">
              <span className="text-sm font-medium text-slate-300">{g.name}</span>
              <div className="flex items-center gap-1 text-sm">
                <Thermometer size={14} className={g.temperature > 80 ? 'text-red-400' : 'text-slate-400'} />
                <span className={g.temperature > 80 ? 'text-red-400' : 'text-slate-400'}>
                  {g.temperature}°C
                </span>
              </div>
            </div>

            <div className="space-y-2">
              <div>
                <div className="flex justify-between text-xs text-slate-400 mb-1">
                  <span>Utilization</span>
                  <span>{g.utilization.toFixed(0)}%</span>
                </div>
                <ProgressBar
                  value={g.utilization}
                  max={100}
                  color={g.utilization > 90 ? 'bg-red-500' : g.utilization > 70 ? 'bg-yellow-500' : 'bg-green-500'}
                />
              </div>

              <div>
                <div className="flex justify-between text-xs text-slate-400 mb-1">
                  <span>Memory</span>
                  <span>{(g.memory_used_mb / 1024).toFixed(1)} / {(g.memory_total_mb / 1024).toFixed(1)} GB</span>
                </div>
                <ProgressBar
                  value={g.memory_used_mb}
                  max={g.memory_total_mb}
                  color={g.memory_used_mb / g.memory_total_mb > 0.9 ? 'bg-red-500' : 'bg-blue-500'}
                />
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

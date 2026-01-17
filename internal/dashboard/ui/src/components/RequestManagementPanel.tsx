import { useState } from 'react';
import { Gauge, Layers, RotateCcw, Settings } from 'lucide-react';
import { api } from '../api/client';
import type { RequestManagement } from '../types';

interface RequestManagementPanelProps {
  data: RequestManagement | null;
  loading: boolean;
  onRefresh: () => void;
}

function ProgressRing({ value, max, size = 60, color }: { value: number; max: number; size?: number; color: string }) {
  const percent = max > 0 ? (value / max) * 100 : 0;
  const strokeWidth = 6;
  const radius = (size - strokeWidth) / 2;
  const circumference = radius * 2 * Math.PI;
  const offset = circumference - (percent / 100) * circumference;

  return (
    <div className="relative" style={{ width: size, height: size }}>
      <svg className="transform -rotate-90" width={size} height={size}>
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          stroke="currentColor"
          strokeWidth={strokeWidth}
          fill="none"
          className="text-slate-700"
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          stroke="currentColor"
          strokeWidth={strokeWidth}
          fill="none"
          strokeDasharray={circumference}
          strokeDashoffset={offset}
          strokeLinecap="round"
          className={color}
        />
      </svg>
      <div className="absolute inset-0 flex items-center justify-center">
        <span className="text-xs font-medium text-slate-300">{percent.toFixed(0)}%</span>
      </div>
    </div>
  );
}

export function RequestManagementPanel({ data, loading, onRefresh }: RequestManagementPanelProps) {
  const [showSettings, setShowSettings] = useState(false);
  const [rateLimit, setRateLimit] = useState('');
  const [concurrencyLimit, setConcurrencyLimit] = useState('');
  const [saving, setSaving] = useState(false);

  const handleSaveRateLimit = async () => {
    const rate = parseFloat(rateLimit);
    if (isNaN(rate) || rate <= 0) return;
    setSaving(true);
    try {
      await api.setGlobalRateLimit(rate);
      setRateLimit('');
      onRefresh();
    } catch (err) {
      console.error('Failed to set rate limit:', err);
    } finally {
      setSaving(false);
    }
  };

  const handleSaveConcurrency = async () => {
    const max = parseInt(concurrencyLimit);
    if (isNaN(max) || max <= 0) return;
    setSaving(true);
    try {
      await api.setGlobalConcurrency(max);
      setConcurrencyLimit('');
      onRefresh();
    } catch (err) {
      console.error('Failed to set concurrency:', err);
    } finally {
      setSaving(false);
    }
  };

  if (loading || !data) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">Request Management</h3>
        <div className="animate-pulse space-y-4">
          <div className="h-24 bg-slate-700 rounded"></div>
        </div>
      </div>
    );
  }

  const { rate_limiter, concurrency_limiter, retry_policy } = data;

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-slate-200">Request Management</h3>
        <button
          onClick={() => setShowSettings(!showSettings)}
          className={`p-2 rounded transition-colors ${showSettings ? 'bg-slate-600 text-slate-200' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-700'}`}
        >
          <Settings size={18} />
        </button>
      </div>

      <div className="grid grid-cols-3 gap-4 mb-4">
        {/* Rate Limiter */}
        <div className="text-center">
          <div className="flex justify-center mb-2">
            <ProgressRing
              value={rate_limiter.current_tokens}
              max={rate_limiter.burst_size}
              color="text-blue-500"
            />
          </div>
          <div className="flex items-center justify-center gap-1 text-sm text-slate-400 mb-1">
            <Gauge size={14} />
            <span>Rate Limiter</span>
          </div>
          <div className="text-xs text-slate-500">
            {rate_limiter.current_rate.toFixed(0)} req/s
          </div>
          <div className="text-xs text-slate-500">
            {rate_limiter.total_throttled} throttled
          </div>
        </div>

        {/* Concurrency */}
        <div className="text-center">
          <div className="flex justify-center mb-2">
            <ProgressRing
              value={concurrency_limiter.global_active}
              max={concurrency_limiter.global_max}
              color="text-green-500"
            />
          </div>
          <div className="flex items-center justify-center gap-1 text-sm text-slate-400 mb-1">
            <Layers size={14} />
            <span>Concurrency</span>
          </div>
          <div className="text-xs text-slate-500">
            {concurrency_limiter.global_active} / {concurrency_limiter.global_max} active
          </div>
          <div className="text-xs text-slate-500">
            {concurrency_limiter.total_rejected} rejected
          </div>
        </div>

        {/* Retries */}
        <div className="text-center">
          <div className="flex justify-center mb-2">
            <ProgressRing
              value={retry_policy.total_successes}
              max={retry_policy.total_attempts || 1}
              color="text-yellow-500"
            />
          </div>
          <div className="flex items-center justify-center gap-1 text-sm text-slate-400 mb-1">
            <RotateCcw size={14} />
            <span>Retries</span>
          </div>
          <div className="text-xs text-slate-500">
            {retry_policy.total_retries} retries
          </div>
          <div className="text-xs text-slate-500">
            {retry_policy.total_failures} failures
          </div>
        </div>
      </div>

      {/* Settings Panel */}
      {showSettings && (
        <div className="border-t border-slate-700 pt-4 mt-4 space-y-3">
          <div className="flex items-center gap-2">
            <label className="text-sm text-slate-400 w-32">Rate Limit:</label>
            <input
              type="number"
              value={rateLimit}
              onChange={(e) => setRateLimit(e.target.value)}
              placeholder={`${rate_limiter.current_rate.toFixed(0)}`}
              className="flex-1 bg-slate-700 border border-slate-600 rounded px-2 py-1 text-sm text-slate-200"
            />
            <button
              onClick={handleSaveRateLimit}
              disabled={saving || !rateLimit}
              className="px-3 py-1 text-sm bg-blue-600 hover:bg-blue-500 rounded disabled:opacity-50 transition-colors"
            >
              Set
            </button>
          </div>

          <div className="flex items-center gap-2">
            <label className="text-sm text-slate-400 w-32">Max Concurrent:</label>
            <input
              type="number"
              value={concurrencyLimit}
              onChange={(e) => setConcurrencyLimit(e.target.value)}
              placeholder={`${concurrency_limiter.global_max}`}
              className="flex-1 bg-slate-700 border border-slate-600 rounded px-2 py-1 text-sm text-slate-200"
            />
            <button
              onClick={handleSaveConcurrency}
              disabled={saving || !concurrencyLimit}
              className="px-3 py-1 text-sm bg-blue-600 hover:bg-blue-500 rounded disabled:opacity-50 transition-colors"
            >
              Set
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

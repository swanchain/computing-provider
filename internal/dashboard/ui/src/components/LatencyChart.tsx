import { useEffect, useState } from 'react';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import type { InferenceMetrics } from '../types';

interface LatencyChartProps {
  metrics: InferenceMetrics | null;
}

interface DataPoint {
  time: string;
  avg: number;
  p95: number;
  p99: number;
}

const MAX_POINTS = 30;

export function LatencyChart({ metrics }: LatencyChartProps) {
  const [data, setData] = useState<DataPoint[]>([]);

  useEffect(() => {
    if (!metrics) return;

    const now = new Date();
    const timeStr = `${now.getMinutes().toString().padStart(2, '0')}:${now.getSeconds().toString().padStart(2, '0')}`;

    setData((prev) => {
      const newPoint: DataPoint = {
        time: timeStr,
        avg: metrics.avg_latency_ms,
        p95: metrics.p95_latency_ms,
        p99: metrics.p99_latency_ms,
      };
      const updated = [...prev, newPoint];
      return updated.slice(-MAX_POINTS);
    });
  }, [metrics]);

  if (data.length < 2) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">Latency Over Time</h3>
        <div className="h-48 flex items-center justify-center text-slate-500">
          Collecting data...
        </div>
      </div>
    );
  }

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <h3 className="text-lg font-semibold text-slate-200 mb-4">Latency Over Time</h3>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data}>
            <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
            <XAxis
              dataKey="time"
              stroke="#64748b"
              fontSize={10}
              tickLine={false}
            />
            <YAxis
              stroke="#64748b"
              fontSize={10}
              tickLine={false}
              unit="ms"
            />
            <Tooltip
              contentStyle={{
                backgroundColor: '#1e293b',
                border: '1px solid #334155',
                borderRadius: '6px',
                fontSize: '12px',
              }}
              labelStyle={{ color: '#94a3b8' }}
            />
            <Line
              type="monotone"
              dataKey="avg"
              stroke="#3b82f6"
              strokeWidth={2}
              dot={false}
              name="Avg"
            />
            <Line
              type="monotone"
              dataKey="p95"
              stroke="#f59e0b"
              strokeWidth={1.5}
              dot={false}
              name="P95"
            />
            <Line
              type="monotone"
              dataKey="p99"
              stroke="#ef4444"
              strokeWidth={1}
              dot={false}
              name="P99"
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <div className="flex justify-center gap-4 mt-2 text-xs">
        <div className="flex items-center gap-1">
          <div className="w-3 h-0.5 bg-blue-500"></div>
          <span className="text-slate-400">Avg</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="w-3 h-0.5 bg-yellow-500"></div>
          <span className="text-slate-400">P95</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="w-3 h-0.5 bg-red-500"></div>
          <span className="text-slate-400">P99</span>
        </div>
      </div>
    </div>
  );
}

import type { ReactNode } from 'react';

interface StatusCardProps {
  title: string;
  value: string | number;
  subtitle?: string;
  icon?: ReactNode;
  trend?: 'up' | 'down' | 'neutral';
  color?: 'green' | 'red' | 'yellow' | 'blue' | 'gray';
}

const colorClasses = {
  green: 'text-green-400',
  red: 'text-red-400',
  yellow: 'text-yellow-400',
  blue: 'text-blue-400',
  gray: 'text-gray-400',
};

export function StatusCard({ title, value, subtitle, icon, color = 'blue' }: StatusCardProps) {
  return (
    <div className="min-w-0 rounded-2xl border border-slate-800 bg-slate-900 p-4 sm:p-5">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-slate-300 sm:text-sm">{title}</span>
        {icon && <span aria-hidden="true" className={colorClasses[color]}>{icon}</span>}
      </div>
      <div className={`break-words text-2xl font-semibold tracking-tight tabular-nums sm:text-3xl ${colorClasses[color]}`}>{value}</div>
      {subtitle && <div className="mt-1 text-xs leading-5 text-slate-400">{subtitle}</div>}
    </div>
  );
}

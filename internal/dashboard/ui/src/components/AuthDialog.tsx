import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { KeyRound, X } from 'lucide-react';
import { api } from '../api/client';

interface AuthDialogProps {
  open: boolean;
  onClose: () => void;
  onAuthenticated: () => void;
}

export function AuthDialog({ open, onClose, onAuthenticated }: AuthDialogProps) {
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    setError('');
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.setTimeout(() => inputRef.current?.focus(), 0);
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== 'Tab' || !dialogRef.current) return;
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])',
      ));
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      document.body.style.overflow = previousOverflow;
      previousFocusRef.current?.focus();
    };
  }, [open, onClose]);

  if (!open) return null;

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (!token.trim()) return;
    setSubmitting(true);
    setError('');
    api.setAccessToken(token);
    try {
      await api.getSettings();
      setToken('');
      onAuthenticated();
    } catch (requestError) {
      api.clearAccessToken();
      setError(requestError instanceof Error ? requestError.message : 'The access token was rejected');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      ref={dialogRef}
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="unlock-title"
      aria-describedby="unlock-description"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="w-full max-w-md rounded-2xl border border-slate-700 bg-slate-900 shadow-2xl shadow-black/40">
        <div className="flex items-start justify-between gap-4 border-b border-slate-800 p-5">
          <div className="flex gap-3">
            <div className="rounded-xl bg-blue-500/10 p-2 text-blue-400">
              <KeyRound aria-hidden="true" size={22} />
            </div>
            <div>
              <h2 id="unlock-title" className="text-lg font-semibold text-white">Unlock operator controls</h2>
              <p id="unlock-description" className="mt-1 text-sm text-slate-300">Monitoring stays read-only until this browser tab is unlocked.</p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-2 text-slate-400 hover:bg-slate-800 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            aria-label="Close unlock dialog"
          >
            <X aria-hidden="true" size={20} />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4 p-5">
          <div>
            <label htmlFor="control-token" className="mb-2 block text-sm font-medium text-slate-200">Control token</label>
            <input
              ref={inputRef}
              id="control-token"
              type="password"
              autoComplete="off"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              className="min-h-11 w-full rounded-lg border border-slate-600 bg-slate-950 px-3 font-mono text-sm text-white outline-none transition placeholder:text-slate-400 focus:border-blue-500 focus:ring-2 focus:ring-blue-500/30"
              placeholder="Paste dashboard.token"
              aria-describedby="token-help"
            />
            <p id="token-help" className="mt-2 text-xs leading-5 text-slate-300">
              On the provider host, read <code className="rounded bg-slate-800 px-1.5 py-0.5 text-slate-300">$CP_PATH/dashboard.token</code>. The token is kept only for this browser tab.
            </p>
          </div>

          {error && <p role="alert" className="rounded-lg border border-red-800/70 bg-red-950/40 px-3 py-2 text-sm text-red-300">{error}</p>}

          <div className="flex justify-end gap-3">
            <button type="button" onClick={onClose} className="min-h-10 rounded-lg px-4 text-sm text-slate-300 hover:bg-slate-800">Cancel</button>
            <button
              type="submit"
              disabled={submitting || !token.trim()}
              className="min-h-10 rounded-lg bg-blue-600 px-4 text-sm font-medium text-white transition hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {submitting ? 'Checking…' : 'Unlock'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

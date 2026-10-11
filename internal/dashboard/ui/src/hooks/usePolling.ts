import { useState, useEffect, useCallback, useRef } from 'react';

export interface UsePollingResult<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  refreshing: boolean;
  lastUpdated: Date | null;
  refetch: () => Promise<void>;
}

// Every mounted data panel participates, including slower proxy/chart polls.
export function refreshDashboard() {
  window.dispatchEvent(new Event('dashboard:refresh'));
}

export function usePolling<T>(
  fetchFn: () => Promise<T>,
  intervalMs: number = 5000
): UsePollingResult<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const requestId = useRef(0);
  const previousFetch = useRef(fetchFn);
  const cancelPending = useCallback(() => { requestId.current++; }, []);

  const refetch = useCallback(async () => {
    const id = ++requestId.current;
    // A different time range or scope must not inherit the previous query's totals.
    if (previousFetch.current !== fetchFn) {
      previousFetch.current = fetchFn;
      setData(null);
      setError(null);
      setLastUpdated(null);
      setLoading(true);
    }
    setRefreshing(true);
    try {
      const result = await fetchFn();
      if (id !== requestId.current) return;
      setData(result);
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      if (id !== requestId.current) return;
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      if (id === requestId.current) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }, [fetchFn]);

  useEffect(() => {
    refetch();
    // A non-positive interval fetches once and then leaves the data alone —
    // for views where a background refresh would move content the reader is
    // in the middle of, such as a paged list.
    const interval = intervalMs > 0 ? setInterval(refetch, intervalMs) : undefined;
    return () => { clearInterval(interval); cancelPending(); };
  }, [refetch, intervalMs, cancelPending]);

  useEffect(() => {
    const refresh = () => { void refetch(); };
    window.addEventListener('dashboard:refresh', refresh);
    return () => window.removeEventListener('dashboard:refresh', refresh);
  }, [refetch]);

  return { data, error, loading, refreshing, lastUpdated, refetch };
}

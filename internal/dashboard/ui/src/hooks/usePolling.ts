import { useState, useEffect, useCallback } from 'react';

interface UsePollingResult<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  refreshing: boolean;
  lastUpdated: Date | null;
  refetch: () => Promise<void>;
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

  const refetch = useCallback(async () => {
    setRefreshing(true);
    try {
      const result = await fetchFn();
      setData(result);
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [fetchFn]);

  useEffect(() => {
    refetch();
    // A non-positive interval fetches once and then leaves the data alone —
    // for views where a background refresh would move content the reader is
    // in the middle of, such as a paged list.
    if (intervalMs <= 0) return;
    const interval = setInterval(refetch, intervalMs);
    return () => clearInterval(interval);
  }, [refetch, intervalMs]);

  return { data, error, loading, refreshing, lastUpdated, refetch };
}

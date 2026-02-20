import { useState, useEffect, useCallback } from 'react';

const API_BASE = '';

async function apiFetch<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`);
  if (!res.ok) {
    throw new Error(`API ${path}: ${res.status} ${res.statusText}`);
  }
  return res.json();
}

export function useApi<T>(path: string, interval = 5000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(() => {
    apiFetch<T>(path)
      .then((d) => { setData(d); setError(null); })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, [path]);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, interval);
    return () => clearInterval(id);
  }, [refresh, interval]);

  return { data, error, loading, refresh };
}

export async function fetchLogs(namespace: string, pod: string, container?: string, tail = 100): Promise<string> {
  let url = `/api/logs/${namespace}/${pod}?tail=${tail}`;
  if (container) url += `&container=${container}`;
  const res = await apiFetch<{ logs: string }>(url);
  return res.logs;
}

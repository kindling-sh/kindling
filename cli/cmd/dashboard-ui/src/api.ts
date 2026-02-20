import { useState, useEffect, useCallback } from 'react';

const API_BASE = '';

// ── Action result type from backend ──────────────────────────────

export interface ActionResult {
  ok: boolean;
  output?: string;
  error?: string;
}

// ── Read helpers ─────────────────────────────────────────────────

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

// ── Write helpers ────────────────────────────────────────────────

export async function apiPost(path: string, body?: object): Promise<ActionResult> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : '{}',
  });
  return res.json();
}

export async function apiDelete(path: string): Promise<ActionResult> {
  const res = await fetch(`${API_BASE}${path}`, { method: 'DELETE' });
  return res.json();
}

export async function fetchEnvVars(namespace: string, deployment: string): Promise<{ name: string; value: string }[]> {
  return apiFetch(`/api/env/list/${namespace}/${deployment}`);
}

export async function fetchExposeStatus(): Promise<{ running: boolean; url?: string }> {
  return apiFetch('/api/expose/status');
}

// ── Stream helper for init (ndjson) ──────────────────────────────

export async function streamInit(onMessage: (msg: string) => void): Promise<ActionResult> {
  const res = await fetch(`${API_BASE}/api/init`, { method: 'POST' });
  const reader = res.body?.getReader();
  if (!reader) return { ok: false, error: 'no response body' };

  const decoder = new TextDecoder();
  let buffer = '';
  let lastResult: ActionResult = { ok: false, error: 'no result' };

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';
    for (const line of lines) {
      if (!line.trim()) continue;
      try {
        const parsed = JSON.parse(line);
        if (parsed.status) onMessage(parsed.status);
        if (parsed.ok !== undefined) lastResult = parsed;
      } catch { /* skip malformed */ }
    }
  }
  return lastResult;
}

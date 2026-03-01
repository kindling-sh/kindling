import { useState, useEffect, useCallback } from 'react';
import type { RuntimeInfo, SyncStatus, ServiceDir, IntelStatus, TopologyGraph, TopologyStatusMap, TopologyNodeDetail, TopologyLogs } from './types';

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

export async function fetchExposeStatus(): Promise<{ running: boolean; url?: string; dns_ready?: boolean }> {
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

// ── Runtime / Sync / Load helpers ────────────────────────────────

export async function fetchRuntimeInfo(namespace: string, deployment: string, src?: string): Promise<RuntimeInfo> {
  let url = `/api/runtime/${namespace}/${deployment}`;
  if (src) url += `?src=${encodeURIComponent(src)}`;
  return apiFetch<RuntimeInfo>(url);
}

export async function fetchSyncStatus(): Promise<SyncStatus> {
  return apiFetch<SyncStatus>('/api/sync/status');
}

export async function fetchServiceDirs(): Promise<ServiceDir[]> {
  return apiFetch<ServiceDir[]>('/api/load-context');
}

// ── Intel helpers ────────────────────────────────────────────────

export async function fetchIntelStatus(): Promise<IntelStatus> {
  return apiFetch<IntelStatus>('/api/intel');
}

export async function activateIntel(): Promise<ActionResult> {
  return apiPost('/api/intel');
}

export async function deactivateIntel(): Promise<ActionResult> {
  return apiDelete('/api/intel');
}

// ── Generate helper (ndjson stream) ──────────────────────────────

export interface GenerateResult extends ActionResult {
  workflow?: string;
  path?: string;
}

export async function streamGenerate(
  body: { apiKey: string; repoPath?: string; provider?: string; model?: string; ciProvider?: string; branch?: string; dryRun?: boolean },
  onMessage: (msg: string) => void,
): Promise<GenerateResult> {
  const res = await fetch(`${API_BASE}/api/generate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const reader = res.body?.getReader();
  if (!reader) return { ok: false, error: 'no response body' };

  const decoder = new TextDecoder();
  let buffer = '';
  let lastResult: GenerateResult = { ok: false, error: 'no result' };

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

// ── Topology helpers ─────────────────────────────────────────────

export async function fetchTopology(): Promise<TopologyGraph> {
  return apiFetch<TopologyGraph>('/api/topology');
}

export async function fetchTopologyStatus(): Promise<TopologyStatusMap> {
  return apiFetch<TopologyStatusMap>('/api/topology/status');
}

export async function fetchTopologyLogs(nodeId: string, tail = 200): Promise<TopologyLogs> {
  return apiFetch<TopologyLogs>(`/api/topology/logs?node=${encodeURIComponent(nodeId)}&tail=${tail}`);
}

export async function fetchTopologyNodeDetail(nodeId: string): Promise<TopologyNodeDetail> {
  return apiFetch<TopologyNodeDetail>(`/api/topology/node/detail?node=${encodeURIComponent(nodeId)}`);
}

export async function scaleDeployment(namespace: string, deployment: string, replicas: number): Promise<ActionResult> {
  return apiPost(`/api/scale/${encodeURIComponent(namespace)}/${encodeURIComponent(deployment)}`, { replicas });
}

export async function deployTopology(graph: TopologyGraph): Promise<ActionResult> {
  return apiPost('/api/topology/deploy', graph);
}

export async function scaffoldService(body: { name: string; path: string; port?: number; language?: string; deps?: { envVar: string; value: string }[] }): Promise<ActionResult & { path?: string; language?: string }> {
  return apiPost('/api/topology/scaffold', body);
}

export async function checkPath(path: string): Promise<{ exists: boolean; has_dockerfile: boolean; language: string }> {
  return apiFetch(`/api/topology/check-path?path=${encodeURIComponent(path)}`);
}

export async function fetchWorkspaceInfo(): Promise<{ root: string; services: string[] }> {
  return apiFetch('/api/topology/workspace');
}

export async function cleanupService(body: {
  name: string;
  path?: string;
  dseName?: string;
  referencedBy?: string[];
}): Promise<ActionResult> {
  return apiPost('/api/topology/cleanup', body);
}

export async function removeEdgeFromCluster(body: {
  dseName: string;
  envVar?: string;
  depType?: string;
}): Promise<ActionResult> {
  return apiPost('/api/topology/edge/remove', body);
}

export async function saveCanvas(overlay: { nodes: unknown[]; edges: unknown[]; positions?: Record<string, { x: number; y: number }> }): Promise<{ ok: boolean }> {
  return apiPost('/api/topology/canvas', overlay);
}

// ── Proxy / API Explorer helpers ─────────────────────────────────

export interface ProxyService {
  name: string;
  namespace: string;
  port: number;
  ports: { name: string; port: number }[];
}

export interface ProxyResponse {
  ok: boolean;
  status?: number;
  elapsed?: number;
  headers?: Record<string, string>;
  body?: string;
  size?: number;
  error?: string;
}

export async function fetchProxyServices(): Promise<ProxyService[]> {
  return apiFetch<ProxyService[]>('/api/proxy/services');
}

export async function proxyRequest(body: {
  service: string;
  namespace?: string;
  port: number;
  method: string;
  path: string;
  headers?: Record<string, string>;
  body?: string;
}): Promise<ProxyResponse> {
  const res = await fetch(`${API_BASE}/api/proxy`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return res.json();
}

// ── Debug API ───────────────────────────────────────────────────

export interface DebugSession {
  deployment: string;
  namespace?: string;
  runtime: string;
  localPort: number;
  remotePort?: number;
  launchConfig?: Record<string, unknown>;
}

export interface DebugStatus {
  active: boolean;
  deployment: string;
  runtime?: string;
  localPort?: number;
}

export async function startDebugSession(deployment: string, namespace = 'default'): Promise<DebugSession & { status: string }> {
  const res = await fetch(`${API_BASE}/api/debug`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ deployment, namespace }),
  });
  return res.json();
}

export async function stopDebugSession(deployment: string, namespace = 'default'): Promise<{ status: string }> {
  const res = await fetch(`${API_BASE}/api/debug`, {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ deployment, namespace }),
  });
  return res.json();
}

export async function fetchDebugStatus(deployment?: string, namespace = 'default'): Promise<DebugStatus | { sessions: DebugSession[] }> {
  const params = new URLSearchParams();
  if (deployment) params.set('deployment', deployment);
  params.set('namespace', namespace);
  return apiFetch(`/api/debug/status?${params}`);
}

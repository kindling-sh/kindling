import { useState } from 'react';
import { useApi, fetchLogs } from '../api';
import type { K8sList, K8sPod } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';

export function PodsPage() {
  const { data, loading } = useApi<K8sList<K8sPod>>('/api/pods');
  const [logPod, setLogPod] = useState<{ ns: string; name: string } | null>(null);
  const [logs, setLogs] = useState<string>('');
  const [logLoading, setLogLoading] = useState(false);

  async function showLogs(ns: string, name: string) {
    setLogPod({ ns, name });
    setLogLoading(true);
    try {
      const text = await fetchLogs(ns, name);
      setLogs(text);
    } catch {
      setLogs('Failed to fetch logs.');
    } finally {
      setLogLoading(false);
    }
  }

  if (loading) return <div className="loading">Loading pods…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Pods</h1>

      {logPod && (
        <div className="log-viewer">
          <div className="log-header">
            <h3>
              <span className="mono">{logPod.ns}/{logPod.name}</span>
            </h3>
            <button className="btn btn-sm" onClick={() => { setLogPod(null); setLogs(''); }}>
              ✕ Close
            </button>
          </div>
          <pre className="log-output">
            {logLoading ? 'Loading logs…' : (logs || '(no logs)')}
          </pre>
        </div>
      )}

      {items.length === 0 ? (
        <EmptyState message="No pods found." />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Namespace</th>
              <th>Phase</th>
              <th>Ready</th>
              <th>Restarts</th>
              <th>Node</th>
              <th>Age</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {items.map((p) => {
              const phase = p.status?.phase ?? 'Unknown';
              const ok = phase === 'Running' || phase === 'Succeeded';
              const containers = p.status?.containerStatuses ?? [];
              const readyCount = containers.filter((c) => c.ready).length;
              const total = containers.length || (p.spec?.containers?.length ?? 0);
              const restarts = containers.reduce((s, c) => s + (c.restartCount ?? 0), 0);

              return (
                <tr key={`${p.metadata.namespace}/${p.metadata.name}`}>
                  <td className="mono">{p.metadata.name}</td>
                  <td>{p.metadata.namespace}</td>
                  <td><StatusBadge ok={ok} label={phase} /></td>
                  <td>{readyCount}/{total}</td>
                  <td>{restarts > 0 ? <span className="warn-text">{restarts}</span> : '0'}</td>
                  <td className="mono">{p.spec?.nodeName ?? '—'}</td>
                  <td><TimeAgo timestamp={p.metadata.creationTimestamp} /></td>
                  <td>
                    <button
                      className="btn btn-sm"
                      onClick={() => showLogs(p.metadata.namespace!, p.metadata.name)}
                    >
                      📋 Logs
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

import { useState } from 'react';
import { useApi, apiPost, apiDelete, fetchExposeStatus, streamInit } from '../api';
import type { ActionResult } from '../api';
import type { ClusterInfo, K8sList, K8sNode, K8sPod } from '../types';
import { StatusBadge, DeploymentStatus } from './shared';
import { ActionButton, ConfirmDialog, ResultOutput, useToast } from './actions';

export function OverviewPage() {
  const { data: cluster, loading: cl, refresh } = useApi<ClusterInfo>('/api/cluster');
  const { data: nodes } = useApi<K8sList<K8sNode>>('/api/nodes');
  const { data: ingressPods } = useApi<K8sList<K8sPod>>('/api/ingress-controller');
  const { toast } = useToast();

  const [showDestroy, setShowDestroy] = useState(false);
  const [initRunning, setInitRunning] = useState(false);
  const [initMessages, setInitMessages] = useState<string[]>([]);
  const [initResult, setInitResult] = useState<ActionResult | null>(null);
  const [tunnelStatus, setTunnelStatus] = useState<{ running: boolean; url?: string } | null>(null);

  // Fetch tunnel status on mount
  useState(() => {
    fetchExposeStatus().then(setTunnelStatus).catch(() => {});
  });

  async function handleInit() {
    setInitRunning(true);
    setInitMessages([]);
    setInitResult(null);
    const result = await streamInit((msg) => setInitMessages((m) => [...m, msg]));
    setInitResult(result);
    setInitRunning(false);
    if (result.ok) {
      toast('Cluster initialized', 'success');
      refresh();
    } else {
      toast(result.error || 'Init failed', 'error');
    }
  }

  async function handleDestroy() {
    setShowDestroy(false);
    const result = await apiDelete('/api/cluster/destroy');
    if (result.ok) {
      toast('Cluster destroyed', 'success');
      refresh();
    } else {
      toast(result.error || 'Destroy failed', 'error');
    }
  }

  async function toggleTunnel() {
    if (tunnelStatus?.running) {
      const result = await apiDelete('/api/expose');
      if (result.ok) {
        toast('Tunnel stopped', 'success');
        setTunnelStatus({ running: false });
      } else {
        toast(result.error || 'Failed to stop tunnel', 'error');
      }
    } else {
      const result = await apiPost('/api/expose');
      if (result.ok) {
        toast(result.output || 'Tunnel started', 'success');
        fetchExposeStatus().then(setTunnelStatus);
      } else {
        toast(result.error || 'Failed to start tunnel', 'error');
      }
    }
  }

  if (cl) return <div className="loading">Loading cluster info…</div>;
  if (!cluster) return <div className="error">Failed to load cluster info</div>;

  return (
    <div className="page">
      <div className="page-header">
        <h1>Cluster Overview</h1>
        <div className="page-actions">
          {!cluster.exists && (
            <ActionButton icon="🚀" label="Init Cluster" onClick={handleInit} disabled={initRunning} />
          )}
          {cluster.exists && (
            <>
              <ActionButton
                icon={tunnelStatus?.running ? '🔴' : '🟢'}
                label={tunnelStatus?.running ? 'Stop Tunnel' : 'Start Tunnel'}
                onClick={toggleTunnel}
              />
              <ActionButton icon="💣" label="Destroy Cluster" onClick={() => setShowDestroy(true)} danger />
            </>
          )}
        </div>
      </div>

      {showDestroy && (
        <ConfirmDialog
          title="Destroy Cluster"
          message={`This will permanently delete the '${cluster.name}' Kind cluster and all resources. This cannot be undone.`}
          confirmLabel="Destroy"
          danger
          onConfirm={handleDestroy}
          onCancel={() => setShowDestroy(false)}
        />
      )}

      {(initMessages.length > 0 || initResult) && (
        <div className="init-progress">
          <h3>Init Progress</h3>
          <div className="log-output">
            {initMessages.map((m, i) => <div key={i}>{m}</div>)}
          </div>
          <ResultOutput result={initResult} />
        </div>
      )}

      {tunnelStatus?.running && tunnelStatus.url && (
        <div className="card card-wide tunnel-card">
          <div className="card-header">
            <span className="card-icon">🔗</span>
            <h3>Public Tunnel</h3>
            <StatusBadge ok label="Active" />
          </div>
          <div className="card-body">
            <div className="stat-row">
              <span className="label">URL</span>
              <a href={tunnelStatus.url} target="_blank" rel="noopener" className="tunnel-url">{tunnelStatus.url}</a>
            </div>
          </div>
        </div>
      )}

      <div className="cards">
        <div className="card">
          <div className="card-header">
            <span className="card-icon">☸️</span>
            <h3>Cluster</h3>
          </div>
          <div className="card-body">
            <div className="stat-row">
              <span className="label">Name</span>
              <span className="value">{cluster.name}</span>
            </div>
            <div className="stat-row">
              <span className="label">Context</span>
              <span className="value mono">{cluster.context}</span>
            </div>
            <div className="stat-row">
              <span className="label">Status</span>
              <StatusBadge ok={cluster.exists} label={cluster.exists ? 'Running' : 'Not Found'} />
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">⚙️</span>
            <h3>Operator</h3>
          </div>
          <div className="card-body">
            {cluster.operator ? (
              <DeploymentStatus dep={cluster.operator} />
            ) : (
              <span className="text-dim">Not installed</span>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">📦</span>
            <h3>Registry</h3>
          </div>
          <div className="card-body">
            {cluster.registry ? (
              <DeploymentStatus dep={cluster.registry} />
            ) : (
              <span className="text-dim">Not installed</span>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">🌐</span>
            <h3>Ingress Controller</h3>
          </div>
          <div className="card-body">
            {ingressPods?.items?.length ? (
              ingressPods.items.map((p) => (
                <div key={p.metadata.name} className="stat-row">
                  <span className="label">{p.metadata.name}</span>
                  <StatusBadge ok={p.status.phase === 'Running'} label={p.status.phase || 'Unknown'} />
                </div>
              ))
            ) : (
              <span className="text-dim">No ingress controller pods</span>
            )}
          </div>
        </div>
      </div>

      {nodes?.items && (
        <div className="section">
          <h2>Nodes</h2>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Status</th>
                <th>Version</th>
                <th>OS</th>
                <th>Runtime</th>
              </tr>
            </thead>
            <tbody>
              {nodes.items.map((n) => {
                const ready = n.status.conditions?.find((c) => c.type === 'Ready');
                return (
                  <tr key={n.metadata.name}>
                    <td className="mono">{n.metadata.name}</td>
                    <td><StatusBadge ok={ready?.status === 'True'} label={ready?.status === 'True' ? 'Ready' : 'NotReady'} /></td>
                    <td>{n.status.nodeInfo?.kubeletVersion}</td>
                    <td>{n.status.nodeInfo?.osImage}</td>
                    <td>{n.status.nodeInfo?.containerRuntimeVersion}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

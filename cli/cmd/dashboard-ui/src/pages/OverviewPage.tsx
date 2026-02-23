import { useState } from 'react';
import { useApi, streamInit } from '../api';
import type { ActionResult } from '../api';
import type { ClusterInfo, K8sList, K8sNode, K8sPod, K8sDeployment } from '../types';
import { StatusBadge, DeploymentStatus } from './shared';
import { ActionButton, ResultOutput, useToast } from './actions';

export function OverviewPage() {
  const { data: cluster, loading: cl, refresh } = useApi<ClusterInfo>('/api/cluster');
  const { data: nodes } = useApi<K8sList<K8sNode>>('/api/nodes');
  const { data: ingressPods } = useApi<K8sList<K8sPod>>('/api/ingress-controller');
  const { data: deployments } = useApi<K8sList<K8sDeployment>>('/api/deployments');
  const { data: allPods } = useApi<K8sList<K8sPod>>('/api/pods');
  const { toast } = useToast();

  const [initRunning, setInitRunning] = useState(false);
  const [initMessages, setInitMessages] = useState<string[]>([]);
  const [initResult, setInitResult] = useState<ActionResult | null>(null);

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

  if (cl) return <div className="loading">Loading cluster info…</div>;
  if (!cluster) return <div className="loading">Failed to load cluster info</div>;

  // Compute metrics
  const totalPods = allPods?.items?.length ?? 0;
  const runningPods = allPods?.items?.filter(p => p.status?.phase === 'Running').length ?? 0;
  const totalDeps = deployments?.items?.length ?? 0;
  const readyDeps = deployments?.items?.filter(d => (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1)).length ?? 0;
  const nodeCount = nodes?.items?.length ?? 0;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Cluster Overview</h1>
          <p className="page-subtitle">Kind cluster "{cluster.name}" · context {cluster.context}</p>
        </div>
        <div className="page-actions">
          {!cluster.exists && (
            <ActionButton icon="▶" label="Init Cluster" onClick={handleInit} disabled={initRunning} primary />
          )}
        </div>
      </div>

      {(initMessages.length > 0 || initResult) && (
        <div className="init-progress">
          <h3>Initialization Progress</h3>
          <div className="log-output">
            {initMessages.map((m, i) => <div key={i}>{m}</div>)}
          </div>
          <ResultOutput result={initResult} />
        </div>
      )}

      {/* Metrics */}
      {cluster.exists && (
        <div className="metric-row">
          <div className="metric-card">
            <div className="metric-label">Nodes</div>
            <div className="metric-value">{nodeCount}</div>
          </div>
          <div className="metric-card">
            <div className="metric-label">Deployments</div>
            <div className="metric-value">{readyDeps}<span style={{ fontSize: 16, color: 'var(--text-tertiary)' }}>/{totalDeps}</span></div>
            <div className="metric-sub">ready</div>
          </div>
          <div className="metric-card">
            <div className="metric-label">Pods</div>
            <div className={`metric-value ${runningPods === totalPods && totalPods > 0 ? 'text-green' : runningPods < totalPods ? 'text-yellow' : ''}`}>
              {runningPods}<span style={{ fontSize: 16, color: 'var(--text-tertiary)' }}>/{totalPods}</span>
            </div>
            <div className="metric-sub">running</div>
          </div>
          <div className="metric-card">
            <div className="metric-label">Cluster Status</div>
            <div className={`metric-value ${cluster.exists ? 'text-green' : 'text-red'}`}>
              {cluster.exists ? '●' : '○'}
            </div>
            <div className="metric-sub">{cluster.exists ? 'Running' : 'Not Found'}</div>
          </div>
        </div>
      )}

      {/* Infrastructure cards */}
      <div className="card-grid card-grid-3">
        <div className="card">
          <div className="card-header">
            <span className="card-icon">⬡</span>
            <h3>Operator</h3>
          </div>
          <div className="card-body">
            {cluster.operator ? (
              <DeploymentStatus dep={cluster.operator} />
            ) : (
              <span className="text-dim" style={{ fontSize: 13 }}>Not installed</span>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">◈</span>
            <h3>Registry</h3>
          </div>
          <div className="card-body">
            {cluster.registry ? (
              <DeploymentStatus dep={cluster.registry} />
            ) : (
              <span className="text-dim" style={{ fontSize: 13 }}>Not installed</span>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">◎</span>
            <h3>Ingress Controller</h3>
          </div>
          <div className="card-body">
            {ingressPods?.items?.length ? (
              ingressPods.items.map((p) => (
                <div key={p.metadata.name} className="stat-row" style={{ flexWrap: 'nowrap' }}>
                  <span className="label mono" style={{ fontSize: 11, overflow: 'auto', whiteSpace: 'nowrap', minWidth: 0, flex: '1 1 0' }}>{p.metadata.name}</span>
                  <StatusBadge ok={p.status.phase === 'Running'} label={p.status.phase || 'Unknown'} />
                </div>
              ))
            ) : (
              <span className="text-dim" style={{ fontSize: 13 }}>No ingress controller</span>
            )}
          </div>
        </div>
      </div>

      {/* Nodes table */}
      {nodes?.items && nodes.items.length > 0 && (
        <>
          <h2 className="section-title">Nodes</h2>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Version</th>
                  <th>OS</th>
                  <th>Arch</th>
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
                      <td className="mono">{n.status.nodeInfo?.kubeletVersion}</td>
                      <td>{n.status.nodeInfo?.osImage}</td>
                      <td>{n.status.nodeInfo?.architecture}</td>
                      <td className="mono">{n.status.nodeInfo?.containerRuntimeVersion}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}

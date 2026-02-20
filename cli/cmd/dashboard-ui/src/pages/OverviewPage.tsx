import { useApi } from '../api';
import type { ClusterInfo, K8sList, K8sNode, K8sPod } from '../types';
import { StatusBadge, DeploymentStatus } from './shared';

export function OverviewPage() {
  const { data: cluster, loading: cl } = useApi<ClusterInfo>('/api/cluster');
  const { data: nodes } = useApi<K8sList<K8sNode>>('/api/nodes');
  const { data: ingressPods } = useApi<K8sList<K8sPod>>('/api/ingress-controller');

  if (cl) return <div className="loading">Loading cluster info…</div>;
  if (!cluster) return <div className="error">Failed to load cluster info</div>;

  return (
    <div className="page">
      <h1>Cluster Overview</h1>

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

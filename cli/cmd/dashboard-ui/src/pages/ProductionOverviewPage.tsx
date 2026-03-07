import { useApi } from '../api';
import { fetchProdNodeMetrics, fetchProdAdvisor, fetchMetricsStatus } from '../api';
import { useState, useEffect } from 'react';
import type { ProdClusterInfo, K8sList, K8sNode, K8sDeployment, K8sPod, NodeMetric, PrometheusStatus, Advisory, MetricsStackStatus } from '../types';
import { StatusBadge, TimeAgo } from './shared';

function parsePct(s: string): number {
  return parseInt(s.replace('%', ''), 10) || 0;
}

export function ProductionOverviewPage() {
  const { data: cluster, loading } = useApi<ProdClusterInfo>('/api/prod/cluster');
  const { data: nodes } = useApi<K8sList<K8sNode>>('/api/prod/nodes');
  const { data: deployments } = useApi<K8sList<K8sDeployment>>('/api/prod/deployments');
  const { data: pods } = useApi<K8sList<K8sPod>>('/api/prod/pods');
  const { data: prom } = useApi<PrometheusStatus>('/api/prod/prometheus/status', 15000);

  const [nodeMetrics, setNodeMetrics] = useState<NodeMetric[]>([]);
  const [metricsStack, setMetricsStack] = useState<MetricsStackStatus | null>(null);
  const [advisories, setAdvisories] = useState<Advisory[]>([]);
  const [advisorLoading, setAdvisorLoading] = useState(true);
  const [advisorChecked, setAdvisorChecked] = useState('');

  useEffect(() => {
    fetchProdNodeMetrics().then(r => setNodeMetrics(r.items || [])).catch(() => {});
    fetchMetricsStatus().then(s => setMetricsStack(s)).catch(() => {});
    const id = setInterval(() => {
      fetchProdNodeMetrics().then(r => setNodeMetrics(r.items || [])).catch(() => {});
    }, 10000);
    return () => clearInterval(id);
  }, []);

  // Advisor poll — check every 30s
  useEffect(() => {
    const load = () => {
      fetchProdAdvisor().then(r => {
        setAdvisories(r.advisories || []);
        setAdvisorChecked(r.checked_at || '');
        setAdvisorLoading(false);
      }).catch(() => setAdvisorLoading(false));
    };
    load();
    const id = setInterval(load, 30000);
    return () => clearInterval(id);
  }, []);

  if (loading) return <div className="loading">Connecting to production cluster…</div>;
  if (!cluster || !cluster.connected) {
    return (
      <div className="page">
        <div className="page-header"><div className="page-header-left">
          <h1>Production Cluster</h1>
          <p className="page-subtitle">Not connected — start the dashboard with <code>--prod-context</code></p>
        </div></div>
        <div className="prod-disconnected">
          <div className="prod-disconnected-icon">⚠</div>
          <h2>No Production Context</h2>
          <p>Launch the dashboard with a production kubeconfig context:</p>
          <pre className="prod-code-block">kindling dashboard --prod-context &lt;your-context&gt;</pre>
          <p className="text-dim" style={{ marginTop: 12, fontSize: 13 }}>
            Available contexts are listed by <code>kubectl config get-contexts -o name</code>
          </p>
        </div>
      </div>
    );
  }

  const totalPods = pods?.items?.length ?? 0;
  const runningPods = pods?.items?.filter(p => p.status?.phase === 'Running').length ?? 0;
  const failedPods = pods?.items?.filter(p => p.status?.phase === 'Failed' || p.status?.phase === 'CrashLoopBackOff').length ?? 0;
  const totalDeps = deployments?.items?.length ?? 0;
  const readyDeps = deployments?.items?.filter(d => (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1)).length ?? 0;
  const nodeCount = nodes?.items?.length ?? 0;

  // Aggregate node metrics
  const avgCPU = nodeMetrics.length > 0 ? Math.round(nodeMetrics.reduce((s, m) => s + parsePct(m.cpu_pct), 0) / nodeMetrics.length) : 0;
  const avgMem = nodeMetrics.length > 0 ? Math.round(nodeMetrics.reduce((s, m) => s + parsePct(m.mem_pct), 0) / nodeMetrics.length) : 0;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Production Overview</h1>
          <p className="page-subtitle">
            <span className="prod-provider-badge">{cluster.provider}</span>
            {cluster.version && <span className="tag" style={{ marginLeft: 8 }}>{cluster.version}</span>}
            <span style={{ marginLeft: 8, color: 'var(--text-tertiary)' }}>{cluster.context}</span>
          </p>
        </div>
        <div className="page-actions">
          <span className={`prod-status-pill ${cluster.connected ? 'prod-status-ok' : 'prod-status-err'}`}>
            <span className="prod-status-dot" /> {cluster.connected ? 'Connected' : 'Disconnected'}
          </span>
        </div>
      </div>

      {/* Key metrics */}
      <div className="metric-row">
        <div className="metric-card">
          <div className="metric-label">Nodes</div>
          <div className="metric-value">{nodeCount}</div>
        </div>
        <div className="metric-card">
          <div className="metric-label">Deployments</div>
          <div className="metric-value">
            {readyDeps}<span style={{ fontSize: 16, color: 'var(--text-tertiary)' }}>/{totalDeps}</span>
          </div>
          <div className="metric-sub">ready</div>
        </div>
        <div className="metric-card">
          <div className={`metric-value ${runningPods === totalPods && totalPods > 0 ? 'text-green' : failedPods > 0 ? 'text-red' : 'text-yellow'}`}>
            {runningPods}<span style={{ fontSize: 16, color: 'var(--text-tertiary)' }}>/{totalPods}</span>
          </div>
          <div className="metric-label">Pods Running</div>
          {failedPods > 0 && <div className="metric-sub text-red">{failedPods} unhealthy</div>}
        </div>
        <div className="metric-card">
          <div className="metric-label">Avg CPU</div>
          <div className={`metric-value ${avgCPU > 80 ? 'text-red' : avgCPU > 60 ? 'text-yellow' : 'text-green'}`}>{avgCPU}%</div>
        </div>
        <div className="metric-card">
          <div className="metric-label">Avg Memory</div>
          <div className={`metric-value ${avgMem > 85 ? 'text-red' : avgMem > 70 ? 'text-yellow' : 'text-green'}`}>{avgMem}%</div>
        </div>
      </div>

      {/* Cluster advisor */}
      {!advisorLoading && advisories.length > 0 && (
        <div className="card advisor-card" style={{ marginBottom: 20 }}>
          <div className="card-header">
            <span className="card-icon">{advisories.some(a => a.severity === 'critical') ? '🔴' : advisories.some(a => a.severity === 'warning') ? '🟡' : '🟢'}</span>
            <h3>Cluster Advisor</h3>
            {advisorChecked && (
              <span className="text-dim" style={{ marginLeft: 'auto', fontSize: 11 }}>
                checked <TimeAgo timestamp={advisorChecked} />
              </span>
            )}
          </div>
          <div className="card-body" style={{ padding: 0 }}>
            <div className="advisor-list">
              {advisories.map((a, i) => (
                <div key={i} className={`advisor-item advisor-${a.severity}`}>
                  <div className="advisor-severity">
                    {a.severity === 'critical' ? '●' : a.severity === 'warning' ? '▲' : '✓'}
                  </div>
                  <div className="advisor-content">
                    <div className="advisor-title">{a.title}</div>
                    <div className="advisor-detail">{a.detail}</div>
                    {a.action && <div className="advisor-action">→ {a.action}</div>}
                    {a.resource && <span className="advisor-resource">{a.resource}</span>}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Infrastructure status */}
      <div className="card-grid card-grid-3">
        <div className="card">
          <div className="card-header">
            <span className="card-icon">◇</span>
            <h3>VictoriaMetrics</h3>
          </div>
          <div className="card-body">
            <div className="stat-row">
              <span className="label">Status</span>
              <StatusBadge ok={!!metricsStack?.victoria_metrics || !!prom?.detected} label={metricsStack?.victoria_metrics ? 'Running' : prom?.detected ? 'Detected' : 'Not Installed'} />
            </div>
            {metricsStack?.vm_version && (
              <div className="stat-row">
                <span className="label">Version</span>
                <span className="value mono">{metricsStack.vm_version}</span>
              </div>
            )}
            {prom?.detected && (
              <div className="stat-row">
                <span className="label">Endpoint</span>
                <span className="value mono">{prom.service}</span>
              </div>
            )}
            <div className="stat-row">
              <span className="label">kube-state-metrics</span>
              <StatusBadge ok={!!metricsStack?.kube_state_metrics} label={metricsStack?.kube_state_metrics ? 'Running' : 'Not Found'} />
            </div>
            {(metricsStack?.victoria_metrics || prom?.detected) && (
              <div style={{ marginTop: 8 }}>
                <button className="btn btn-sm btn-primary" onClick={() => window.dispatchEvent(new CustomEvent('navigate', { detail: 'prod-metrics' }))}>
                  View Metrics →
                </button>
              </div>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">🔐</span>
            <h3>Cert-Manager</h3>
          </div>
          <div className="card-body">
            <div className="stat-row">
              <span className="label">Status</span>
              <StatusBadge ok={!!cluster.cert_manager} label={cluster.cert_manager ? 'Installed' : 'Not Found'} />
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <span className="card-icon">◎</span>
            <h3>Ingress Controller</h3>
          </div>
          <div className="card-body">
            <div className="stat-row">
              <span className="label">Traefik</span>
              <StatusBadge ok={!!cluster.traefik} label={cluster.traefik ? 'Running' : 'Not Found'} />
            </div>
          </div>
        </div>
      </div>

      {/* Node resource usage */}
      {nodeMetrics.length > 0 && (
        <div className="card" style={{ marginBottom: 20 }}>
          <div className="card-header">
            <span className="card-icon">⬡</span>
            <h3>Node Resources</h3>
          </div>
          <div className="card-body">
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr><th>Node</th><th>CPU</th><th>CPU %</th><th>Memory</th><th>Mem %</th></tr>
                </thead>
                <tbody>
                  {nodeMetrics.map(m => (
                    <tr key={m.name}>
                      <td className="mono">{m.name}</td>
                      <td>{m.cpu_cores}</td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <div className="prod-mini-bar"><div className="prod-mini-fill" style={{ width: m.cpu_pct, background: parsePct(m.cpu_pct) > 80 ? 'var(--red)' : 'var(--accent)' }} /></div>
                          <span>{m.cpu_pct}</span>
                        </div>
                      </td>
                      <td>{m.mem_bytes}</td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <div className="prod-mini-bar"><div className="prod-mini-fill" style={{ width: m.mem_pct, background: parsePct(m.mem_pct) > 85 ? 'var(--red)' : 'var(--green)' }} /></div>
                          <span>{m.mem_pct}</span>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* Recent deployments */}
      {deployments && deployments.items.length > 0 && (
        <div className="card">
          <div className="card-header">
            <span className="card-icon">□</span>
            <h3>Deployments</h3>
            <span className="text-dim" style={{ marginLeft: 'auto', fontSize: 12 }}>
              {readyDeps}/{totalDeps} healthy
            </span>
          </div>
          <div className="card-body">
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr><th>Name</th><th>Namespace</th><th>Ready</th><th>Image</th><th>Age</th></tr>
                </thead>
                <tbody>
                  {deployments.items.slice(0, 10).map(d => {
                    const ready = (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1);
                    const image = d.spec?.template?.spec?.containers?.[0]?.image ?? '—';
                    return (
                      <tr key={`${d.metadata.namespace}/${d.metadata.name}`}>
                        <td className="mono" style={{ fontWeight: 550 }}>{d.metadata.name}</td>
                        <td><span className="tag">{d.metadata.namespace || 'default'}</span></td>
                        <td>
                          <StatusBadge ok={ready} label={`${d.status?.readyReplicas ?? 0}/${d.spec?.replicas ?? 1}`} warn={!ready && (d.status?.readyReplicas ?? 0) > 0} />
                        </td>
                        <td className="mono truncate" title={image}>{image.split('/').pop()}</td>
                        <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {deployments.items.length > 10 && (
              <p className="text-dim" style={{ fontSize: 12, padding: '8px 0 0' }}>
                Showing 10 of {deployments.items.length} — see Workloads page for all
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

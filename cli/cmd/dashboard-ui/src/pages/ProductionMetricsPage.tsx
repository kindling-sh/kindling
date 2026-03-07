import { useState, useEffect, useCallback } from 'react';
import { useApi, promQuery, promQueryRange, fetchProdNodeMetrics, fetchProdPodMetrics, fetchMetricsStatus, streamMetricsInstall, uninstallMetricsStack } from '../api';
import type { PrometheusStatus, NodeMetric, PodMetric, MetricsStackStatus } from '../types';

// ── SVG Sparkline Chart ─────────────────────────────────────────

function Sparkline({ data, width = 280, height = 60, color = 'var(--accent)', label }: {
  data: [number, string][];
  width?: number;
  height?: number;
  color?: string;
  label?: string;
}) {
  if (!data || data.length < 2) return <div className="prod-spark-empty">No data</div>;

  const values = data.map(d => parseFloat(d[1]) || 0);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;

  const points = values.map((v, i) => {
    const x = (i / (values.length - 1)) * width;
    const y = height - ((v - min) / range) * (height - 8) - 4;
    return `${x},${y}`;
  }).join(' ');

  const areaPoints = `0,${height} ${points} ${width},${height}`;
  const latest = values[values.length - 1];

  return (
    <div className="prod-spark">
      {label && <div className="prod-spark-label">{label}</div>}
      <svg width={width} height={height} className="prod-spark-svg">
        <defs>
          <linearGradient id={`grad-${label}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.25" />
            <stop offset="100%" stopColor={color} stopOpacity="0.02" />
          </linearGradient>
        </defs>
        <polygon points={areaPoints} fill={`url(#grad-${label})`} />
        <polyline points={points} fill="none" stroke={color} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        <circle cx={width} cy={height - ((latest - min) / range) * (height - 8) - 4} r="3" fill={color} />
      </svg>
      <div className="prod-spark-value" style={{ color }}>{latest.toFixed(2)}</div>
    </div>
  );
}

// ── Preset Queries ──────────────────────────────────────────────
// These match the metrics our VictoriaMetrics scrape config collects:
//   • kube_* from kube-state-metrics (always available)
//   • container_* from kubelet cAdvisor (available when RBAC is configured)
// Users don't need to know PromQL — these are pre-built dashboards.

interface MetricPreset {
  label: string;
  query: string;
  color: string;
  format: string;
  category: 'health' | 'workloads' | 'resources';
  description: string;
}

const PRESET_QUERIES: MetricPreset[] = [
  // Cluster health (kube-state-metrics)
  { label: 'Nodes Ready', query: 'sum(kube_node_status_condition{condition="Ready",status="true"})', color: 'var(--green)', format: 'num', category: 'health', description: 'Nodes in Ready state' },
  { label: 'Pod Restarts', query: 'sum(increase(kube_pod_container_status_restarts_total[1h]))', color: 'var(--red)', format: 'num', category: 'health', description: 'Container restarts in the last hour' },
  { label: 'Pods Running', query: 'sum(kube_pod_status_phase{phase="Running"})', color: 'var(--accent)', format: 'num', category: 'health', description: 'Pods currently in Running phase' },
  { label: 'Pods Pending', query: 'sum(kube_pod_status_phase{phase="Pending"}) or vector(0)', color: 'var(--orange)', format: 'num', category: 'health', description: 'Pods stuck in Pending state' },

  // Workload status (kube-state-metrics)
  { label: 'Deployment Replicas', query: 'sum(kube_deployment_status_replicas)', color: 'var(--cyan)', format: 'num', category: 'workloads', description: 'Total running deployment replicas' },
  { label: 'Desired Replicas', query: 'sum(kube_deployment_spec_replicas)', color: 'var(--text-secondary)', format: 'num', category: 'workloads', description: 'Total desired deployment replicas' },

  // Resource usage (cAdvisor via kubelet)
  { label: 'CPU Usage', query: 'sum(rate(container_cpu_usage_seconds_total{container!=""}[5m]))', color: 'var(--accent)', format: 'cores', category: 'resources', description: 'Total CPU usage across all containers' },
  { label: 'Memory Usage', query: 'sum(container_memory_working_set_bytes{container!=""}) / 1024 / 1024 / 1024', color: 'var(--purple)', format: 'gb', category: 'resources', description: 'Total working set memory across all containers' },
  { label: 'Network In', query: 'sum(rate(container_network_receive_bytes_total[5m]))', color: 'var(--cyan)', format: 'bytes', category: 'resources', description: 'Network receive rate across all containers' },
  { label: 'Network Out', query: 'sum(rate(container_network_transmit_bytes_total[5m]))', color: 'var(--orange)', format: 'bytes', category: 'resources', description: 'Network transmit rate across all containers' },
];

function formatValue(v: number, fmt: string): string {
  if (fmt === 'pct') return (v * 100).toFixed(1) + '%';
  if (fmt === 'cores') return v.toFixed(2) + ' cores';
  if (fmt === 'gb') return v.toFixed(2) + ' GB';
  if (fmt === 'bytes') {
    if (v > 1e9) return (v / 1e9).toFixed(1) + ' GB/s';
    if (v > 1e6) return (v / 1e6).toFixed(1) + ' MB/s';
    if (v > 1e3) return (v / 1e3).toFixed(1) + ' KB/s';
    return v.toFixed(0) + ' B/s';
  }
  if (fmt === 'num' && v >= 1000) return (v / 1000).toFixed(1) + 'k';
  return v % 1 === 0 ? v.toFixed(0) : v.toFixed(2);
}

export function ProductionMetricsPage() {
  const { data: promStatus } = useApi<PrometheusStatus>('/api/prod/prometheus/status', 15000);

  const [rangeData, setRangeData] = useState<Record<string, [number, string][]>>({});
  const [instantData, setInstantData] = useState<Record<string, number>>({});
  const [customQuery, setCustomQuery] = useState('');
  const [customResult, setCustomResult] = useState<string>('');
  const [customRunning, setCustomRunning] = useState(false);

  // kubectl metrics fallback
  const [nodeMetrics, setNodeMetrics] = useState<NodeMetric[]>([]);
  const [podMetrics, setPodMetrics] = useState<PodMetric[]>([]);
  const [metricNS, setMetricNS] = useState('');

  // Metrics stack management
  const [stackStatus, setStackStatus] = useState<MetricsStackStatus | null>(null);
  const [installLogs, setInstallLogs] = useState<{ type: string; message: string }[]>([]);
  const [installing, setInstalling] = useState(false);
  const [showInstall, setShowInstall] = useState(false);
  const [installRetention, setInstallRetention] = useState('1d');
  const [installScrape, setInstallScrape] = useState('30s');

  useEffect(() => {
    fetchMetricsStatus().then(s => setStackStatus(s)).catch(() => {});
  }, []);

  const fetchCharts = useCallback(async () => {
    const now = Math.floor(Date.now() / 1000);
    const start = now - 3600; // 1 hour
    const step = '60';

    for (const preset of PRESET_QUERIES) {
      try {
        const res = await promQueryRange(preset.query, start.toString(), now.toString(), step);
        if (res.data?.result?.[0]?.values) {
          setRangeData(prev => ({ ...prev, [preset.label]: res.data!.result[0].values! }));
        }
        // Also get instant value
        const instant = await promQuery(preset.query);
        if (instant.data?.result?.[0]?.value) {
          setInstantData(prev => ({ ...prev, [preset.label]: parseFloat(instant.data!.result[0].value![1]) }));
        }
      } catch { /* not all queries will work */ }
    }
  }, []);

  useEffect(() => {
    if (promStatus?.detected) {
      fetchCharts();
      const id = setInterval(fetchCharts, 30000);
      return () => clearInterval(id);
    }
  }, [promStatus?.detected, fetchCharts]);

  // Fallback: kubectl top
  useEffect(() => {
    fetchProdNodeMetrics().then(r => setNodeMetrics(r.items || [])).catch(() => {});
    fetchProdPodMetrics(metricNS || undefined).then(r => setPodMetrics(r.items || [])).catch(() => {});
    const id = setInterval(() => {
      fetchProdNodeMetrics().then(r => setNodeMetrics(r.items || [])).catch(() => {});
      fetchProdPodMetrics(metricNS || undefined).then(r => setPodMetrics(r.items || [])).catch(() => {});
    }, 15000);
    return () => clearInterval(id);
  }, [metricNS]);

  async function runCustomQuery() {
    if (!customQuery) return;
    setCustomRunning(true);
    try {
      const res = await promQuery(customQuery);
      setCustomResult(JSON.stringify(res, null, 2));
    } catch (e) {
      setCustomResult(`Error: ${e}`);
    }
    setCustomRunning(false);
  }

  function doInstall() {
    setInstalling(true);
    setInstallLogs([]);
    streamMetricsInstall(
      { retention: installRetention, scrape: installScrape },
      (msg) => {
        setInstallLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setInstalling(false);
          fetchMetricsStatus().then(s => setStackStatus(s)).catch(() => {});
        }
      },
    );
  }

  async function doUninstall() {
    await uninstallMetricsStack();
    setStackStatus({ victoria_metrics: false, kube_state_metrics: false, vm_version: '' });
    setShowInstall(false);
  }

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Metrics</h1>
          <p className="page-subtitle">
            {promStatus?.detected
              ? <>VictoriaMetrics: <span className="mono">{promStatus.service}</span> in <span className="tag">{promStatus.namespace}</span>{stackStatus?.vm_version && <span className="tag" style={{ marginLeft: 4 }}>{stackStatus.vm_version}</span>}</>
              : 'VictoriaMetrics not detected — showing kubectl top metrics'}
          </p>
        </div>
        <div className="page-actions">
          {stackStatus?.victoria_metrics ? (
            <button className="btn btn-danger-outline" onClick={doUninstall}>Uninstall Metrics</button>
          ) : (
            <button className="btn btn-primary" onClick={() => setShowInstall(true)}>Install VictoriaMetrics</button>
          )}
        </div>
      </div>

      {/* Metrics install panel */}
      {showInstall && !stackStatus?.victoria_metrics && (
        <div className="card" style={{ marginBottom: 20 }}>
          <div className="card-header">
            <span className="card-icon">◇</span>
            <h3>Install VictoriaMetrics Stack</h3>
          </div>
          <div className="card-body">
            {!installing && installLogs.length === 0 && (
              <div style={{ display: 'flex', gap: 16, alignItems: 'flex-end' }}>
                <div className="form-group" style={{ flex: 1 }}>
                  <label className="form-label">Retention</label>
                  <input className="form-input" value={installRetention} onChange={e => setInstallRetention(e.target.value)} placeholder="1d" />
                  <span className="form-hint">e.g. 1d, 7d, 30d (minimum 1d)</span>
                </div>
                <div className="form-group" style={{ flex: 1 }}>
                  <label className="form-label">Scrape Interval</label>
                  <input className="form-input" value={installScrape} onChange={e => setInstallScrape(e.target.value)} placeholder="30s" />
                  <span className="form-hint">e.g. 15s, 30s, 60s</span>
                </div>
                <button className="btn btn-primary" onClick={doInstall} style={{ marginBottom: 4 }}>Install</button>
                <button className="btn btn-secondary" onClick={() => setShowInstall(false)} style={{ marginBottom: 4 }}>Cancel</button>
              </div>
            )}
            {installLogs.length > 0 && (
              <div className="deploy-log" style={{ maxHeight: 200 }}>
                {installLogs.map((log, i) => (
                  <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                    <span className="deploy-log-icon">
                      {log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}
                    </span>
                    <span>{log.message}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {/* VictoriaMetrics dashboards — grouped by category */}
      {promStatus?.detected && (
        <>
          {/* Cluster Health */}
          {(() => {
            const health = PRESET_QUERIES.filter(p => p.category === 'health');
            const hasData = health.some(p => rangeData[p.label] || instantData[p.label] !== undefined);
            if (!hasData && Object.keys(rangeData).length > 0) return null;
            return (
              <div style={{ marginBottom: 20 }}>
                <h3 className="prod-section-title">Cluster Health</h3>
                <div className="prod-chart-grid">
                  {health.map(preset => {
                    const data = rangeData[preset.label];
                    const current = instantData[preset.label];
                    return (
                      <div key={preset.label} className="prod-chart-card" title={preset.description}>
                        <div className="prod-chart-header">
                          <span className="prod-chart-title">{preset.label}</span>
                          {current !== undefined && (
                            <span className="prod-chart-current" style={{ color: preset.color }}>
                              {formatValue(current, preset.format)}
                            </span>
                          )}
                        </div>
                        <Sparkline data={data || []} color={preset.color} label={preset.label} width={300} height={64} />
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })()}

          {/* Workload Status */}
          {(() => {
            const workloads = PRESET_QUERIES.filter(p => p.category === 'workloads');
            const hasData = workloads.some(p => rangeData[p.label] || instantData[p.label] !== undefined);
            if (!hasData && Object.keys(rangeData).length > 0) return null;
            return (
              <div style={{ marginBottom: 20 }}>
                <h3 className="prod-section-title">Workload Status</h3>
                <div className="prod-chart-grid">
                  {workloads.map(preset => {
                    const data = rangeData[preset.label];
                    const current = instantData[preset.label];
                    return (
                      <div key={preset.label} className="prod-chart-card" title={preset.description}>
                        <div className="prod-chart-header">
                          <span className="prod-chart-title">{preset.label}</span>
                          {current !== undefined && (
                            <span className="prod-chart-current" style={{ color: preset.color }}>
                              {formatValue(current, preset.format)}
                            </span>
                          )}
                        </div>
                        <Sparkline data={data || []} color={preset.color} label={preset.label} width={300} height={64} />
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })()}

          {/* Resource Usage */}
          {(() => {
            const resources = PRESET_QUERIES.filter(p => p.category === 'resources');
            const hasData = resources.some(p => rangeData[p.label] || instantData[p.label] !== undefined);
            if (!hasData && Object.keys(rangeData).length > 0) return null;
            return (
              <div style={{ marginBottom: 20 }}>
                <h3 className="prod-section-title">Resource Usage</h3>
                <p className="text-dim" style={{ fontSize: 12, marginBottom: 8 }}>Container-level metrics from cAdvisor</p>
                <div className="prod-chart-grid">
                  {resources.map(preset => {
                    const data = rangeData[preset.label];
                    const current = instantData[preset.label];
                    return (
                      <div key={preset.label} className="prod-chart-card" title={preset.description}>
                        <div className="prod-chart-header">
                          <span className="prod-chart-title">{preset.label}</span>
                          {current !== undefined && (
                            <span className="prod-chart-current" style={{ color: preset.color }}>
                              {formatValue(current, preset.format)}
                            </span>
                          )}
                        </div>
                        <Sparkline data={data || []} color={preset.color} label={preset.label} width={300} height={64} />
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })()}

          {/* Custom Query (advanced — collapsed by default) */}
          <details className="card" style={{ marginTop: 20 }}>
            <summary className="card-header" style={{ cursor: 'pointer', userSelect: 'none' }}>
              <span className="card-icon">⌘</span>
              <h3>Custom Query</h3>
              <span className="text-dim" style={{ marginLeft: 8, fontSize: 11 }}>Advanced — run any PromQL query against VictoriaMetrics</span>
            </summary>
            <div className="card-body">
              <div style={{ display: 'flex', gap: 8 }}>
                <input className="form-input" style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: 13 }}
                  placeholder='e.g. sum(kube_pod_status_phase{phase="Running"}) by (namespace)' value={customQuery} onChange={e => setCustomQuery(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') runCustomQuery(); }}
                />
                <button className="btn btn-primary" disabled={customRunning || !customQuery} onClick={runCustomQuery}>
                  {customRunning ? '…' : 'Query'}
                </button>
              </div>
              {customResult && (
                <pre className="log-output" style={{ marginTop: 12, maxHeight: 300, overflow: 'auto', fontSize: 12 }}>{customResult}</pre>
              )}
            </div>
          </details>
        </>
      )}

      {/* kubectl top fallback / supplemental */}
      <div className="card" style={{ marginTop: 20 }}>
        <div className="card-header">
          <span className="card-icon">⬡</span>
          <h3>Node Metrics</h3>
          <span className="text-dim" style={{ marginLeft: 8, fontSize: 11 }}>kubectl top nodes</span>
        </div>
        <div className="card-body">
          {nodeMetrics.length === 0 ? (
            <p className="text-dim" style={{ fontSize: 13 }}>
              Metrics server not available. Install metrics-server for resource usage data.
            </p>
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead><tr><th>Node</th><th>CPU</th><th>CPU %</th><th>Memory</th><th>Mem %</th></tr></thead>
                <tbody>
                  {nodeMetrics.map(m => (
                    <tr key={m.name}>
                      <td className="mono">{m.name}</td>
                      <td>{m.cpu_cores}</td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <div className="prod-mini-bar"><div className="prod-mini-fill" style={{ width: m.cpu_pct, background: 'var(--accent)' }} /></div>
                          <span>{m.cpu_pct}</span>
                        </div>
                      </td>
                      <td>{m.mem_bytes}</td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <div className="prod-mini-bar"><div className="prod-mini-fill" style={{ width: m.mem_pct, background: 'var(--green)' }} /></div>
                          <span>{m.mem_pct}</span>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      <div className="card" style={{ marginTop: 16 }}>
        <div className="card-header">
          <span className="card-icon">○</span>
          <h3>Pod Metrics</h3>
          <span className="text-dim" style={{ marginLeft: 8, fontSize: 11 }}>kubectl top pods</span>
          <div style={{ marginLeft: 'auto' }}>
            <input className="form-input" style={{ width: 160, fontSize: 12 }} placeholder="Filter namespace…"
              value={metricNS} onChange={e => setMetricNS(e.target.value)} />
          </div>
        </div>
        <div className="card-body">
          {podMetrics.length === 0 ? (
            <p className="text-dim" style={{ fontSize: 13 }}>No pod metrics available.</p>
          ) : (
            <div className="table-wrap" style={{ maxHeight: 400, overflow: 'auto' }}>
              <table className="data-table">
                <thead><tr><th>Pod</th><th>Namespace</th><th>CPU</th><th>Memory</th></tr></thead>
                <tbody>
                  {podMetrics.slice(0, 50).map(m => (
                    <tr key={`${m.namespace}/${m.name}`}>
                      <td className="mono" style={{ fontSize: 12 }}>{m.name}</td>
                      <td><span className="tag">{m.namespace}</span></td>
                      <td>{m.cpu}</td>
                      <td>{m.memory}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {podMetrics.length > 50 && <p className="text-dim" style={{ padding: '8px 0 0', fontSize: 12 }}>Showing 50 of {podMetrics.length}</p>}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

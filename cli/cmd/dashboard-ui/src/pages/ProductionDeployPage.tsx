import { useState, useEffect, useRef } from 'react';
import { fetchSnapshotStatus, streamSnapshotDeploy } from '../api';
import type { SnapshotStatus, SnapshotService } from '../types';
import { StatusBadge, EmptyState } from './shared';

type DeployStep = 'configure' | 'deploying' | 'done';

export function ProductionDeployPage() {
  const [status, setStatus] = useState<SnapshotStatus | null>(null);
  const [loading, setLoading] = useState(true);

  // Form state
  const [registry, setRegistry] = useState('');
  const [tag, setTag] = useState('latest');
  const [format, setFormat] = useState<'helm' | 'kustomize'>('helm');
  const [namespace, setNamespace] = useState('default');
  const [selectedIngress, setSelectedIngress] = useState<Set<string>>(new Set());

  // Deploy state
  const [step, setStep] = useState<DeployStep>('configure');
  const [logs, setLogs] = useState<{ type: string; message: string }[]>([]);
  const logRef = useRef<HTMLDivElement>(null);
  const cancelRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    fetchSnapshotStatus()
      .then(s => {
        setStatus(s);
        // Pre-select all services with ingress enabled
        const ing = new Set<string>();
        for (const svc of s.services) {
          if (svc.ingress?.enabled) ing.add(svc.name);
        }
        setSelectedIngress(ing);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  // Auto-scroll log
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [logs]);

  function toggleIngress(name: string) {
    setSelectedIngress(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  function startDeploy() {
    if (!registry) return;
    setStep('deploying');
    setLogs([]);

    const cancel = streamSnapshotDeploy(
      { registry, tag, format, namespace, ingress: Array.from(selectedIngress) },
      (msg) => {
        setLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setStep('done');
        }
      },
    );
    cancelRef.current = cancel;
  }

  function reset() {
    if (cancelRef.current) cancelRef.current();
    setStep('configure');
    setLogs([]);
    fetchSnapshotStatus().then(s => setStatus(s)).catch(() => {});
  }

  if (loading) return <div className="loading">Reading cluster state…</div>;

  if (!status?.connected) {
    return (
      <div className="page">
        <div className="page-header"><div className="page-header-left">
          <h1>Deploy to Production</h1>
          <p className="page-subtitle">Not connected — start the dashboard with <code>--prod-context</code></p>
        </div></div>
        <div className="prod-disconnected">
          <div className="prod-disconnected-icon">⚠</div>
          <h2>No Production Context</h2>
          <p>Launch the dashboard with a production kubeconfig context to enable deployment.</p>
          <pre className="prod-code-block">kindling dashboard --prod-context &lt;your-context&gt;</pre>
        </div>
      </div>
    );
  }

  const services: SnapshotService[] = status?.services || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Deploy to Production</h1>
          <p className="page-subtitle">
            Snapshot dev environment and deploy to <span className="mono">{status?.context}</span>
          </p>
        </div>
        <div className="page-actions">
          <div className="deploy-tools">
            <StatusBadge ok={!!status?.helm} label={status?.helm ? 'Helm ✓' : 'Helm ✗'} />
            <StatusBadge ok={!!status?.crane || !!status?.docker} label={status?.crane ? 'Crane ✓' : status?.docker ? 'Docker ✓' : 'No push tool'} />
          </div>
        </div>
      </div>

      {step === 'configure' && (
        <>
          {/* Services detected */}
          {services.length === 0 ? (
            <EmptyState icon="□" message="No DevStagingEnvironments found in the local Kind cluster. Deploy services with kindling first." />
          ) : (
            <>
              <div className="card" style={{ marginBottom: 16 }}>
                <div className="card-header">
                  <span className="card-icon">□</span>
                  <h3>Services ({services.length})</h3>
                </div>
                <div className="card-body" style={{ padding: 0 }}>
                  <div className="table-wrap">
                    <table className="data-table">
                      <thead>
                        <tr><th>Service</th><th>Image</th><th>Port</th><th>Replicas</th><th>Dependencies</th><th>Ingress</th></tr>
                      </thead>
                      <tbody>
                        {services.map(svc => (
                          <tr key={svc.name}>
                            <td className="mono" style={{ fontWeight: 550 }}>{svc.name}</td>
                            <td className="mono" style={{ fontSize: 12 }}>{svc.image}</td>
                            <td>{svc.port}</td>
                            <td>{svc.replicas}</td>
                            <td>
                              {svc.deps?.length ? svc.deps.map(d => (
                                <span key={d} className="tag" style={{ marginRight: 4 }}>{d}</span>
                              )) : <span className="text-dim">—</span>}
                            </td>
                            <td>
                              <label className="deploy-ingress-toggle">
                                <input
                                  type="checkbox"
                                  checked={selectedIngress.has(svc.name)}
                                  onChange={() => toggleIngress(svc.name)}
                                />
                                <span className="mono" style={{ fontSize: 12, marginLeft: 4 }}>
                                  {svc.ingress?.enabled ? (svc.ingress.host || 'enabled in dev') : 'expose'}
                                </span>
                              </label>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              </div>

              {/* Deploy configuration */}
              <div className="card-grid card-grid-2" style={{ marginBottom: 16 }}>
                <div className="card">
                  <div className="card-header">
                    <span className="card-icon">◇</span>
                    <h3>Registry & Image</h3>
                  </div>
                  <div className="card-body">
                    <div className="form-group">
                      <label className="form-label">Container Registry *</label>
                      <input
                        className="form-input"
                        placeholder="ghcr.io/org or docker.io/user"
                        value={registry}
                        onChange={e => setRegistry(e.target.value)}
                      />
                      <span className="form-hint">Images will be pushed as registry/service:tag</span>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Tag</label>
                      <input
                        className="form-input"
                        placeholder="latest"
                        value={tag}
                        onChange={e => setTag(e.target.value)}
                      />
                    </div>
                  </div>
                </div>

                <div className="card">
                  <div className="card-header">
                    <span className="card-icon">⬡</span>
                    <h3>Deploy Target</h3>
                  </div>
                  <div className="card-body">
                    <div className="form-group">
                      <label className="form-label">Format</label>
                      <div className="deploy-format-toggle">
                        <button
                          className={`deploy-format-btn ${format === 'helm' ? 'active' : ''}`}
                          onClick={() => setFormat('helm')}
                        >
                          Helm
                        </button>
                        <button
                          className={`deploy-format-btn ${format === 'kustomize' ? 'active' : ''}`}
                          onClick={() => setFormat('kustomize')}
                        >
                          Kustomize
                        </button>
                      </div>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Namespace</label>
                      <input
                        className="form-input"
                        placeholder="default"
                        value={namespace}
                        onChange={e => setNamespace(e.target.value)}
                      />
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Target Context</label>
                      <input
                        className="form-input"
                        value={status?.context || ''}
                        disabled
                      />
                      <span className="form-hint">Set via --prod-context flag</span>
                    </div>
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                <button
                  className="btn btn-primary"
                  disabled={!registry || services.length === 0}
                  onClick={startDeploy}
                >
                  Deploy to Production
                </button>
              </div>
            </>
          )}
        </>
      )}

      {/* Deploy progress */}
      {(step === 'deploying' || step === 'done') && (
        <div className="card">
          <div className="card-header">
            <span className="card-icon">{step === 'deploying' ? '⏳' : logs.some(l => l.type === 'error') ? '✗' : '✓'}</span>
            <h3>{step === 'deploying' ? 'Deploying…' : logs.some(l => l.type === 'error') ? 'Deploy Failed' : 'Deploy Complete'}</h3>
          </div>
          <div className="card-body" style={{ padding: 0 }}>
            <div ref={logRef} className="deploy-log">
              {logs.map((log, i) => (
                <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                  <span className="deploy-log-icon">
                    {log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}
                  </span>
                  <span>{log.message}</span>
                </div>
              ))}
              {step === 'deploying' && (
                <div className="deploy-log-line deploy-log-step">
                  <span className="deploy-log-icon deploy-log-spinner">◌</span>
                  <span className="text-dim">Working…</span>
                </div>
              )}
            </div>
          </div>
          {step === 'done' && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              <button className="btn btn-secondary" onClick={reset}>
                ← Configure Another Deploy
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

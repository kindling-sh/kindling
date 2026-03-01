import { useState, useEffect, useRef, useCallback } from 'react';
import { useApi, apiPost, apiDelete, fetchRuntimeInfo, fetchSyncStatus, fetchServiceDirs, fetchTopologyLogs } from '../api';
import type { K8sList, DSE, RuntimeInfo, SyncStatus, ServiceDir, TopologyLogEntry } from '../types';
import { DEP_META } from '../types';
import { DEP_ICONS } from '../icons';
import { StatusBadge, ConditionsTable, EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast, ResultOutput } from './actions';
import type { ActionResult } from '../api';

export function DSEPage() {
  const { data, loading, refresh } = useApi<K8sList<DSE>>('/api/dses');
  const { toast } = useToast();
  const [showDeploy, setShowDeploy] = useState(false);
  const [yaml, setYaml] = useState('');
  const [deploying, setDeploying] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleDeploy() {
    setDeploying(true);
    const result = await apiPost('/api/deploy', { yaml });
    setDeploying(false);
    if (result.ok) {
      toast('Environment deployed', 'success');
      setShowDeploy(false);
      setYaml('');
      refresh();
    } else {
      toast(result.error || 'Deploy failed', 'error');
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const result = await apiDelete(`/api/dses/${deleteTarget.ns}/${deleteTarget.name}`);
    if (result.ok) {
      toast(`Deleted ${deleteTarget.name}`, 'success');
      refresh();
    } else {
      toast(result.error || 'Delete failed', 'error');
    }
    setDeleteTarget(null);
  }

  if (loading) return <div className="loading">Loading environments…</div>;

  const dses = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Dev Staging Environments</h1>
          <p className="page-subtitle">Managed by the kindling operator</p>
        </div>
        <div className="page-actions">
          <ActionButton icon="+" label="Deploy" onClick={() => setShowDeploy(true)} primary />
        </div>
      </div>

      {showDeploy && (
        <ActionModal
          title="Deploy Environment"
          submitLabel="Deploy"
          loading={deploying}
          onSubmit={handleDeploy}
          onClose={() => setShowDeploy(false)}
        >
          <label className="form-label">YAML Manifest</label>
          <textarea
            className="form-textarea"
            rows={14}
            placeholder="Paste your DevStagingEnvironment YAML here..."
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
          />
        </ActionModal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete Environment"
          message={`Delete '${deleteTarget.name}' and all its resources?`}
          confirmLabel="Delete"
          danger
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {dses.length === 0 ? (
        <EmptyState icon="◆" message="No DevStagingEnvironments found. Deploy one with: kindling deploy -f <file.yaml>" />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {dses.map((dse) => (
            <DSECard key={dse.metadata.name} dse={dse} onDelete={(ns, name) => setDeleteTarget({ ns, name })} />
          ))}
        </div>
      )}
    </div>
  );
}

function DSECard({ dse, onDelete }: { dse: DSE; onDelete: (ns: string, name: string) => void }) {
  const s = dse.status;
  const { toast } = useToast();
  const allReady = s?.deploymentReady && s?.serviceReady &&
    (s?.ingressReady || !dse.spec.ingress?.enabled) &&
    (s?.dependenciesReady || !dse.spec.dependencies?.length);

  const ns = dse.metadata.namespace || 'default';
  const name = dse.metadata.name;

  // Runtime detection
  const [runtime, setRuntime] = useState<RuntimeInfo | null>(null);
  const [runtimeLoading, setRuntimeLoading] = useState(true);

  // Modal state
  const [showSync, setShowSync] = useState(false);
  const [showLoad, setShowLoad] = useState(false);
  const [showLogs, setShowLogs] = useState(false);

  // Sync status polling
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);

  useEffect(() => {
    fetchRuntimeInfo(ns, name)
      .then(setRuntime)
      .catch(() => setRuntime(null))
      .finally(() => setRuntimeLoading(false));
  }, [ns, name]);

  // Poll sync status when a sync might be running for this deployment
  useEffect(() => {
    const poll = () => {
      fetchSyncStatus().then(setSyncStatus).catch(() => {});
    };
    poll();
    const id = setInterval(poll, 3000);
    return () => clearInterval(id);
  }, []);

  const isSyncRunning = syncStatus?.running && syncStatus?.deployment === name && syncStatus?.namespace === ns;

  async function handleStopSync() {
    const result = await apiDelete('/api/sync');
    if (result.ok) {
      toast('Sync stopped', 'success');
      setSyncStatus(null);
    } else {
      toast(result.error || 'Failed to stop sync', 'error');
    }
  }

  return (
    <div className="card card-wide">
      <div className="card-header">
        <span className="card-icon">◆</span>
        <h3>{name}</h3>
        <StatusBadge ok={!!allReady} label={allReady ? 'Ready' : 'Not Ready'} />
        {!runtimeLoading && runtime && (
          <span className={`tag ${runtime.sync_supported ? 'tag-purple' : ''}`} style={{ marginLeft: 8 }}>
            {runtime.runtime !== 'unknown' ? runtime.runtime : runtime.language || 'unknown'}
          </span>
        )}
      </div>
      <div className="card-body">
        <div className="card-body-grid">
          <div>
            <h4>Deployment</h4>
            <div className="stat-row"><span className="label">Image</span><span className="value mono">{dse.spec.deployment.image}</span></div>
            <div className="stat-row"><span className="label">Port</span><span className="value">{dse.spec.deployment.port}</span></div>
            <div className="stat-row"><span className="label">Replicas</span><span className="value">{s?.availableReplicas ?? 0} / {dse.spec.deployment.replicas ?? 1}</span></div>
            <div className="stat-row"><span className="label">Health</span><span className="value mono">{dse.spec.deployment.healthCheck?.path || '—'}</span></div>
            <div className="stat-row">
              <span className="label">Status</span>
              <StatusBadge ok={!!s?.deploymentReady} label={s?.deploymentReady ? 'Ready' : 'Pending'} />
            </div>
          </div>

          <div>
            <h4>Service</h4>
            <div className="stat-row"><span className="label">Port</span><span className="value">{dse.spec.service.port}</span></div>
            <div className="stat-row"><span className="label">Type</span><span className="value">{dse.spec.service.type || 'ClusterIP'}</span></div>
            <div className="stat-row">
              <span className="label">Status</span>
              <StatusBadge ok={!!s?.serviceReady} label={s?.serviceReady ? 'Ready' : 'Pending'} />
            </div>
          </div>

          {dse.spec.ingress?.enabled && (
            <div>
              <h4>Ingress</h4>
              <div className="stat-row"><span className="label">Host</span><span className="value mono">{dse.spec.ingress.host || '—'}</span></div>
              <div className="stat-row"><span className="label">Path</span><span className="value mono">{dse.spec.ingress.path || '/'}</span></div>
              <div className="stat-row">
                <span className="label">Status</span>
                <StatusBadge ok={!!s?.ingressReady} label={s?.ingressReady ? 'Ready' : 'Pending'} />
              </div>
              {s?.externalURL && (
                <div className="stat-row">
                  <span className="label">URL</span>
                  <a href={s.externalURL} target="_blank" className="value link">{s.externalURL}</a>
                </div>
              )}
            </div>
          )}
        </div>

        {dse.spec.dependencies && dse.spec.dependencies.length > 0 && (
          <div className="dse-deps-section">
            <div className="dse-deps-header">
              <span className="dse-deps-label">Dependencies</span>
              <StatusBadge ok={!!s?.dependenciesReady} label={s?.dependenciesReady ? 'Ready' : 'Pending'} />
            </div>
            <div className="dse-deps-list">
              {dse.spec.dependencies.map((dep, i) => (
                <DepChip key={i} dep={dep} ready={!!s?.dependenciesReady} />
              ))}
            </div>
          </div>
        )}

        {isSyncRunning && (
          <div className="sync-status-bar">
            <span className="sync-pulse" />
            <span>Sync active — {syncStatus!.sync_count} {syncStatus!.sync_count === 1 ? 'sync' : 'syncs'} • watching for changes</span>
            <button className="btn btn-sm btn-danger" onClick={handleStopSync}>Stop</button>
          </div>
        )}

        {dse.spec.deployment.env && dse.spec.deployment.env.length > 0 && (
          <details style={{ marginTop: 16 }}>
            <summary>Environment Variables ({dse.spec.deployment.env.length})</summary>
            <table className="env-table" style={{ marginTop: 8 }}>
              <tbody>
                {dse.spec.deployment.env.map((e, i) => (
                  <tr key={i}><td>{e.name}</td><td>{e.value || '(from secret)'}</td></tr>
                ))}
              </tbody>
            </table>
          </details>
        )}

        {s?.conditions && (
          <details>
            <summary>Conditions ({s.conditions.length})</summary>
            <ConditionsTable conditions={s.conditions} />
          </details>
        )}
      </div>
      <div className="card-footer">
        <TimeAgo timestamp={dse.metadata.creationTimestamp} />
        <div className="card-footer-actions">
          <ActionButton
            icon={showLogs ? '✕' : '📋'}
            label={showLogs ? 'Hide Logs' : 'Logs'}
            onClick={() => setShowLogs(!showLogs)}
            small
          />
          {runtime?.sync_supported && !isSyncRunning && (
            <ActionButton icon="⚡" label="Sync" onClick={() => setShowSync(true)} small />
          )}
          {isSyncRunning && (
            <ActionButton icon="■" label="Stop Sync" onClick={handleStopSync} danger small />
          )}
          <ActionButton icon="📦" label="Load" onClick={() => setShowLoad(true)} small />
          <ActionButton icon="✕" label="Delete" onClick={() => onDelete(ns, name)} danger small />
        </div>
      </div>

      {showLogs && (
        <DSELogViewer nodeId={`svc-${name}`} />
      )}

      {showSync && (
        <SyncModal
          deployment={name}
          namespace={ns}
          runtime={runtime}
          onClose={() => setShowSync(false)}
          onStarted={() => {
            setShowSync(false);
            toast('Sync started — watching for changes', 'success');
          }}
        />
      )}

      {showLoad && (
        <LoadModal
          service={name}
          namespace={ns}
          onClose={() => setShowLoad(false)}
          onComplete={(result) => {
            setShowLoad(false);
            if (result.ok) {
              toast(`${name} rebuilt and deployed`, 'success');
            } else {
              toast(result.error || 'Load failed', 'error');
            }
          }}
        />
      )}
    </div>
  );
}

// ── Dependency Chip ─────────────────────────────────────────────

function DepChip({ dep, ready }: { dep: { type: string; version?: string; port?: number; envVarName?: string }; ready: boolean }) {
  const meta = DEP_META[dep.type as keyof typeof DEP_META];
  const color = meta?.color || 'var(--accent)';
  const envVar = dep.envVarName || meta?.envVar || '';
  const SvgIcon = DEP_ICONS[dep.type];
  return (
    <div className="dse-dep-chip">
      <span className="dse-dep-icon" style={{ background: color }}>
        {SvgIcon ? <SvgIcon /> : (meta?.icon || '◆')}
      </span>
      <div className="dse-dep-info">
        <div className="dse-dep-name">
          {meta?.label || dep.type}
          {dep.version && <span className="dse-dep-ver">{dep.version}</span>}
          <span className={`dse-dep-dot ${ready ? 'ready' : ''}`} />
        </div>
        {envVar && (
          <div className="dse-dep-env">
            <span className="dse-dep-env-arrow">→</span>
            <code>{envVar}</code>
            {dep.port ? <span className="dse-dep-port">:{dep.port}</span> : null}
          </div>
        )}
      </div>
    </div>
  );
}

// ── DSE Log Viewer ──────────────────────────────────────────────

function DSELogViewer({ nodeId }: { nodeId: string }) {
  const [logs, setLogs] = useState<TopologyLogEntry[]>([]);
  const [pods, setPods] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [autoScroll, setAutoScroll] = useState(true);
  const [filter, setFilter] = useState('');
  const termRef = useRef<HTMLDivElement>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadLogs = useCallback(async () => {
    try {
      const result = await fetchTopologyLogs(nodeId);
      setLogs(result.lines || []);
      setPods(result.pods || []);
    } catch { /* ignore */ }
    setLoading(false);
  }, [nodeId]);

  useEffect(() => {
    setLoading(true);
    setLogs([]);
    loadLogs();
    intervalRef.current = setInterval(loadLogs, 4000);
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, [loadLogs]);

  useEffect(() => {
    if (autoScroll && termRef.current) {
      termRef.current.scrollTop = termRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  const handleScroll = useCallback(() => {
    if (!termRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = termRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 40);
  }, []);

  const filteredLogs = filter
    ? logs.filter(l => l.line.toLowerCase().includes(filter.toLowerCase()) || l.pod.toLowerCase().includes(filter.toLowerCase()))
    : logs;

  const multiPod = pods.length > 1;
  const podColors = useRef(new Map<string, string>());
  const colorPalette = ['#60a5fa', '#34d399', '#fbbf24', '#f87171', '#a78bfa', '#fb923c', '#2dd4bf', '#e879f9'];
  pods.forEach((p, i) => {
    if (!podColors.current.has(p)) {
      podColors.current.set(p, colorPalette[i % colorPalette.length]);
    }
  });

  return (
    <div className="dse-log-viewer">
      <div className="topo-terminal-toolbar">
        <input
          className="topo-terminal-filter"
          placeholder="Filter logs…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
        <button
          className={`topo-terminal-btn ${autoScroll ? 'active' : ''}`}
          onClick={() => {
            setAutoScroll(true);
            if (termRef.current) termRef.current.scrollTop = termRef.current.scrollHeight;
          }}
          title="Auto-scroll"
        >
          ↓
        </button>
        <button className="topo-terminal-btn" onClick={loadLogs} title="Refresh">⟳</button>
      </div>
      <div className="topo-terminal-body" ref={termRef} onScroll={handleScroll}>
        {loading && <div className="topo-terminal-loading">Loading logs…</div>}
        {!loading && filteredLogs.length === 0 && (
          <div className="topo-terminal-empty">No logs available</div>
        )}
        {filteredLogs.map((entry, i) => (
          <div key={i} className="topo-terminal-line">
            {multiPod && (
              <span className="topo-terminal-pod" style={{ color: podColors.current.get(entry.pod) }}>
                {entry.pod.slice(0, 24)}
              </span>
            )}
            <span className="topo-terminal-text">{entry.line}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── Sync Modal ──────────────────────────────────────────────────

function SyncModal({
  deployment,
  namespace,
  runtime,
  onClose,
  onStarted,
}: {
  deployment: string;
  namespace: string;
  runtime: RuntimeInfo | null;
  onClose: () => void;
  onStarted: () => void;
}) {
  const { toast } = useToast();
  const [src, setSrc] = useState('');
  const [dest, setDest] = useState(runtime?.default_dest || '/app');
  const [container, setContainer] = useState(runtime?.container || '');
  const [restart, setRestart] = useState(true);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ActionResult | null>(null);

  // Try to auto-detect a matching service directory
  const [serviceDirs, setServiceDirs] = useState<ServiceDir[]>([]);
  useEffect(() => {
    fetchServiceDirs().then((dirs) => {
      setServiceDirs(dirs);
      // Auto-match by name
      const match = dirs.find(d => d.name === deployment || d.name.includes(deployment));
      if (match) {
        setSrc(match.path);
      }
    }).catch(() => {});
  }, [deployment]);

  async function handleSubmit() {
    if (!src.trim()) {
      toast('Source directory is required', 'error');
      return;
    }
    setLoading(true);
    const res = await apiPost('/api/sync', {
      deployment,
      namespace,
      src: src.trim(),
      dest: dest.trim(),
      container: container.trim() || undefined,
      restart,
    });
    setLoading(false);
    if (res.ok) {
      onStarted();
    } else {
      setResult(res);
    }
  }

  const strategyLabel = runtime?.strategy || 'Sync files to container and restart';

  return (
    <ActionModal
      title={`Sync — ${deployment}`}
      submitLabel="Start Sync"
      loading={loading}
      onSubmit={handleSubmit}
      onClose={onClose}
    >
      <div className="sync-modal-strategy">
        <span className="tag tag-purple">⚡ {runtime?.mode || 'auto'}</span>
        <span className="text-muted">{strategyLabel}</span>
      </div>

      <label className="form-label">Source Directory</label>
      {serviceDirs.length > 0 ? (
        <select
          className="form-input"
          value={src}
          onChange={(e) => setSrc(e.target.value)}
        >
          <option value="">Select a service directory…</option>
          {serviceDirs.map(d => (
            <option key={d.path} value={d.path}>
              {d.name} {d.language ? `(${d.language})` : ''} {d.has_dockerfile ? '🐳' : ''}
            </option>
          ))}
          <option value="__custom__">Custom path…</option>
        </select>
      ) : (
        <input
          className="form-input"
          value={src}
          onChange={(e) => setSrc(e.target.value)}
          placeholder={`./services/${deployment}`}
        />
      )}
      {src === '__custom__' && (
        <input
          className="form-input"
          style={{ marginTop: 6 }}
          value=""
          onChange={(e) => setSrc(e.target.value)}
          placeholder="/absolute/path/to/service"
          autoFocus
        />
      )}

      <div className="form-row">
        <div style={{ flex: 1 }}>
          <label className="form-label">Destination</label>
          <input
            className="form-input"
            value={dest}
            onChange={(e) => setDest(e.target.value)}
            placeholder="/app"
          />
        </div>
        <div style={{ flex: 1 }}>
          <label className="form-label">Container</label>
          <input
            className="form-input"
            value={container}
            onChange={(e) => setContainer(e.target.value)}
            placeholder={deployment}
          />
        </div>
      </div>

      <label className="form-checkbox" style={{ marginTop: 14 }}>
        <input type="checkbox" checked={restart} onChange={(e) => setRestart(e.target.checked)} />
        <span>Restart process after sync</span>
      </label>

      <ResultOutput result={result} />
    </ActionModal>
  );
}

// ── Load Modal ──────────────────────────────────────────────────

function LoadModal({
  service,
  namespace,
  onClose,
  onComplete,
}: {
  service: string;
  namespace: string;
  onClose: () => void;
  onComplete: (result: ActionResult) => void;
}) {
  const { toast } = useToast();
  const [context, setContext] = useState('');
  const [dockerfile, setDockerfile] = useState('');
  const [noDeploy, setNoDeploy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<ActionResult | null>(null);

  // Try to auto-detect service directory
  const [serviceDirs, setServiceDirs] = useState<ServiceDir[]>([]);
  const [warning, setWarning] = useState('');
  useEffect(() => {
    fetchServiceDirs().then((dirs) => {
      setServiceDirs(dirs);
      const match = dirs.find(d => d.name === service || d.name.includes(service));
      if (match) {
        setContext(match.context_path || match.path);
        if (match.dockerfile_path) setDockerfile(match.dockerfile_path);
        if (match.warning) setWarning(match.warning);
      }
    }).catch(() => {});
  }, [service]);

  async function handleSubmit() {
    if (!context.trim()) {
      toast('Build context is required', 'error');
      return;
    }
    setLoading(true);
    const res = await apiPost('/api/load', {
      service,
      context: context.trim(),
      dockerfile: dockerfile.trim() || undefined,
      namespace,
      no_deploy: noDeploy,
    });
    setLoading(false);
    setResult(res);
    if (res.ok) {
      // Keep modal open briefly to show success
      setTimeout(() => onComplete(res), 1500);
    }
  }

  return (
    <ActionModal
      title={`Load — ${service}`}
      submitLabel={loading ? 'Building…' : 'Build & Load'}
      loading={loading}
      onSubmit={handleSubmit}
      onClose={onClose}
    >
      <p style={{ marginTop: 0, marginBottom: 16 }}>
        Build a Docker image, load it into the cluster, and roll out the new version.
      </p>

      <label className="form-label">Build Context</label>
      {serviceDirs.length > 0 ? (
        <select
          className="form-input"
          value={serviceDirs.find(d => (d.context_path || d.path) === context)?.path || context}
          onChange={(e) => {
            const val = e.target.value;
            const match = serviceDirs.find(d => d.path === val);
            if (match) {
              setContext(match.context_path || match.path);
              setDockerfile(match.dockerfile_path || '');
              setWarning(match.warning || '');
            } else {
              setContext(val);
              setDockerfile('');
              setWarning('');
            }
          }}
        >
          <option value="">Select a service directory…</option>
          {serviceDirs.map(d => (
            <option key={d.path} value={d.path}>
              {d.name} {d.has_dockerfile ? '🐳' : '(no Dockerfile)'} {d.language ? `· ${d.language}` : ''}
            </option>
          ))}
          <option value="__custom__">Custom path…</option>
        </select>
      ) : (
        <input
          className="form-input"
          value={context}
          onChange={(e) => setContext(e.target.value)}
          placeholder={`./services/${service}`}
        />
      )}
      {context === '__custom__' && (
        <input
          className="form-input"
          style={{ marginTop: 6 }}
          value=""
          onChange={(e) => setContext(e.target.value)}
          placeholder="/absolute/path/to/service"
          autoFocus
        />
      )}

      {warning && (
        <div style={{
          marginTop: 8, padding: '8px 12px', borderRadius: 6,
          background: 'var(--color-warning-bg, #fff3cd)',
          color: 'var(--color-warning-text, #856404)',
          fontSize: '0.85em', lineHeight: 1.4,
          border: '1px solid var(--color-warning-border, #ffc107)',
        }}>
          ⚠️ {warning}
        </div>
      )}

      <label className="form-label">Dockerfile</label>
      <input
        className="form-input"
        value={dockerfile}
        onChange={(e) => setDockerfile(e.target.value)}
        placeholder="Dockerfile (default)"
      />

      <label className="form-checkbox" style={{ marginTop: 14 }}>
        <input type="checkbox" checked={noDeploy} onChange={(e) => setNoDeploy(e.target.checked)} />
        <span>Build only — don't deploy</span>
      </label>

      <ResultOutput result={result} />
    </ActionModal>
  );
}

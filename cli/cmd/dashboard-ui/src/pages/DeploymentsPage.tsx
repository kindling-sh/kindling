import { useState, useEffect } from 'react';
import { useApi, apiPost, fetchEnvVars, fetchLogs } from '../api';
import type { K8sList, K8sDeployment, K8sReplicaSet, K8sPod, K8sContainerSpec, K8sContainerStatus } from '../types';
import { StatusBadge, TimeAgo, EmptyState, ConditionsTable } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function DeploymentsPage() {
  const { data, loading, refresh } = useApi<K8sList<K8sDeployment>>('/api/deployments');
  const { toast } = useToast();
  const [selected, setSelected] = useState<K8sDeployment | null>(null);

  // Scale
  const [scaleTarget, setScaleTarget] = useState<{ ns: string; name: string; current: number } | null>(null);
  const [scaleCount, setScaleCount] = useState(1);

  // Restart
  const [restartTarget, setRestartTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleRestart() {
    if (!restartTarget) return;
    setRestartTarget(null);
    const result = await apiPost(`/api/restart/${restartTarget.ns}/${restartTarget.name}`);
    if (result.ok) {
      toast(`Restarted ${restartTarget.name}`, 'success');
      refresh();
    } else {
      toast(result.error || 'Restart failed', 'error');
    }
  }

  async function handleScale() {
    if (!scaleTarget) return;
    const result = await apiPost(`/api/scale/${scaleTarget.ns}/${scaleTarget.name}`, { replicas: scaleCount });
    if (result.ok) {
      toast(`Scaled ${scaleTarget.name} to ${scaleCount}`, 'success');
      setScaleTarget(null);
      refresh();
    } else {
      toast(result.error || 'Scale failed', 'error');
    }
  }

  if (loading) return <div className="loading">Loading deployments…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Deployments</h1>
          <p className="page-subtitle">Click a deployment to explore its ReplicaSets, Pods, and Containers</p>
        </div>
      </div>

      {restartTarget && (
        <ConfirmDialog
          title="Restart Deployment"
          message={`Rolling restart ${restartTarget.name}?`}
          confirmLabel="Restart"
          onConfirm={handleRestart}
          onCancel={() => setRestartTarget(null)}
        />
      )}

      {scaleTarget && (
        <ActionModal
          title={`Scale ${scaleTarget.name}`}
          submitLabel="Scale"
          onSubmit={handleScale}
          onClose={() => setScaleTarget(null)}
        >
          <label className="form-label">Replicas (current: {scaleTarget.current})</label>
          <input className="form-input" type="number" min={0} max={20} value={scaleCount}
            onChange={(e) => setScaleCount(Number(e.target.value))} />
        </ActionModal>
      )}

      {items.length === 0 ? (
        <EmptyState icon="□" message="No deployments found in the cluster." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Ready</th>
                <th>Image</th>
                <th>Strategy</th>
                <th>Status</th>
                <th>Age</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {items.map((d) => {
                const ready = (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1);
                const image = d.spec?.template?.spec?.containers?.[0]?.image ?? '—';
                const ns = d.metadata.namespace || 'default';
                return (
                  <tr
                    key={`${ns}/${d.metadata.name}`}
                    className="clickable-row"
                    onClick={() => setSelected(d)}
                  >
                    <td className="mono" style={{ fontWeight: 550 }}>{d.metadata.name}</td>
                    <td><span className="tag">{ns}</span></td>
                    <td>{d.status?.readyReplicas ?? 0} / {d.spec?.replicas ?? 1}</td>
                    <td className="mono truncate" title={image}>{image.split('/').pop()}</td>
                    <td><span className="tag tag-purple">{d.spec?.strategy?.type || 'RollingUpdate'}</span></td>
                    <td><StatusBadge ok={ready} label={ready ? 'Available' : 'Progressing'} /></td>
                    <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                    <td className="action-cell" onClick={(e) => e.stopPropagation()}>
                      <ActionButton icon="↻" label="" onClick={() => setRestartTarget({ ns, name: d.metadata.name })} small ghost />
                      <ActionButton icon="⚖" label="" onClick={() => { setScaleTarget({ ns, name: d.metadata.name, current: d.spec?.replicas ?? 1 }); setScaleCount(d.spec?.replicas ?? 1); }} small ghost />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {selected && (
        <DeploymentPanel
          deployment={selected}
          onClose={() => setSelected(null)}
          onRefresh={refresh}
        />
      )}
    </div>
  );
}

// ── Deployment Detail Panel ──────────────────────────────────

function DeploymentPanel({
  deployment,
  onClose,
  onRefresh,
}: {
  deployment: K8sDeployment;
  onClose: () => void;
  onRefresh: () => void;
}) {
  const ns = deployment.metadata.namespace || 'default';
  const name = deployment.metadata.name;
  const { toast } = useToast();

  // Drill-down state
  type View = 'deployment' | 'replicaset' | 'pod';
  const [view, setView] = useState<View>('deployment');
  const [selectedRS, setSelectedRS] = useState<K8sReplicaSet | null>(null);
  const [selectedPod, setSelectedPod] = useState<K8sPod | null>(null);

  // Fetch child resources
  const selectorLabels = deployment.spec?.selector?.matchLabels;
  const selector = selectorLabels ? Object.entries(selectorLabels).map(([k, v]) => `${k}=${v}`).join(',') : '';

  const { data: rsData } = useApi<K8sList<K8sReplicaSet>>(
    `/api/replicasets?namespace=${ns}&selector=${encodeURIComponent(selector)}`,
    5000
  );

  const { data: podsData } = useApi<K8sList<K8sPod>>(
    `/api/pods?namespace=${ns}&selector=${encodeURIComponent(selector)}`,
    5000
  );

  // Env vars
  const [envVars, setEnvVars] = useState<{ name: string; value: string }[]>([]);
  const [newEnvKey, setNewEnvKey] = useState('');
  const [newEnvVal, setNewEnvVal] = useState('');

  useEffect(() => {
    fetchEnvVars(ns, name).then(setEnvVars).catch(() => setEnvVars([]));
  }, [ns, name]);

  async function addEnvVar() {
    if (!newEnvKey) return;
    const result = await apiPost('/api/env/set', {
      deployment: name,
      namespace: ns,
      env: { [newEnvKey]: newEnvVal },
    });
    if (result.ok) {
      toast(`Set ${newEnvKey}`, 'success');
      setNewEnvKey('');
      setNewEnvVal('');
      const vars = await fetchEnvVars(ns, name);
      setEnvVars(vars);
      onRefresh();
    } else {
      toast(result.error || 'Failed', 'error');
    }
  }

  async function removeEnvVar(key: string) {
    const result = await apiPost('/api/env/unset', {
      deployment: name,
      namespace: ns,
      keys: [key],
    });
    if (result.ok) {
      toast(`Removed ${key}`, 'success');
      const vars = await fetchEnvVars(ns, name);
      setEnvVars(vars);
      onRefresh();
    } else {
      toast(result.error || 'Failed', 'error');
    }
  }

  const replicaSets = (rsData?.items || [])
    .filter(rs => rs.metadata.ownerReferences?.some(ref => ref.name === name))
    .sort((a, b) => (b.metadata.creationTimestamp || '').localeCompare(a.metadata.creationTimestamp || ''));

  const allPods = podsData?.items || [];

  function podsForRS(rs: K8sReplicaSet) {
    return allPods.filter(p =>
      p.metadata.ownerReferences?.some(ref => ref.name === rs.metadata.name)
    );
  }

  // Breadcrumb
  function breadcrumbs() {
    const crumbs: { label: string; onClick?: () => void }[] = [
      { label: `⬡ ${name}`, onClick: view !== 'deployment' ? () => { setView('deployment'); setSelectedRS(null); setSelectedPod(null); } : undefined },
    ];
    if ((view === 'replicaset' || view === 'pod') && selectedRS) {
      crumbs.push({ label: `↳ ${selectedRS.metadata.name}`, onClick: view === 'pod' ? () => { setView('replicaset'); setSelectedPod(null); } : undefined });
    }
    if (view === 'pod' && selectedPod) {
      crumbs.push({ label: `↳ ${selectedPod.metadata.name}` });
    }
    return crumbs;
  }

  return (
    <>
      <div className="panel-overlay" onClick={onClose} />
      <div className="slide-panel">
        <div className="panel-header">
          <h2>{view === 'pod' && selectedPod ? selectedPod.metadata.name : view === 'replicaset' && selectedRS ? selectedRS.metadata.name : name}</h2>
          <button className="panel-close" onClick={onClose}>✕</button>
        </div>

        <div className="panel-breadcrumb">
          {breadcrumbs().map((c, i) => (
            <span key={i}>
              {i > 0 && <span className="crumb-sep"> › </span>}
              {c.onClick ? (
                <span className="crumb" onClick={c.onClick}>{c.label}</span>
              ) : (
                <span className="crumb-active">{c.label}</span>
              )}
            </span>
          ))}
        </div>

        <div className="panel-body">
          {view === 'deployment' && (
            <DeploymentDetail
              deployment={deployment}
              replicaSets={replicaSets}
              podsForRS={podsForRS}
              envVars={envVars}
              onSelectRS={(rs) => { setSelectedRS(rs); setView('replicaset'); }}
              onAddEnv={addEnvVar}
              onRemoveEnv={removeEnvVar}
              newEnvKey={newEnvKey}
              newEnvVal={newEnvVal}
              setNewEnvKey={setNewEnvKey}
              setNewEnvVal={setNewEnvVal}
            />
          )}
          {view === 'replicaset' && selectedRS && (
            <ReplicaSetDetail
              rs={selectedRS}
              pods={podsForRS(selectedRS)}
              onSelectPod={(p) => { setSelectedPod(p); setView('pod'); }}
            />
          )}
          {view === 'pod' && selectedPod && (
            <PodDetail pod={selectedPod} />
          )}
        </div>
      </div>
    </>
  );
}

// ── Deployment Detail View ───────────────────────────────────

function DeploymentDetail({
  deployment,
  replicaSets,
  podsForRS,
  envVars,
  onSelectRS,
  onAddEnv,
  onRemoveEnv,
  newEnvKey,
  newEnvVal,
  setNewEnvKey,
  setNewEnvVal,
}: {
  deployment: K8sDeployment;
  replicaSets: K8sReplicaSet[];
  podsForRS: (rs: K8sReplicaSet) => K8sPod[];
  envVars: { name: string; value: string }[];
  onSelectRS: (rs: K8sReplicaSet) => void;
  onAddEnv: () => void;
  onRemoveEnv: (key: string) => void;
  newEnvKey: string;
  newEnvVal: string;
  setNewEnvKey: (v: string) => void;
  setNewEnvVal: (v: string) => void;
}) {
  const d = deployment;
  const containers = d.spec?.template?.spec?.containers || [];

  return (
    <>
      {/* Overview */}
      <div className="panel-section">
        <div className="panel-section-title">Deployment Info</div>
        <div className="stat-row"><span className="label">Namespace</span><span className="value"><span className="tag">{d.metadata.namespace || 'default'}</span></span></div>
        <div className="stat-row"><span className="label">Strategy</span><span className="value"><span className="tag tag-purple">{d.spec?.strategy?.type || 'RollingUpdate'}</span></span></div>
        <div className="stat-row"><span className="label">Replicas</span><span className="value">{d.status?.readyReplicas ?? 0} ready / {d.spec?.replicas ?? 1} desired</span></div>
        <div className="stat-row"><span className="label">Updated</span><span className="value">{d.status?.updatedReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Available</span><span className="value">{d.status?.availableReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Created</span><span className="value"><TimeAgo timestamp={d.metadata.creationTimestamp} /></span></div>
      </div>

      {/* Containers spec */}
      <div className="panel-section">
        <div className="panel-section-title">Container Templates ({containers.length})</div>
        {containers.map((c) => (
          <div key={c.name} className="container-card">
            <div className="container-card-header">
              <span>▣</span>
              <span className="container-card-name">{c.name}</span>
            </div>
            <div className="container-card-body">
              <div className="stat-row"><span className="label">Image</span><span className="value mono">{c.image}</span></div>
              {c.ports && c.ports.length > 0 && (
                <div className="stat-row"><span className="label">Ports</span><span className="value mono">{c.ports.map(p => p.containerPort).join(', ')}</span></div>
              )}
              {c.resources && (
                <div className="stat-row">
                  <span className="label">Resources</span>
                  <span className="value mono">
                    {c.resources.requests ? `req: ${Object.entries(c.resources.requests).map(([k, v]) => `${k}=${v}`).join(' ')}` : ''}
                    {c.resources.limits ? ` lim: ${Object.entries(c.resources.limits).map(([k, v]) => `${k}=${v}`).join(' ')}` : ''}
                  </span>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>

      {/* ReplicaSets */}
      <div className="panel-section">
        <div className="panel-section-title">ReplicaSets ({replicaSets.length})</div>
        {replicaSets.length === 0 ? (
          <p className="text-dim" style={{ fontSize: 13 }}>No ReplicaSets found</p>
        ) : (
          replicaSets.map((rs) => {
            const pods = podsForRS(rs);
            const ready = (rs.status?.readyReplicas ?? 0);
            const desired = (rs.spec?.replicas ?? 0);
            const isActive = desired > 0;
            return (
              <div key={rs.metadata.name} className="k8s-item" onClick={() => onSelectRS(rs)}>
                <span className="k8s-item-icon">{isActive ? '●' : '○'}</span>
                <div className="k8s-item-info">
                  <div className="k8s-item-name">{rs.metadata.name}</div>
                  <div className="k8s-item-detail">
                    {ready}/{desired} ready · {pods.length} pod{pods.length !== 1 ? 's' : ''} · <TimeAgo timestamp={rs.metadata.creationTimestamp} />
                  </div>
                </div>
                <div className="k8s-item-right">
                  <StatusBadge ok={ready >= desired && desired > 0} label={ready >= desired && desired > 0 ? 'Ready' : desired === 0 ? 'Scaled Down' : 'Progressing'} />
                  <span className="k8s-item-chevron">›</span>
                </div>
              </div>
            );
          })
        )}
      </div>

      {/* Environment Variables */}
      <div className="panel-section">
        <div className="panel-section-title">Environment Variables ({envVars.length})</div>
        {envVars.length > 0 && (
          <table className="env-table">
            <tbody>
              {envVars.map((v) => (
                <tr key={v.name}>
                  <td>{v.name}</td>
                  <td style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
                    <span>{v.value || '(ref)'}</span>
                    <button className="btn btn-sm btn-danger" onClick={() => onRemoveEnv(v.name)} style={{ flexShrink: 0, padding: '2px 6px' }}>✕</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="form-row" style={{ marginTop: envVars.length > 0 ? 10 : 0 }}>
          <input className="form-input" placeholder="KEY" value={newEnvKey} onChange={(e) => setNewEnvKey(e.target.value)} style={{ flex: '0 0 140px' }} />
          <input className="form-input" placeholder="value" value={newEnvVal} onChange={(e) => setNewEnvVal(e.target.value)} />
          <ActionButton icon="+" label="Add" onClick={onAddEnv} small />
        </div>
      </div>

      {/* Conditions */}
      {d.status?.conditions && (
        <div className="panel-section">
          <details>
            <summary>Conditions ({d.status.conditions.length})</summary>
            <ConditionsTable conditions={d.status.conditions} />
          </details>
        </div>
      )}
    </>
  );
}

// ── ReplicaSet Detail View ───────────────────────────────────

function ReplicaSetDetail({
  rs,
  pods,
  onSelectPod,
}: {
  rs: K8sReplicaSet;
  pods: K8sPod[];
  onSelectPod: (p: K8sPod) => void;
}) {
  return (
    <>
      <div className="panel-section">
        <div className="panel-section-title">ReplicaSet Info</div>
        <div className="stat-row"><span className="label">Name</span><span className="value mono">{rs.metadata.name}</span></div>
        <div className="stat-row"><span className="label">Namespace</span><span className="value"><span className="tag">{rs.metadata.namespace || 'default'}</span></span></div>
        <div className="stat-row"><span className="label">Desired</span><span className="value">{rs.spec?.replicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Ready</span><span className="value">{rs.status?.readyReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Available</span><span className="value">{rs.status?.availableReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Created</span><span className="value"><TimeAgo timestamp={rs.metadata.creationTimestamp} /></span></div>
      </div>

      <div className="panel-section">
        <div className="panel-section-title">Pods ({pods.length})</div>
        {pods.length === 0 ? (
          <p className="text-dim" style={{ fontSize: 13 }}>No pods found for this ReplicaSet</p>
        ) : (
          pods.map((pod) => {
            const phase = pod.status?.phase ?? 'Unknown';
            const ok = phase === 'Running' || phase === 'Succeeded';
            const containers = pod.status?.containerStatuses ?? [];
            const readyCount = containers.filter(c => c.ready).length;
            const total = containers.length || pod.spec?.containers?.length || 0;
            const restarts = containers.reduce((s, c) => s + (c.restartCount ?? 0), 0);

            return (
              <div key={pod.metadata.name} className="k8s-item" onClick={() => onSelectPod(pod)}>
                <span className="k8s-item-icon">○</span>
                <div className="k8s-item-info">
                  <div className="k8s-item-name">{pod.metadata.name}</div>
                  <div className="k8s-item-detail">
                    {readyCount}/{total} containers ready
                    {restarts > 0 && <span style={{ color: 'var(--yellow)' }}> · {restarts} restart{restarts !== 1 ? 's' : ''}</span>}
                    {pod.status?.podIP && ` · ${pod.status.podIP}`}
                  </div>
                </div>
                <div className="k8s-item-right">
                  <StatusBadge ok={ok} label={phase} />
                  <span className="k8s-item-chevron">›</span>
                </div>
              </div>
            );
          })
        )}
      </div>
    </>
  );
}

// ── Pod Detail View ──────────────────────────────────────────

function PodDetail({ pod }: { pod: K8sPod }) {
  const [logTarget, setLogTarget] = useState<string | null>(null);
  const [logs, setLogs] = useState('');
  const [logLoading, setLogLoading] = useState(false);

  async function showLogs(container: string) {
    setLogTarget(container);
    setLogLoading(true);
    try {
      const text = await fetchLogs(pod.metadata.namespace || 'default', pod.metadata.name, container);
      setLogs(text);
    } catch {
      setLogs('Failed to fetch logs.');
    }
    setLogLoading(false);
  }

  const containerSpecs = pod.spec?.containers || [];
  const containerStatuses = pod.status?.containerStatuses || [];
  const initSpecs = pod.spec?.initContainers || [];
  const initStatuses = pod.status?.initContainerStatuses || [];

  function containerState(status?: K8sContainerStatus): { label: string; ok: boolean } {
    if (!status) return { label: 'Unknown', ok: false };
    if (status.state?.running) return { label: 'Running', ok: true };
    if (status.state?.waiting) return { label: status.state.waiting.reason || 'Waiting', ok: false };
    if (status.state?.terminated) return { label: `Terminated (${status.state.terminated.reason || 'exit ' + status.state.terminated.exitCode})`, ok: false };
    return { label: 'Unknown', ok: false };
  }

  return (
    <>
      <div className="panel-section">
        <div className="panel-section-title">Pod Info</div>
        <div className="stat-row"><span className="label">Phase</span><StatusBadge ok={pod.status?.phase === 'Running'} label={pod.status?.phase || 'Unknown'} /></div>
        <div className="stat-row"><span className="label">Pod IP</span><span className="value mono">{pod.status?.podIP || '—'}</span></div>
        <div className="stat-row"><span className="label">Host IP</span><span className="value mono">{pod.status?.hostIP || '—'}</span></div>
        <div className="stat-row"><span className="label">Node</span><span className="value mono">{pod.spec?.nodeName || '—'}</span></div>
        <div className="stat-row"><span className="label">Service Account</span><span className="value mono">{pod.spec?.serviceAccountName || 'default'}</span></div>
        <div className="stat-row"><span className="label">Restart Policy</span><span className="value">{pod.spec?.restartPolicy || 'Always'}</span></div>
        <div className="stat-row"><span className="label">Started</span><span className="value"><TimeAgo timestamp={pod.status?.startTime} /></span></div>
      </div>

      {/* Init Containers */}
      {initSpecs.length > 0 && (
        <div className="panel-section">
          <div className="panel-section-title">Init Containers ({initSpecs.length})</div>
          {initSpecs.map((c) => {
            const status = initStatuses.find(s => s.name === c.name);
            return <ContainerDetail key={c.name} spec={c} status={status} containerState={containerState} onShowLogs={() => showLogs(c.name)} />;
          })}
        </div>
      )}

      {/* Containers */}
      <div className="panel-section">
        <div className="panel-section-title">Containers ({containerSpecs.length})</div>
        {containerSpecs.map((c) => {
          const status = containerStatuses.find(s => s.name === c.name);
          return <ContainerDetail key={c.name} spec={c} status={status} containerState={containerState} onShowLogs={() => showLogs(c.name)} />;
        })}
      </div>

      {/* Volumes */}
      {pod.spec?.volumes && pod.spec.volumes.length > 0 && (
        <div className="panel-section">
          <details>
            <summary>Volumes ({pod.spec.volumes.length})</summary>
            <table className="mini-table">
              <thead><tr><th>Name</th><th>Type</th></tr></thead>
              <tbody>
                {pod.spec.volumes.map((v) => {
                  const type = Object.keys(v).filter(k => k !== 'name')[0] || 'unknown';
                  return (
                    <tr key={v.name}>
                      <td className="mono">{v.name}</td>
                      <td className="mono">{type}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </details>
        </div>
      )}

      {/* Conditions */}
      {pod.status?.conditions && (
        <div className="panel-section">
          <details>
            <summary>Conditions ({pod.status.conditions.length})</summary>
            <ConditionsTable conditions={pod.status.conditions} />
          </details>
        </div>
      )}

      {/* Log Viewer */}
      {logTarget && (
        <div className="panel-section">
          <div className="log-viewer">
            <div className="log-header">
              <h3>Logs: <span className="mono">{logTarget}</span></h3>
              <button className="btn btn-sm btn-ghost" onClick={() => { setLogTarget(null); setLogs(''); }}>✕ Close</button>
            </div>
            <pre className="log-output">{logLoading ? 'Loading logs…' : (logs || '(no logs)')}</pre>
          </div>
        </div>
      )}
    </>
  );
}

// ── Container Detail Card ────────────────────────────────────

function ContainerDetail({
  spec,
  status,
  containerState,
  onShowLogs,
}: {
  spec: K8sContainerSpec;
  status?: K8sContainerStatus;
  containerState: (s?: K8sContainerStatus) => { label: string; ok: boolean };
  onShowLogs: () => void;
}) {
  const [open, setOpen] = useState(true);
  const state = containerState(status);

  return (
    <div className="container-card">
      <div className="container-card-header" onClick={() => setOpen(!open)}>
        <span>{open ? '▾' : '▸'}</span>
        <span className="container-card-name">{spec.name}</span>
        <StatusBadge ok={state.ok} label={state.label} />
        <button className="btn btn-sm btn-ghost" onClick={(e) => { e.stopPropagation(); onShowLogs(); }}>≡ Logs</button>
      </div>
      {open && (
        <div className="container-card-body">
          <div className="stat-row"><span className="label">Image</span><span className="value mono">{spec.image}</span></div>
          {status && (
            <div className="stat-row"><span className="label">Restarts</span><span className="value">{status.restartCount > 0 ? <span className="warn-text">{status.restartCount}</span> : '0'}</span></div>
          )}
          {spec.ports && spec.ports.length > 0 && (
            <div className="stat-row"><span className="label">Ports</span><span className="value mono">{spec.ports.map(p => `${p.containerPort}/${p.protocol || 'TCP'}`).join(', ')}</span></div>
          )}
          {spec.command && (
            <div className="stat-row"><span className="label">Command</span><span className="value mono">{spec.command.join(' ')}</span></div>
          )}
          {spec.volumeMounts && spec.volumeMounts.length > 0 && (
            <details style={{ marginTop: 8 }}>
              <summary>Volume Mounts ({spec.volumeMounts.length})</summary>
              <table className="mini-table">
                <thead><tr><th>Name</th><th>Mount Path</th><th>RO</th></tr></thead>
                <tbody>
                  {spec.volumeMounts.map(vm => (
                    <tr key={vm.name + vm.mountPath}>
                      <td className="mono">{vm.name}</td>
                      <td className="mono">{vm.mountPath}</td>
                      <td>{vm.readOnly ? 'yes' : 'no'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          )}
          {spec.env && spec.env.length > 0 && (
            <details open style={{ marginTop: 8 }}>
              <summary>Environment ({spec.env.length})</summary>
              <table className="env-table" style={{ marginTop: 6 }}>
                <tbody>
                  {spec.env.map((e, i) => (
                    <tr key={i}>
                      <td>{e.name}</td>
                      <td>{e.value || (e.valueFrom ? '(from ref)' : '—')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

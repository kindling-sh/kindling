import { useState, useEffect } from 'react';
import { useApi, apiPost, apiDelete, fetchEnvVars, fetchLogs } from '../api';
import type {
  K8sList,
  K8sDeployment,
  K8sReplicaSet,
  K8sPod,
  K8sContainerSpec,
  K8sContainerStatus,
  K8sService,
  K8sIngress,
  K8sSecret,
  K8sEvent,
  K8sServiceAccount,
  K8sRole,
  K8sRoleBinding,
  K8sClusterRole,
  K8sClusterRoleBinding,
} from '../types';
import { StatusBadge, LabelBadges, TimeAgo, EmptyState, ConditionsTable } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

// ── Tab definitions ──────────────────────────────────────────────

type PrimitivesTab = 'deployments' | 'pods' | 'services' | 'ingresses' | 'secrets' | 'events' | 'rbac';

const TABS: { key: PrimitivesTab; icon: string; label: string }[] = [
  { key: 'deployments', icon: '□', label: 'Deployments' },
  { key: 'pods', icon: '○', label: 'Pods' },
  { key: 'services', icon: '◎', label: 'Services' },
  { key: 'ingresses', icon: '⊕', label: 'Ingresses' },
  { key: 'secrets', icon: '◈', label: 'Secrets' },
  { key: 'events', icon: '◇', label: 'Events' },
  { key: 'rbac', icon: '⊘', label: 'RBAC' },
];

export function K8sPrimitivesPage() {
  const [tab, setTab] = useState<PrimitivesTab>('deployments');

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Cluster Resources</h1>
          <p className="page-subtitle">Deployments, pods, services, networking, secrets &amp; access control</p>
        </div>
      </div>

      <div className="tab-bar">
        {TABS.map((t) => (
          <button
            key={t.key}
            className={`tab-btn${tab === t.key ? ' tab-active' : ''}`}
            onClick={() => setTab(t.key)}
          >
            <span style={{ marginRight: 6 }}>{t.icon}</span>
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'deployments' && <DeploymentsTab />}
      {tab === 'pods' && <PodsTab />}
      {tab === 'services' && <ServicesTab />}
      {tab === 'ingresses' && <IngressesTab />}
      {tab === 'secrets' && <SecretsTab />}
      {tab === 'events' && <EventsTab />}
      {tab === 'rbac' && <RBACTab />}
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Shared: Container detail card (used by both Deployments & Pods)
// ═══════════════════════════════════════════════════════════════

function containerStateLabel(status?: K8sContainerStatus): { label: string; ok: boolean } {
  if (!status) return { label: 'Unknown', ok: false };
  if (status.state?.running) return { label: 'Running', ok: true };
  if (status.state?.waiting) return { label: status.state.waiting.reason || 'Waiting', ok: false };
  if (status.state?.terminated) return { label: `Terminated (${status.state.terminated.reason || 'exit ' + status.state.terminated.exitCode})`, ok: false };
  return { label: 'Unknown', ok: false };
}

function ContainerCard({
  spec,
  status,
  onShowLogs,
}: {
  spec: K8sContainerSpec;
  status?: K8sContainerStatus;
  onShowLogs: () => void;
}) {
  const [open, setOpen] = useState(true);
  const state = containerStateLabel(status);

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
          {spec.resources && (
            <div className="stat-row">
              <span className="label">Resources</span>
              <span className="value mono">
                {spec.resources.requests ? `req: ${Object.entries(spec.resources.requests).map(([k, v]) => `${k}=${v}`).join(' ')}` : ''}
                {spec.resources.limits ? ` lim: ${Object.entries(spec.resources.limits).map(([k, v]) => `${k}=${v}`).join(' ')}` : ''}
              </span>
            </div>
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
          {spec.volumeMounts && spec.volumeMounts.length > 0 && (
            <details style={{ marginTop: 8 }}>
              <summary>Volume Mounts ({spec.volumeMounts.length})</summary>
              <table className="mini-table">
                <thead><tr><th>Name</th><th>Path</th><th>RO</th></tr></thead>
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
        </div>
      )}
    </div>
  );
}

// Shared: log viewer for pods
function LogViewer({ pod, container, onClose }: { pod: K8sPod; container: string; onClose: () => void }) {
  const [logs, setLogs] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    fetchLogs(pod.metadata.namespace || 'default', pod.metadata.name, container)
      .then(setLogs)
      .catch(() => setLogs('Failed to fetch logs.'))
      .finally(() => setLoading(false));
  }, [pod.metadata.namespace, pod.metadata.name, container]);

  return (
    <div className="panel-section">
      <div className="log-viewer">
        <div className="log-header">
          <h3>Logs: <span className="mono">{container}</span></h3>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>✕ Close</button>
        </div>
        <pre className="log-output">{loading ? 'Loading logs…' : (logs || '(no logs)')}</pre>
      </div>
    </div>
  );
}

// Shared: Pod info section (used in both Deployment drill-down and Pods panel)
function PodInfoSection({ pod }: { pod: K8sPod }) {
  const [logTarget, setLogTarget] = useState<string | null>(null);

  const containerSpecs = pod.spec?.containers || [];
  const containerStatuses = pod.status?.containerStatuses || [];
  const initSpecs = pod.spec?.initContainers || [];
  const initStatuses = pod.status?.initContainerStatuses || [];

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

      {initSpecs.length > 0 && (
        <div className="panel-section">
          <div className="panel-section-title">Init Containers ({initSpecs.length})</div>
          {initSpecs.map((c) => {
            const status = initStatuses.find(s => s.name === c.name);
            return <ContainerCard key={c.name} spec={c} status={status} onShowLogs={() => setLogTarget(c.name)} />;
          })}
        </div>
      )}

      <div className="panel-section">
        <div className="panel-section-title">Containers ({containerSpecs.length})</div>
        {containerSpecs.map((c) => {
          const status = containerStatuses.find(s => s.name === c.name);
          return <ContainerCard key={c.name} spec={c} status={status} onShowLogs={() => setLogTarget(c.name)} />;
        })}
      </div>

      {pod.spec?.volumes && pod.spec.volumes.length > 0 && (
        <div className="panel-section">
          <details>
            <summary>Volumes ({pod.spec.volumes.length})</summary>
            <table className="mini-table">
              <thead><tr><th>Name</th><th>Type</th></tr></thead>
              <tbody>
                {pod.spec.volumes.map((v) => {
                  const type = Object.keys(v).filter(k => k !== 'name')[0] || 'unknown';
                  return <tr key={v.name}><td className="mono">{v.name}</td><td className="mono">{type}</td></tr>;
                })}
              </tbody>
            </table>
          </details>
        </div>
      )}

      {pod.status?.conditions && (
        <div className="panel-section">
          <details>
            <summary>Conditions ({pod.status.conditions.length})</summary>
            <ConditionsTable conditions={pod.status.conditions} />
          </details>
        </div>
      )}

      {logTarget && <LogViewer pod={pod} container={logTarget} onClose={() => setLogTarget(null)} />}
    </>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: Deployments
// ═══════════════════════════════════════════════════════════════

function DeploymentsTab() {
  const { data, loading, refresh } = useApi<K8sList<K8sDeployment>>('/api/deployments');
  const { toast } = useToast();
  const [selected, setSelected] = useState<K8sDeployment | null>(null);
  const [scaleTarget, setScaleTarget] = useState<{ ns: string; name: string; current: number } | null>(null);
  const [scaleCount, setScaleCount] = useState(1);
  const [restartTarget, setRestartTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleRestart() {
    if (!restartTarget) return;
    setRestartTarget(null);
    const result = await apiPost(`/api/restart/${restartTarget.ns}/${restartTarget.name}`);
    if (result.ok) { toast(`Restarted ${restartTarget.name}`, 'success'); refresh(); }
    else { toast(result.error || 'Restart failed', 'error'); }
  }

  async function handleScale() {
    if (!scaleTarget) return;
    const result = await apiPost(`/api/scale/${scaleTarget.ns}/${scaleTarget.name}`, { replicas: scaleCount });
    if (result.ok) { toast(`Scaled ${scaleTarget.name} to ${scaleCount}`, 'success'); setScaleTarget(null); refresh(); }
    else { toast(result.error || 'Scale failed', 'error'); }
  }

  if (loading) return <div className="loading">Loading deployments…</div>;
  const items = data?.items || [];

  return (
    <>
      {restartTarget && (
        <ConfirmDialog title="Restart Deployment" message={`Rolling restart ${restartTarget.name}?`} confirmLabel="Restart" onConfirm={handleRestart} onCancel={() => setRestartTarget(null)} />
      )}
      {scaleTarget && (
        <ActionModal title={`Scale ${scaleTarget.name}`} submitLabel="Scale" onSubmit={handleScale} onClose={() => setScaleTarget(null)}>
          <label className="form-label">Replicas (current: {scaleTarget.current})</label>
          <input className="form-input" type="number" min={0} max={20} value={scaleCount} onChange={(e) => setScaleCount(Number(e.target.value))} />
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
                  <tr key={`${ns}/${d.metadata.name}`} className="clickable-row" onClick={() => setSelected(d)}>
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

      {selected && <DeploymentPanel deployment={selected} onClose={() => setSelected(null)} onRefresh={refresh} />}
    </>
  );
}

// ── Deployment Detail Panel ──────────────────────────────────

function DeploymentPanel({ deployment, onClose, onRefresh }: { deployment: K8sDeployment; onClose: () => void; onRefresh: () => void }) {
  const ns = deployment.metadata.namespace || 'default';
  const name = deployment.metadata.name;
  const { toast } = useToast();

  type View = 'deployment' | 'replicaset' | 'pod';
  const [view, setView] = useState<View>('deployment');
  const [selectedRS, setSelectedRS] = useState<K8sReplicaSet | null>(null);
  const [selectedPod, setSelectedPod] = useState<K8sPod | null>(null);

  const selectorLabels = deployment.spec?.selector?.matchLabels;
  const selector = selectorLabels ? Object.entries(selectorLabels).map(([k, v]) => `${k}=${v}`).join(',') : '';
  const { data: rsData } = useApi<K8sList<K8sReplicaSet>>(`/api/replicasets?namespace=${ns}&selector=${encodeURIComponent(selector)}`, 5000);
  const { data: podsData } = useApi<K8sList<K8sPod>>(`/api/pods?namespace=${ns}&selector=${encodeURIComponent(selector)}`, 5000);

  const [envVars, setEnvVars] = useState<{ name: string; value: string }[]>([]);
  const [newEnvKey, setNewEnvKey] = useState('');
  const [newEnvVal, setNewEnvVal] = useState('');

  useEffect(() => { fetchEnvVars(ns, name).then(setEnvVars).catch(() => setEnvVars([])); }, [ns, name]);

  async function addEnvVar() {
    if (!newEnvKey) return;
    const result = await apiPost('/api/env/set', { deployment: name, namespace: ns, env: { [newEnvKey]: newEnvVal } });
    if (result.ok) {
      toast(`Set ${newEnvKey}`, 'success'); setNewEnvKey(''); setNewEnvVal('');
      setEnvVars(await fetchEnvVars(ns, name)); onRefresh();
    } else { toast(result.error || 'Failed', 'error'); }
  }

  async function removeEnvVar(key: string) {
    const result = await apiPost('/api/env/unset', { deployment: name, namespace: ns, keys: [key] });
    if (result.ok) { toast(`Removed ${key}`, 'success'); setEnvVars(await fetchEnvVars(ns, name)); onRefresh(); }
    else { toast(result.error || 'Failed', 'error'); }
  }

  const replicaSets = (rsData?.items || [])
    .filter(rs => rs.metadata.ownerReferences?.some(ref => ref.name === name))
    .sort((a, b) => (b.metadata.creationTimestamp || '').localeCompare(a.metadata.creationTimestamp || ''));
  const allPods = podsData?.items || [];
  function podsForRS(rs: K8sReplicaSet) {
    return allPods.filter(p => p.metadata.ownerReferences?.some(ref => ref.name === rs.metadata.name));
  }

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
              {c.onClick ? <span className="crumb" onClick={c.onClick}>{c.label}</span> : <span className="crumb-active">{c.label}</span>}
            </span>
          ))}
        </div>
        <div className="panel-body">
          {view === 'deployment' && (
            <DeploymentDetail
              deployment={deployment} replicaSets={replicaSets} podsForRS={podsForRS}
              envVars={envVars} onSelectRS={(rs) => { setSelectedRS(rs); setView('replicaset'); }}
              onAddEnv={addEnvVar} onRemoveEnv={removeEnvVar}
              newEnvKey={newEnvKey} newEnvVal={newEnvVal} setNewEnvKey={setNewEnvKey} setNewEnvVal={setNewEnvVal}
            />
          )}
          {view === 'replicaset' && selectedRS && (
            <ReplicaSetDetail rs={selectedRS} pods={podsForRS(selectedRS)} onSelectPod={(p) => { setSelectedPod(p); setView('pod'); }} />
          )}
          {view === 'pod' && selectedPod && <PodInfoSection pod={selectedPod} />}
        </div>
      </div>
    </>
  );
}

function DeploymentDetail({
  deployment, replicaSets, podsForRS, envVars, onSelectRS, onAddEnv, onRemoveEnv,
  newEnvKey, newEnvVal, setNewEnvKey, setNewEnvVal,
}: {
  deployment: K8sDeployment; replicaSets: K8sReplicaSet[]; podsForRS: (rs: K8sReplicaSet) => K8sPod[];
  envVars: { name: string; value: string }[]; onSelectRS: (rs: K8sReplicaSet) => void;
  onAddEnv: () => void; onRemoveEnv: (key: string) => void;
  newEnvKey: string; newEnvVal: string; setNewEnvKey: (v: string) => void; setNewEnvVal: (v: string) => void;
}) {
  const d = deployment;
  const containers = d.spec?.template?.spec?.containers || [];

  return (
    <>
      <div className="panel-section">
        <div className="panel-section-title">Deployment Info</div>
        <div className="stat-row"><span className="label">Namespace</span><span className="value"><span className="tag">{d.metadata.namespace || 'default'}</span></span></div>
        <div className="stat-row"><span className="label">Strategy</span><span className="value"><span className="tag tag-purple">{d.spec?.strategy?.type || 'RollingUpdate'}</span></span></div>
        <div className="stat-row"><span className="label">Replicas</span><span className="value">{d.status?.readyReplicas ?? 0} ready / {d.spec?.replicas ?? 1} desired</span></div>
        <div className="stat-row"><span className="label">Updated</span><span className="value">{d.status?.updatedReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Available</span><span className="value">{d.status?.availableReplicas ?? 0}</span></div>
        <div className="stat-row"><span className="label">Created</span><span className="value"><TimeAgo timestamp={d.metadata.creationTimestamp} /></span></div>
      </div>

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

      <div className="panel-section">
        <div className="panel-section-title">ReplicaSets ({replicaSets.length})</div>
        {replicaSets.length === 0 ? (
          <p className="text-dim" style={{ fontSize: 13 }}>No ReplicaSets found</p>
        ) : (
          replicaSets.map((rs) => {
            const pods = podsForRS(rs);
            const ready = rs.status?.readyReplicas ?? 0;
            const desired = rs.spec?.replicas ?? 0;
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

function ReplicaSetDetail({ rs, pods, onSelectPod }: { rs: K8sReplicaSet; pods: K8sPod[]; onSelectPod: (p: K8sPod) => void }) {
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

// ═══════════════════════════════════════════════════════════════
//  Tab: Pods
// ═══════════════════════════════════════════════════════════════

function PodsTab() {
  const { data, loading, refresh } = useApi<K8sList<K8sPod>>('/api/pods');
  const { toast } = useToast();
  const [selected, setSelected] = useState<K8sPod | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleDeletePod() {
    if (!deleteTarget) return;
    const result = await apiDelete(`/api/pods/${deleteTarget.ns}/${deleteTarget.name}`);
    if (result.ok) { toast(`Pod ${deleteTarget.name} deleted`, 'success'); refresh(); }
    else { toast(result.error || 'Delete failed', 'error'); }
    setDeleteTarget(null);
  }

  if (loading) return <div className="loading">Loading pods…</div>;
  const items = data?.items || [];

  return (
    <>
      {deleteTarget && (
        <ConfirmDialog title="Delete Pod" message={`Delete pod '${deleteTarget.name}'? It will be recreated by its controller.`}
          confirmLabel="Delete" danger onConfirm={handleDeletePod} onCancel={() => setDeleteTarget(null)} />
      )}

      {items.length === 0 ? (
        <EmptyState icon="○" message="No pods found in the cluster." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Phase</th>
                <th>Ready</th>
                <th>Restarts</th>
                <th>IP</th>
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
                  <tr key={`${p.metadata.namespace}/${p.metadata.name}`} className="clickable-row" onClick={() => setSelected(p)}>
                    <td className="mono" style={{ fontWeight: 550 }}>{p.metadata.name}</td>
                    <td><span className="tag">{p.metadata.namespace}</span></td>
                    <td><StatusBadge ok={ok} label={phase} /></td>
                    <td>{readyCount}/{total}</td>
                    <td>{restarts > 0 ? <span className="warn-text">{restarts}</span> : '0'}</td>
                    <td className="mono">{p.status?.podIP ?? '—'}</td>
                    <td className="mono truncate">{p.spec?.nodeName ?? '—'}</td>
                    <td><TimeAgo timestamp={p.metadata.creationTimestamp} /></td>
                    <td onClick={(e) => e.stopPropagation()}>
                      <button className="btn btn-sm btn-danger" onClick={() => setDeleteTarget({ ns: p.metadata.namespace || 'default', name: p.metadata.name })}>✕</button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {selected && (
        <>
          <div className="panel-overlay" onClick={() => setSelected(null)} />
          <div className="slide-panel">
            <div className="panel-header">
              <h2>{selected.metadata.name}</h2>
              <button className="panel-close" onClick={() => setSelected(null)}>✕</button>
            </div>
            <div className="panel-breadcrumb">
              {selected.metadata.ownerReferences?.[0] && (
                <>
                  <span className="crumb">{selected.metadata.ownerReferences[0].kind}/{selected.metadata.ownerReferences[0].name}</span>
                  <span className="crumb-sep"> › </span>
                </>
              )}
              <span className="crumb-active">{selected.metadata.name}</span>
            </div>
            <div className="panel-body">
              <PodInfoSection pod={selected} />
            </div>
          </div>
        </>
      )}
    </>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: Services
// ═══════════════════════════════════════════════════════════════

function ServicesTab() {
  const { data, loading } = useApi<K8sList<K8sService>>('/api/services');
  if (loading) return <div className="loading">Loading services…</div>;

  const services = (data?.items || []).filter(
    (s) => s.metadata.namespace !== 'kube-system' && s.metadata.namespace !== 'local-path-storage'
  );
  if (services.length === 0) return <EmptyState icon="○" message="No services found in workload namespaces." />;

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr><th>Name</th><th>Namespace</th><th>Type</th><th>Cluster IP</th><th>Ports</th><th>Selector</th><th>Age</th></tr>
        </thead>
        <tbody>
          {services.map((svc) => {
            const ports = svc.spec.ports?.map((p) => {
              let s = `${p.port}`;
              if (p.targetPort) s += `→${p.targetPort}`;
              if (p.protocol && p.protocol !== 'TCP') s += `/${p.protocol}`;
              if (p.nodePort) s += ` (node:${p.nodePort})`;
              return s;
            }).join(', ') || '—';
            return (
              <tr key={`${svc.metadata.namespace}/${svc.metadata.name}`}>
                <td className="mono">{svc.metadata.name}</td>
                <td><span className="tag">{svc.metadata.namespace}</span></td>
                <td><StatusBadge ok={svc.spec.type === 'ClusterIP' || svc.spec.type === 'LoadBalancer'} label={svc.spec.type || 'ClusterIP'} /></td>
                <td className="mono">{svc.spec.clusterIP || '—'}</td>
                <td className="mono">{ports}</td>
                <td>{svc.spec.selector ? <LabelBadges labels={svc.spec.selector} /> : <span className="text-dim">—</span>}</td>
                <td><TimeAgo timestamp={svc.metadata.creationTimestamp} /></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: Ingresses
// ═══════════════════════════════════════════════════════════════

function IngressesTab() {
  const { data, loading } = useApi<K8sList<K8sIngress>>('/api/ingresses');
  if (loading) return <div className="loading">Loading ingresses…</div>;

  const ingresses = data?.items || [];
  if (ingresses.length === 0) return <EmptyState icon="◎" message="No ingresses configured." />;

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr><th>Name</th><th>Namespace</th><th>Class</th><th>Hosts</th><th>Paths</th><th>Status</th><th>Age</th></tr>
        </thead>
        <tbody>
          {ingresses.map((ing) => {
            const rules = ing.spec.rules || [];
            const hosts = rules.map((r) => r.host || '*').join(', ') || '—';
            const paths = rules.flatMap((r) =>
              (r.http?.paths || []).map((p) => {
                const svc = p.backend?.service;
                const pathStr = p.path || '/';
                const svcStr = svc ? `${svc.name}:${svc.port?.number || svc.port?.name || '?'}` : '?';
                return `${pathStr} → ${svcStr}`;
              })
            );
            const hasIP = ing.status?.loadBalancer?.ingress?.some((i: any) => i.ip || i.hostname);
            return (
              <tr key={`${ing.metadata.namespace}/${ing.metadata.name}`}>
                <td className="mono">{ing.metadata.name}</td>
                <td><span className="tag">{ing.metadata.namespace}</span></td>
                <td className="mono">{ing.spec.ingressClassName || '—'}</td>
                <td className="mono">{hosts}</td>
                <td>
                  {paths.length > 0 ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                      {paths.map((p, i) => <span key={i} className="mono" style={{ fontSize: '0.8em' }}>{p}</span>)}
                    </div>
                  ) : <span className="text-dim">—</span>}
                </td>
                <td><StatusBadge ok={!!hasIP} label={hasIP ? 'Active' : 'Pending'} /></td>
                <td><TimeAgo timestamp={ing.metadata.creationTimestamp} /></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: Secrets
// ═══════════════════════════════════════════════════════════════

function SecretsTab() {
  const { data, loading, refresh } = useApi<K8sList<K8sSecret>>('/api/secrets');
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<K8sSecret | null>(null);
  const [form, setForm] = useState({ name: '', namespace: 'default', key: '', value: '' });

  async function handleCreate() {
    setCreating(true);
    const result = await apiPost('/api/secrets/create', form);
    setCreating(false);
    if (result.ok) { toast('Secret created', 'success'); setShowCreate(false); setForm({ name: '', namespace: 'default', key: '', value: '' }); refresh(); }
    else { toast(result.error || 'Failed to create secret', 'error'); }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const result = await apiPost('/api/secrets/delete', { name: deleteTarget.metadata.name, namespace: deleteTarget.metadata.namespace });
    setDeleteTarget(null);
    if (result.ok) { toast('Secret deleted', 'success'); refresh(); }
    else { toast(result.error || 'Failed to delete secret', 'error'); }
  }

  if (loading) return <div className="loading">Loading secrets…</div>;

  const secrets = (data?.items || []).filter(
    (s) => s.metadata.namespace !== 'kube-system' && s.metadata.namespace !== 'local-path-storage'
  );

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <ActionButton icon="+" label="Create Secret" onClick={() => setShowCreate(true)} primary />
      </div>

      {showCreate && (
        <ActionModal title="Create Secret" submitLabel="Create" loading={creating} onSubmit={handleCreate} onClose={() => setShowCreate(false)}>
          <label className="form-label">Name</label>
          <input className="form-input" placeholder="my-secret" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <label className="form-label">Namespace</label>
          <input className="form-input" placeholder="default" value={form.namespace} onChange={(e) => setForm({ ...form, namespace: e.target.value })} />
          <label className="form-label">Key</label>
          <input className="form-input" placeholder="SECRET_KEY" value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} />
          <label className="form-label">Value</label>
          <input className="form-input" type="password" placeholder="secret value" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} />
        </ActionModal>
      )}

      {deleteTarget && (
        <ConfirmDialog title="Delete Secret" message={`Delete secret "${deleteTarget.metadata.name}" from namespace "${deleteTarget.metadata.namespace}"?`}
          confirmLabel="Delete" danger onConfirm={handleDelete} onCancel={() => setDeleteTarget(null)} />
      )}

      {secrets.length === 0 ? (
        <EmptyState icon="⊕" message="No secrets found. Create one to get started." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr><th>Name</th><th>Namespace</th><th>Type</th><th>Keys</th><th>Age</th><th style={{ width: 50 }}></th></tr>
            </thead>
            <tbody>
              {secrets.map((sec) => {
                const keys = sec.data ? Object.keys(sec.data) : [];
                return (
                  <tr key={`${sec.metadata.namespace}/${sec.metadata.name}`}>
                    <td className="mono">{sec.metadata.name}</td>
                    <td><span className="tag">{sec.metadata.namespace}</span></td>
                    <td className="mono" style={{ fontSize: '0.8em' }}>{sec.type || 'Opaque'}</td>
                    <td>{keys.length > 0 ? <span>{keys.length} key{keys.length !== 1 ? 's' : ''}</span> : <span className="text-dim">empty</span>}</td>
                    <td><TimeAgo timestamp={sec.metadata.creationTimestamp} /></td>
                    <td><ActionButton icon="✕" label="" onClick={() => setDeleteTarget(sec)} danger ghost /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: Events
// ═══════════════════════════════════════════════════════════════

function EventsTab() {
  const { data, loading } = useApi<K8sList<K8sEvent>>('/api/events');
  if (loading) return <div className="loading">Loading events…</div>;

  const events = (data?.items || []).filter(
    (e) => e.metadata.namespace !== 'kube-system' && e.metadata.namespace !== 'local-path-storage'
  );
  events.sort((a, b) => {
    const ta = a.lastTimestamp || a.metadata.creationTimestamp || '';
    const tb = b.lastTimestamp || b.metadata.creationTimestamp || '';
    return tb.localeCompare(ta);
  });

  if (events.length === 0) return <EmptyState icon="◈" message="No events in workload namespaces." />;

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th style={{ width: 72 }}>Type</th><th>Reason</th><th>Object</th><th>Namespace</th><th>Message</th><th style={{ width: 60 }}>Count</th><th>Last Seen</th>
          </tr>
        </thead>
        <tbody>
          {events.map((ev, i) => (
            <tr key={`${ev.metadata.namespace}/${ev.metadata.name}-${i}`}>
              <td><span className={`event-badge event-${(ev.type || 'Normal').toLowerCase()}`}>{ev.type || 'Normal'}</span></td>
              <td className="mono">{ev.reason}</td>
              <td className="mono" style={{ fontSize: '0.8em' }}>{ev.involvedObject?.kind}/{ev.involvedObject?.name}</td>
              <td><span className="tag">{ev.metadata.namespace}</span></td>
              <td style={{ maxWidth: 400, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{ev.message}</td>
              <td style={{ textAlign: 'center' }}>{ev.count || 1}</td>
              <td><TimeAgo timestamp={ev.lastTimestamp || ev.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ═══════════════════════════════════════════════════════════════
//  Tab: RBAC
// ═══════════════════════════════════════════════════════════════

type RBACSubTab = 'serviceaccounts' | 'roles' | 'rolebindings' | 'clusterroles' | 'clusterrolebindings';

const RBAC_TABS: { key: RBACSubTab; label: string }[] = [
  { key: 'serviceaccounts', label: 'Service Accounts' },
  { key: 'roles', label: 'Roles' },
  { key: 'rolebindings', label: 'Role Bindings' },
  { key: 'clusterroles', label: 'Cluster Roles' },
  { key: 'clusterrolebindings', label: 'Cluster Role Bindings' },
];

function RBACTab() {
  const [subTab, setSubTab] = useState<RBACSubTab>('serviceaccounts');
  return (
    <>
      <div className="tab-bar" style={{ marginTop: 12 }}>
        {RBAC_TABS.map((t) => (
          <button key={t.key} className={`tab-btn${subTab === t.key ? ' tab-active' : ''}`} onClick={() => setSubTab(t.key)} style={{ fontSize: '0.85em' }}>{t.label}</button>
        ))}
      </div>
      {subTab === 'serviceaccounts' && <ServiceAccountsSubTab />}
      {subTab === 'roles' && <RolesSubTab />}
      {subTab === 'rolebindings' && <RoleBindingsSubTab />}
      {subTab === 'clusterroles' && <ClusterRolesSubTab />}
      {subTab === 'clusterrolebindings' && <ClusterRoleBindingsSubTab />}
    </>
  );
}

function ServiceAccountsSubTab() {
  const { data, loading } = useApi<K8sList<K8sServiceAccount>>('/api/serviceaccounts');
  if (loading) return <div className="loading">Loading service accounts…</div>;
  const items = (data?.items || []).filter(sa => sa.metadata.namespace !== 'kube-system' && sa.metadata.namespace !== 'local-path-storage');
  if (items.length === 0) return <EmptyState icon="⊞" message="No service accounts found in workload namespaces." />;
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Secrets</th><th>Age</th></tr></thead>
        <tbody>
          {items.map((sa) => (
            <tr key={`${sa.metadata.namespace}/${sa.metadata.name}`}>
              <td className="mono">{sa.metadata.name}</td>
              <td><span className="tag">{sa.metadata.namespace}</span></td>
              <td className="mono">{sa.secrets?.length ?? 0}</td>
              <td><TimeAgo timestamp={sa.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RolesSubTab() {
  const { data, loading } = useApi<K8sList<K8sRole>>('/api/roles');
  if (loading) return <div className="loading">Loading roles…</div>;
  const items = (data?.items || []).filter(r => r.metadata.namespace !== 'kube-system' && r.metadata.namespace !== 'local-path-storage');
  if (items.length === 0) return <EmptyState icon="⊘" message="No roles found in workload namespaces." />;
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Rules</th><th>Age</th></tr></thead>
        <tbody>
          {items.map((role) => (
            <tr key={`${role.metadata.namespace}/${role.metadata.name}`}>
              <td className="mono">{role.metadata.name}</td>
              <td><span className="tag">{role.metadata.namespace}</span></td>
              <td>{role.rules?.length ?? 0}</td>
              <td><TimeAgo timestamp={role.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RoleBindingsSubTab() {
  const { data, loading } = useApi<K8sList<K8sRoleBinding>>('/api/rolebindings');
  if (loading) return <div className="loading">Loading role bindings…</div>;
  const items = (data?.items || []).filter(rb => rb.metadata.namespace !== 'kube-system' && rb.metadata.namespace !== 'local-path-storage');
  if (items.length === 0) return <EmptyState icon="⊘" message="No role bindings found in workload namespaces." />;
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>Name</th><th>Namespace</th><th>Role</th><th>Subjects</th><th>Age</th></tr></thead>
        <tbody>
          {items.map((rb) => (
            <tr key={`${rb.metadata.namespace}/${rb.metadata.name}`}>
              <td className="mono">{rb.metadata.name}</td>
              <td><span className="tag">{rb.metadata.namespace}</span></td>
              <td className="mono">{rb.roleRef.kind}/{rb.roleRef.name}</td>
              <td>{rb.subjects?.map((s, i) => <span key={i} className="tag" style={{ marginRight: 4 }}>{s.kind}:{s.name}</span>) || <span className="text-dim">—</span>}</td>
              <td><TimeAgo timestamp={rb.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ClusterRolesSubTab() {
  const { data, loading } = useApi<K8sList<K8sClusterRole>>('/api/clusterroles');
  if (loading) return <div className="loading">Loading cluster roles…</div>;
  const items = (data?.items || []).filter(cr => !cr.metadata.name.startsWith('system:'));
  if (items.length === 0) return <EmptyState icon="⊘" message="No custom cluster roles found." />;
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>Name</th><th>Rules</th><th>Age</th></tr></thead>
        <tbody>
          {items.map((cr) => (
            <tr key={cr.metadata.name}>
              <td className="mono">{cr.metadata.name}</td>
              <td>{cr.rules?.length ?? 0}</td>
              <td><TimeAgo timestamp={cr.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ClusterRoleBindingsSubTab() {
  const { data, loading } = useApi<K8sList<K8sClusterRoleBinding>>('/api/clusterrolebindings');
  if (loading) return <div className="loading">Loading cluster role bindings…</div>;
  const items = (data?.items || []).filter(crb => !crb.metadata.name.startsWith('system:'));
  if (items.length === 0) return <EmptyState icon="⊘" message="No custom cluster role bindings found." />;
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>Name</th><th>Role</th><th>Subjects</th><th>Age</th></tr></thead>
        <tbody>
          {items.map((crb) => (
            <tr key={crb.metadata.name}>
              <td className="mono">{crb.metadata.name}</td>
              <td className="mono">{crb.roleRef.kind}/{crb.roleRef.name}</td>
              <td>{crb.subjects?.map((s, i) => <span key={i} className="tag" style={{ marginRight: 4 }}>{s.kind}:{s.name}</span>) || <span className="text-dim">—</span>}</td>
              <td><TimeAgo timestamp={crb.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

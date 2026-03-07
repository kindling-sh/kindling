import { useState } from 'react';
import { useApi, prodRestart, prodScale, prodDeletePod, prodRollback, prodExec, fetchProdLogs, fetchProdRolloutHistory } from '../api';
import type { K8sList, K8sDeployment, K8sPod, K8sStatefulSet, K8sDaemonSet, RolloutRevision } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

type WorkloadTab = 'deployments' | 'statefulsets' | 'daemonsets';

export function ProductionWorkloadsPage() {
  const { data: deps, loading, refresh } = useApi<K8sList<K8sDeployment>>('/api/prod/deployments');
  const { data: podsData } = useApi<K8sList<K8sPod>>('/api/prod/pods');
  const { data: stsData } = useApi<K8sList<K8sStatefulSet>>('/api/prod/statefulsets');
  const { data: dsData } = useApi<K8sList<K8sDaemonSet>>('/api/prod/daemonsets');
  const { toast } = useToast();

  const [selected, setSelected] = useState<K8sDeployment | null>(null);
  const [tab, setTab] = useState<WorkloadTab>('deployments');
  const [scaleTarget, setScaleTarget] = useState<{ ns: string; name: string; current: number } | null>(null);
  const [scaleCount, setScaleCount] = useState(1);
  const [restartTarget, setRestartTarget] = useState<{ ns: string; name: string } | null>(null);

  // Rollback
  const [rollbackTarget, setRollbackTarget] = useState<{ ns: string; name: string } | null>(null);
  const [rollbackHistory, setRollbackHistory] = useState<RolloutRevision[]>([]);
  const [rollbackRev, setRollbackRev] = useState(0);
  const [rollbackLoading, setRollbackLoading] = useState(false);

  // Exec
  const [execTarget, setExecTarget] = useState<{ ns: string; pod: string } | null>(null);
  const [execCmd, setExecCmd] = useState('');
  const [execOutput, setExecOutput] = useState('');
  const [execRunning, setExecRunning] = useState(false);

  // Logs
  const [logsTarget, setLogsTarget] = useState<{ ns: string; pod: string } | null>(null);
  const [logs, setLogs] = useState('');

  // Delete pod
  const [deleteTarget, setDeleteTarget] = useState<{ ns: string; pod: string } | null>(null);

  async function handleRestart() {
    if (!restartTarget) return;
    setRestartTarget(null);
    const r = await prodRestart(restartTarget.ns, restartTarget.name);
    if (r.ok) { toast(`Restarted ${restartTarget.name}`, 'success'); refresh(); }
    else toast(r.error || 'Restart failed', 'error');
  }

  async function handleScale() {
    if (!scaleTarget) return;
    const r = await prodScale(scaleTarget.ns, scaleTarget.name, scaleCount);
    if (r.ok) { toast(`Scaled to ${scaleCount}`, 'success'); setScaleTarget(null); refresh(); }
    else toast(r.error || 'Scale failed', 'error');
  }

  async function openRollback(ns: string, name: string) {
    setRollbackTarget({ ns, name });
    setRollbackLoading(true);
    try {
      const h = await fetchProdRolloutHistory(ns, name);
      setRollbackHistory(h.items || []);
    } catch { setRollbackHistory([]); }
    setRollbackLoading(false);
  }

  async function handleRollback() {
    if (!rollbackTarget) return;
    const r = await prodRollback(rollbackTarget.ns, rollbackTarget.name, rollbackRev || undefined);
    if (r.ok) { toast(r.output || 'Rollback initiated', 'success'); setRollbackTarget(null); refresh(); }
    else toast(r.error || 'Rollback failed', 'error');
  }

  async function handleExec() {
    if (!execTarget || !execCmd) return;
    setExecRunning(true);
    const r = await prodExec(execTarget.ns, execTarget.pod, execCmd);
    setExecOutput(r.output || r.error || '(no output)');
    setExecRunning(false);
  }

  async function openLogs(ns: string, pod: string) {
    setLogsTarget({ ns, pod });
    setLogs('Loading...');
    try {
      const l = await fetchProdLogs(ns, pod);
      setLogs(l || '(no logs)');
    } catch (e) { setLogs('Failed to fetch logs'); }
  }

  async function handleDeletePod() {
    if (!deleteTarget) return;
    setDeleteTarget(null);
    const r = await prodDeletePod(deleteTarget.ns, deleteTarget.pod);
    if (r.ok) { toast(`Deleted ${deleteTarget.pod}`, 'success'); refresh(); }
    else toast(r.error || 'Delete failed', 'error');
  }

  if (loading) return <div className="loading">Loading production workloads…</div>;

  const items = deps?.items || [];
  const allPods = podsData?.items || [];
  const stsList = stsData?.items || [];
  const dsList = dsData?.items || [];

  function podsForDep(dep: K8sDeployment) {
    const labels = dep.spec?.selector?.matchLabels;
    if (!labels) return [];
    return allPods.filter(p => {
      const pl = p.metadata.labels || {};
      return Object.entries(labels).every(([k, v]) => pl[k] === v);
    });
  }

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Production Workloads</h1>
          <p className="page-subtitle">
            {items.length} deployments · {stsList.length} statefulsets · {dsList.length} daemonsets
          </p>
        </div>
      </div>

      <div className="prod-filter-bar">
        <div className="prod-filter-group">
          <button className={`prod-filter-btn ${tab === 'deployments' ? 'active' : ''}`} onClick={() => { setTab('deployments'); setSelected(null); }}>
            Deployments {items.length > 0 && <span className="badge">{items.length}</span>}
          </button>
          <button className={`prod-filter-btn ${tab === 'statefulsets' ? 'active' : ''}`} onClick={() => { setTab('statefulsets'); setSelected(null); }}>
            StatefulSets {stsList.length > 0 && <span className="badge">{stsList.length}</span>}
          </button>
          <button className={`prod-filter-btn ${tab === 'daemonsets' ? 'active' : ''}`} onClick={() => { setTab('daemonsets'); setSelected(null); }}>
            DaemonSets {dsList.length > 0 && <span className="badge">{dsList.length}</span>}
          </button>
        </div>
      </div>

      {/* Modals */}
      {restartTarget && (
        <ConfirmDialog title="Restart Deployment" message={`Rolling restart ${restartTarget.name} in production?`}
          confirmLabel="Restart" onConfirm={handleRestart} onCancel={() => setRestartTarget(null)} danger />
      )}

      {scaleTarget && (
        <ActionModal title={`Scale ${scaleTarget.name}`} submitLabel="Scale" onSubmit={handleScale} onClose={() => setScaleTarget(null)}>
          <label className="form-label">Replicas (current: {scaleTarget.current})</label>
          <input className="form-input" type="number" min={0} max={50} value={scaleCount}
            onChange={e => setScaleCount(Number(e.target.value))} />
        </ActionModal>
      )}

      {rollbackTarget && (
        <ActionModal title={`Rollback ${rollbackTarget.name}`} submitLabel="Rollback" onSubmit={handleRollback} onClose={() => setRollbackTarget(null)}>
          {rollbackLoading ? <p className="text-dim">Loading history…</p> : (
            <>
              <label className="form-label">Select revision (0 = previous)</label>
              <select className="form-input" value={rollbackRev} onChange={e => setRollbackRev(Number(e.target.value))}>
                <option value={0}>Previous revision</option>
                {rollbackHistory.map(r => (
                  <option key={r.revision} value={Number(r.revision)}>
                    Rev {r.revision}{r.change_cause ? ` — ${r.change_cause}` : ''}
                  </option>
                ))}
              </select>
              <p className="text-dim" style={{ fontSize: 12, marginTop: 8 }}>
                ⚠ This will roll back the deployment in production. Make sure you know what changed.
              </p>
            </>
          )}
        </ActionModal>
      )}

      {execTarget && (
        <div className="modal-overlay" onClick={() => { setExecTarget(null); setExecOutput(''); setExecCmd(''); }}>
          <div className="modal modal-wide" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Exec — {execTarget.pod}</h3>
              <button className="panel-close" onClick={() => { setExecTarget(null); setExecOutput(''); setExecCmd(''); }}>✕</button>
            </div>
            <div className="modal-body">
              <div style={{ display: 'flex', gap: 8 }}>
                <input className="form-input" style={{ flex: 1, fontFamily: 'var(--font-mono)' }}
                  placeholder="ls -la /app" value={execCmd} onChange={e => setExecCmd(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') handleExec(); }}
                />
                <button className="btn btn-primary" disabled={execRunning || !execCmd} onClick={handleExec}>
                  {execRunning ? '…' : 'Run'}
                </button>
              </div>
              {execOutput && (
                <pre className="log-output" style={{ marginTop: 12, maxHeight: 400, overflow: 'auto' }}>{execOutput}</pre>
              )}
            </div>
          </div>
        </div>
      )}

      {logsTarget && (
        <div className="modal-overlay" onClick={() => setLogsTarget(null)}>
          <div className="modal modal-wide" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Logs — {logsTarget.pod}</h3>
              <button className="panel-close" onClick={() => setLogsTarget(null)}>✕</button>
            </div>
            <div className="modal-body">
              <pre className="log-output" style={{ maxHeight: 500, overflow: 'auto' }}>{logs}</pre>
            </div>
            <div className="modal-footer">
              <button className="btn" onClick={() => setLogsTarget(null)}>Close</button>
            </div>
          </div>
        </div>
      )}

      {deleteTarget && (
        <ConfirmDialog title="Delete Pod" message={`Delete pod ${deleteTarget.pod}? It will be recreated by the deployment.`}
          confirmLabel="Delete" onConfirm={handleDeletePod} onCancel={() => setDeleteTarget(null)} danger />
      )}

      {/* Deployments table */}
      {tab === 'deployments' && (<>
      {items.length === 0 ? (
        <EmptyState icon="□" message="No deployments found in production cluster." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Deployment</th>
                <th>Namespace</th>
                <th>Ready</th>
                <th>Image</th>
                <th>Strategy</th>
                <th>Age</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map(d => {
                const ns = d.metadata.namespace || 'default';
                const ready = (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1);
                const image = d.spec?.template?.spec?.containers?.[0]?.image ?? '—';
                return (
                  <tr key={`${ns}/${d.metadata.name}`} className="clickable-row" onClick={() => setSelected(selected?.metadata.name === d.metadata.name ? null : d)}>
                    <td className="mono" style={{ fontWeight: 550 }}>{d.metadata.name}</td>
                    <td><span className="tag">{ns}</span></td>
                    <td><StatusBadge ok={ready} label={`${d.status?.readyReplicas ?? 0}/${d.spec?.replicas ?? 1}`} warn={!ready && (d.status?.readyReplicas ?? 0) > 0} /></td>
                    <td className="mono truncate" title={image}>{image.split('/').pop()}</td>
                    <td><span className="tag tag-purple">{d.spec?.strategy?.type || 'RollingUpdate'}</span></td>
                    <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                    <td className="action-cell" onClick={e => e.stopPropagation()} style={{ textAlign: 'right' }}>
                      <ActionButton icon="↻" label="" onClick={() => setRestartTarget({ ns, name: d.metadata.name })} small ghost />
                      <ActionButton icon="⚖" label="" onClick={() => { setScaleTarget({ ns, name: d.metadata.name, current: d.spec?.replicas ?? 1 }); setScaleCount(d.spec?.replicas ?? 1); }} small ghost />
                      <ActionButton icon="⏪" label="" onClick={() => openRollback(ns, d.metadata.name)} small ghost />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Expanded pod list for selected deployment */}
      {selected && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-header">
            <span className="card-icon">○</span>
            <h3>Pods — {selected.metadata.name}</h3>
            <button className="panel-close" style={{ marginLeft: 'auto' }} onClick={() => setSelected(null)}>✕</button>
          </div>
          <div className="card-body">
            {(() => {
              const depPods = podsForDep(selected);
              if (depPods.length === 0) return <p className="text-dim">No pods found</p>;
              return (
                <div className="table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr><th>Pod</th><th>Status</th><th>Restarts</th><th>Node</th><th>Age</th><th style={{ textAlign: 'right' }}>Actions</th></tr>
                    </thead>
                    <tbody>
                      {depPods.map(p => {
                        const ns = p.metadata.namespace || 'default';
                        const phase = p.status?.phase || 'Unknown';
                        const restarts = p.status?.containerStatuses?.reduce((s, c) => s + c.restartCount, 0) ?? 0;
                        return (
                          <tr key={p.metadata.name}>
                            <td className="mono" style={{ fontSize: 12 }}>{p.metadata.name}</td>
                            <td><StatusBadge ok={phase === 'Running'} label={phase} warn={phase === 'Pending'} /></td>
                            <td>{restarts > 0 ? <span className="text-red">{restarts}</span> : '0'}</td>
                            <td className="mono" style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>{p.spec?.nodeName || '—'}</td>
                            <td><TimeAgo timestamp={p.metadata.creationTimestamp} /></td>
                            <td className="action-cell" style={{ textAlign: 'right' }}>
                              <ActionButton icon="📋" label="" onClick={() => openLogs(ns, p.metadata.name)} small ghost />
                              <ActionButton icon="▸" label="" onClick={() => setExecTarget({ ns, pod: p.metadata.name })} small ghost />
                              <ActionButton icon="✕" label="" onClick={() => setDeleteTarget({ ns, pod: p.metadata.name })} small ghost />
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              );
            })()}
          </div>
        </div>
      )}
      </>)}

      {/* StatefulSets table */}
      {tab === 'statefulsets' && (
        stsList.length === 0 ? (
          <EmptyState icon="◫" message="No StatefulSets found in production cluster." />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>StatefulSet</th>
                  <th>Namespace</th>
                  <th>Ready</th>
                  <th>Service</th>
                  <th>Age</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {stsList.map(s => {
                  const ns = s.metadata.namespace || 'default';
                  const ready = (s.status?.readyReplicas ?? 0) >= (s.spec?.replicas ?? 1);
                  return (
                    <tr key={`${ns}/${s.metadata.name}`}>
                      <td className="mono" style={{ fontWeight: 550 }}>{s.metadata.name}</td>
                      <td><span className="tag">{ns}</span></td>
                      <td><StatusBadge ok={ready} label={`${s.status?.readyReplicas ?? 0}/${s.spec?.replicas ?? 1}`} warn={!ready && (s.status?.readyReplicas ?? 0) > 0} /></td>
                      <td className="mono" style={{ fontSize: 12 }}>{s.spec?.serviceName || '—'}</td>
                      <td><TimeAgo timestamp={s.metadata.creationTimestamp} /></td>
                      <td className="action-cell" style={{ textAlign: 'right' }}>
                        <ActionButton icon="↻" label="" onClick={() => setRestartTarget({ ns, name: s.metadata.name })} small ghost />
                        <ActionButton icon="⚖" label="" onClick={() => { setScaleTarget({ ns, name: s.metadata.name, current: s.spec?.replicas ?? 1 }); setScaleCount(s.spec?.replicas ?? 1); }} small ghost />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      )}

      {/* DaemonSets table */}
      {tab === 'daemonsets' && (
        dsList.length === 0 ? (
          <EmptyState icon="◈" message="No DaemonSets found in production cluster." />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>DaemonSet</th>
                  <th>Namespace</th>
                  <th>Desired</th>
                  <th>Ready</th>
                  <th>Available</th>
                  <th>Age</th>
                </tr>
              </thead>
              <tbody>
                {dsList.map(d => {
                  const ns = d.metadata.namespace || 'default';
                  const desired = d.status?.desiredNumberScheduled ?? 0;
                  const ready = d.status?.numberReady ?? 0;
                  const allReady = ready >= desired && desired > 0;
                  return (
                    <tr key={`${ns}/${d.metadata.name}`}>
                      <td className="mono" style={{ fontWeight: 550 }}>{d.metadata.name}</td>
                      <td><span className="tag">{ns}</span></td>
                      <td>{desired}</td>
                      <td><StatusBadge ok={allReady} label={`${ready}/${desired}`} warn={!allReady && ready > 0} /></td>
                      <td>{d.status?.numberAvailable ?? 0}</td>
                      <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      )}
    </div>
  );
}

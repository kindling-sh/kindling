import { useState } from 'react';
import { useApi, fetchLogs, apiDelete } from '../api';
import type { K8sList, K8sPod, K8sContainerSpec, K8sContainerStatus } from '../types';
import { StatusBadge, TimeAgo, EmptyState, ConditionsTable } from './shared';
import { ConfirmDialog, useToast } from './actions';

export function PodsPage() {
  const { data, loading, refresh } = useApi<K8sList<K8sPod>>('/api/pods');
  const { toast } = useToast();
  const [selected, setSelected] = useState<K8sPod | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleDeletePod() {
    if (!deleteTarget) return;
    const result = await apiDelete(`/api/pods/${deleteTarget.ns}/${deleteTarget.name}`);
    if (result.ok) {
      toast(`Pod ${deleteTarget.name} deleted`, 'success');
      refresh();
    } else {
      toast(result.error || 'Delete failed', 'error');
    }
    setDeleteTarget(null);
  }

  if (loading) return <div className="loading">Loading pods…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Pods</h1>
          <p className="page-subtitle">Click a pod to inspect its containers, env vars, and logs</p>
        </div>
      </div>

      {deleteTarget && (
        <ConfirmDialog
          title="Delete Pod"
          message={`Delete pod '${deleteTarget.name}'? It will be recreated by its controller.`}
          confirmLabel="Delete"
          danger
          onConfirm={handleDeletePod}
          onCancel={() => setDeleteTarget(null)}
        />
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
                      <button
                        className="btn btn-sm btn-danger"
                        onClick={() => setDeleteTarget({ ns: p.metadata.namespace || 'default', name: p.metadata.name })}
                      >
                        ✕
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {selected && (
        <PodPanel pod={selected} onClose={() => setSelected(null)} />
      )}
    </div>
  );
}

// ── Pod Detail Panel ─────────────────────────────────────────

function PodPanel({ pod, onClose }: { pod: K8sPod; onClose: () => void }) {
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

  const owner = pod.metadata.ownerReferences?.[0];

  return (
    <>
      <div className="panel-overlay" onClick={onClose} />
      <div className="slide-panel">
        <div className="panel-header">
          <h2>{pod.metadata.name}</h2>
          <button className="panel-close" onClick={onClose}>✕</button>
        </div>

        <div className="panel-breadcrumb">
          {owner && <span className="crumb">{owner.kind}/{owner.name}</span>}
          {owner && <span className="crumb-sep"> › </span>}
          <span className="crumb-active">{pod.metadata.name}</span>
        </div>

        <div className="panel-body">
          {/* Pod Info */}
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
                return <ContainerCard key={c.name} spec={c} status={status} containerState={containerState} onShowLogs={() => showLogs(c.name)} />;
              })}
            </div>
          )}

          {/* Containers */}
          <div className="panel-section">
            <div className="panel-section-title">Containers ({containerSpecs.length})</div>
            {containerSpecs.map((c) => {
              const status = containerStatuses.find(s => s.name === c.name);
              return <ContainerCard key={c.name} spec={c} status={status} containerState={containerState} onShowLogs={() => showLogs(c.name)} />;
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
                      return <tr key={v.name}><td className="mono">{v.name}</td><td className="mono">{type}</td></tr>;
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
                  <button className="btn btn-sm btn-ghost" onClick={() => { setLogTarget(null); setLogs(''); }}>✕</button>
                </div>
                <pre className="log-output">{logLoading ? 'Loading logs…' : (logs || '(no logs)')}</pre>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

// ── Container Card ───────────────────────────────────────────

function ContainerCard({
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

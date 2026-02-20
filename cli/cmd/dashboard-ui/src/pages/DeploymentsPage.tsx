import { useState } from 'react';
import { useApi, apiPost, fetchEnvVars } from '../api';
import type { K8sList, K8sDeployment } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function DeploymentsPage() {
  const { data, loading, refresh } = useApi<K8sList<K8sDeployment>>('/api/deployments');
  const { toast } = useToast();

  // Scale state
  const [scaleTarget, setScaleTarget] = useState<{ ns: string; name: string; current: number } | null>(null);
  const [scaleCount, setScaleCount] = useState(1);

  // Restart confirm
  const [restartTarget, setRestartTarget] = useState<{ ns: string; name: string } | null>(null);

  // Env state
  const [envTarget, setEnvTarget] = useState<{ ns: string; name: string } | null>(null);
  const [envVars, setEnvVars] = useState<{ name: string; value: string }[]>([]);
  const [envLoading, setEnvLoading] = useState(false);
  const [newEnvKey, setNewEnvKey] = useState('');
  const [newEnvVal, setNewEnvVal] = useState('');

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

  async function openEnv(ns: string, name: string) {
    setEnvTarget({ ns, name });
    setEnvLoading(true);
    try {
      const vars = await fetchEnvVars(ns, name);
      setEnvVars(vars);
    } catch {
      setEnvVars([]);
    }
    setEnvLoading(false);
  }

  async function addEnvVar() {
    if (!envTarget || !newEnvKey) return;
    const result = await apiPost('/api/env/set', {
      deployment: envTarget.name,
      namespace: envTarget.ns,
      env: { [newEnvKey]: newEnvVal },
    });
    if (result.ok) {
      toast(`Set ${newEnvKey}`, 'success');
      setNewEnvKey('');
      setNewEnvVal('');
      const vars = await fetchEnvVars(envTarget.ns, envTarget.name);
      setEnvVars(vars);
    } else {
      toast(result.error || 'Failed', 'error');
    }
  }

  async function removeEnvVar(key: string) {
    if (!envTarget) return;
    const result = await apiPost('/api/env/unset', {
      deployment: envTarget.name,
      namespace: envTarget.ns,
      keys: [key],
    });
    if (result.ok) {
      toast(`Removed ${key}`, 'success');
      const vars = await fetchEnvVars(envTarget.ns, envTarget.name);
      setEnvVars(vars);
    } else {
      toast(result.error || 'Failed', 'error');
    }
  }

  if (loading) return <div className="loading">Loading deployments…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Deployments</h1>

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

      {envTarget && (
        <ActionModal
          title={`Env: ${envTarget.name}`}
          submitLabel="Done"
          onSubmit={() => { setEnvTarget(null); refresh(); }}
          onClose={() => { setEnvTarget(null); refresh(); }}
        >
          {envLoading ? (
            <p>Loading…</p>
          ) : (
            <>
              {envVars.length > 0 && (
                <table className="data-table">
                  <thead><tr><th>Key</th><th>Value</th><th></th></tr></thead>
                  <tbody>
                    {envVars.map((v) => (
                      <tr key={v.name}>
                        <td className="mono">{v.name}</td>
                        <td className="mono truncate">{v.value || '(ref)'}</td>
                        <td><ActionButton icon="✕" label="" onClick={() => removeEnvVar(v.name)} danger small /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <div className="form-row">
                <input className="form-input" placeholder="KEY" value={newEnvKey}
                  onChange={(e) => setNewEnvKey(e.target.value)} />
                <input className="form-input" placeholder="value" value={newEnvVal}
                  onChange={(e) => setNewEnvVal(e.target.value)} />
                <ActionButton icon="➕" label="Add" onClick={addEnvVar} small />
              </div>
            </>
          )}
        </ActionModal>
      )}

      {items.length === 0 ? (
        <EmptyState message="No deployments found." />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Namespace</th>
              <th>Replicas</th>
              <th>Image</th>
              <th>Status</th>
              <th>Created</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {items.map((d) => {
              const ready = (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1);
              const image = d.spec?.template?.spec?.containers?.[0]?.image ?? '—';
              const ns = d.metadata.namespace || 'default';
              return (
                <tr key={`${ns}/${d.metadata.name}`}>
                  <td className="mono">{d.metadata.name}</td>
                  <td>{ns}</td>
                  <td>{d.status?.readyReplicas ?? 0} / {d.spec?.replicas ?? 1}</td>
                  <td className="mono truncate" title={image}>{image}</td>
                  <td><StatusBadge ok={ready} label={ready ? 'Ready' : 'Progressing'} /></td>
                  <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                  <td className="action-cell">
                    <ActionButton icon="🔄" label="Restart" onClick={() => setRestartTarget({ ns, name: d.metadata.name })} small />
                    <ActionButton icon="⚖️" label="Scale" onClick={() => { setScaleTarget({ ns, name: d.metadata.name, current: d.spec?.replicas ?? 1 }); setScaleCount(d.spec?.replicas ?? 1); }} small />
                    <ActionButton icon="🔧" label="Env" onClick={() => openEnv(ns, d.metadata.name)} small />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

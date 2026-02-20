import { useState } from 'react';
import { useApi, apiPost, apiDelete } from '../api';
import type { K8sList, DSE } from '../types';
import { StatusBadge, ConditionsTable, EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

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

  if (loading) return <div className="loading">Loading DSEs…</div>;

  const dses = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <h1>Dev Staging Environments</h1>
        <div className="page-actions">
          <ActionButton icon="➕" label="Deploy" onClick={() => setShowDeploy(true)} />
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
            rows={12}
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
        <EmptyState message="No DevStagingEnvironments found. Deploy one with: kindling deploy -f <file.yaml>" />
      ) : (
        <div className="dse-list">
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
  const allReady = s?.deploymentReady && s?.serviceReady &&
    (s?.ingressReady || !dse.spec.ingress?.enabled) &&
    (s?.dependenciesReady || !dse.spec.dependencies?.length);

  return (
    <div className="card card-wide">
      <div className="card-header">
        <span className="card-icon">🚀</span>
        <h3>{dse.metadata.name}</h3>
        <StatusBadge ok={!!allReady} label={allReady ? 'Ready' : 'Not Ready'} />
      </div>
      <div className="card-body">
        <div className="card-grid">
          <div>
            <h4>Deployment</h4>
            <div className="stat-row"><span className="label">Image</span><span className="value mono">{dse.spec.deployment.image}</span></div>
            <div className="stat-row"><span className="label">Port</span><span className="value">{dse.spec.deployment.port}</span></div>
            <div className="stat-row"><span className="label">Replicas</span><span className="value">{s?.availableReplicas ?? 0} / {dse.spec.deployment.replicas ?? 1}</span></div>
            <div className="stat-row"><span className="label">Health Check</span><span className="value mono">{dse.spec.deployment.healthCheck?.path || '—'}</span></div>
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

          {dse.spec.dependencies && dse.spec.dependencies.length > 0 && (
            <div>
              <h4>Dependencies</h4>
              {dse.spec.dependencies.map((dep, i) => (
                <div key={i} className="stat-row">
                  <span className="label">{dep.type}{dep.version ? `:${dep.version}` : ''}</span>
                  <span className="value mono">{dep.envVarName || '—'} → :{dep.port || 'default'}</span>
                </div>
              ))}
              <div className="stat-row">
                <span className="label">Status</span>
                <StatusBadge ok={!!s?.dependenciesReady} label={s?.dependenciesReady ? 'Ready' : 'Pending'} />
              </div>
            </div>
          )}
        </div>

        {dse.spec.deployment.env && dse.spec.deployment.env.length > 0 && (
          <details className="env-details">
            <summary>Environment Variables ({dse.spec.deployment.env.length})</summary>
            <table className="mini-table">
              <thead><tr><th>Name</th><th>Value</th></tr></thead>
              <tbody>
                {dse.spec.deployment.env.map((e, i) => (
                  <tr key={i}><td className="mono">{e.name}</td><td className="mono text-dim">{e.value || '(from secret)'}</td></tr>
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
        <ActionButton icon="🗑" label="Delete" onClick={() => onDelete(dse.metadata.namespace || 'default', dse.metadata.name)} danger small />
      </div>
    </div>
  );
}

import { useState } from 'react';
import { useApi, apiPost } from '../api';
import type { K8sList, K8sSecret } from '../types';
import { EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function SecretsPage() {
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
    if (result.ok) {
      toast('Secret created', 'success');
      setShowCreate(false);
      setForm({ name: '', namespace: 'default', key: '', value: '' });
      refresh();
    } else {
      toast(result.error || 'Failed to create secret', 'error');
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const result = await apiPost('/api/secrets/delete', {
      name: deleteTarget.metadata.name,
      namespace: deleteTarget.metadata.namespace,
    });
    setDeleteTarget(null);
    if (result.ok) {
      toast('Secret deleted', 'success');
      refresh();
    } else {
      toast(result.error || 'Failed to delete secret', 'error');
    }
  }

  if (loading) return <div className="loading">Loading secrets…</div>;

  const secrets = (data?.items || []).filter(
    (s) => s.metadata.namespace !== 'kube-system' && s.metadata.namespace !== 'local-path-storage'
  );

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Secrets</h1>
          <p className="page-subtitle">Managed secrets in the cluster</p>
        </div>
        <div className="page-actions">
          <ActionButton icon="+" label="Create Secret" onClick={() => setShowCreate(true)} primary />
        </div>
      </div>

      {showCreate && (
        <ActionModal
          title="Create Secret"
          submitLabel="Create"
          loading={creating}
          onSubmit={handleCreate}
          onClose={() => setShowCreate(false)}
        >
          <label className="form-label">Name</label>
          <input className="form-input" placeholder="my-secret" value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <label className="form-label">Namespace</label>
          <input className="form-input" placeholder="default" value={form.namespace}
            onChange={(e) => setForm({ ...form, namespace: e.target.value })} />
          <label className="form-label">Key</label>
          <input className="form-input" placeholder="SECRET_KEY" value={form.key}
            onChange={(e) => setForm({ ...form, key: e.target.value })} />
          <label className="form-label">Value</label>
          <input className="form-input" type="password" placeholder="secret value" value={form.value}
            onChange={(e) => setForm({ ...form, value: e.target.value })} />
        </ActionModal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete Secret"
          message={`Delete secret "${deleteTarget.metadata.name}" from namespace "${deleteTarget.metadata.namespace}"?`}
          confirmLabel="Delete"
          danger
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {secrets.length === 0 ? (
        <EmptyState icon="⊕" message="No secrets found. Create one to get started." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Type</th>
                <th>Keys</th>
                <th>Age</th>
                <th style={{ width: 50 }}></th>
              </tr>
            </thead>
            <tbody>
              {secrets.map((sec) => {
                const keys = sec.data ? Object.keys(sec.data) : [];
                return (
                  <tr key={`${sec.metadata.namespace}/${sec.metadata.name}`}>
                    <td className="mono">{sec.metadata.name}</td>
                    <td><span className="tag">{sec.metadata.namespace}</span></td>
                    <td className="mono" style={{ fontSize: '0.8em' }}>{sec.type || 'Opaque'}</td>
                    <td>
                      {keys.length > 0 ? (
                        <span>{keys.length} key{keys.length !== 1 ? 's' : ''}</span>
                      ) : (
                        <span className="text-dim">empty</span>
                      )}
                    </td>
                    <td><TimeAgo timestamp={sec.metadata.creationTimestamp} /></td>
                    <td>
                      <ActionButton icon="✕" label="" onClick={() => setDeleteTarget(sec)} danger ghost />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

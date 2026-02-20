import { useState } from 'react';
import { useApi, apiPost, apiDelete } from '../api';
import type { K8sList, K8sSecret } from '../types';
import { TimeAgo, EmptyState } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function SecretsPage() {
  const { data, loading, refresh } = useApi<K8sList<K8sSecret>>('/api/secrets');
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: '', value: '' });
  const [deleteTarget, setDeleteTarget] = useState<{ ns: string; name: string } | null>(null);

  async function handleCreate() {
    setCreating(true);
    const result = await apiPost('/api/secrets/create', { name: form.name, value: form.value });
    setCreating(false);
    if (result.ok) {
      toast(`Secret '${form.name}' created`, 'success');
      setShowCreate(false);
      setForm({ name: '', value: '' });
      refresh();
    } else {
      toast(result.error || 'Failed to create secret', 'error');
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const result = await apiDelete(`/api/secrets/${deleteTarget.ns}/${deleteTarget.name}`);
    if (result.ok) {
      toast(`Secret '${deleteTarget.name}' deleted`, 'success');
      refresh();
    } else {
      toast(result.error || 'Delete failed', 'error');
    }
    setDeleteTarget(null);
  }

  if (loading) return <div className="loading">Loading secrets…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <h1>Secrets</h1>
        <div className="page-actions">
          <ActionButton icon="➕" label="Create Secret" onClick={() => setShowCreate(true)} />
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
          <label className="form-label">Secret Name</label>
          <input className="form-input" placeholder="MY_API_KEY" value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <label className="form-label">Value</label>
          <input className="form-input" type="password" placeholder="sk_live_..." value={form.value}
            onChange={(e) => setForm({ ...form, value: e.target.value })} />
        </ActionModal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete Secret"
          message={`Delete secret '${deleteTarget.name}'?`}
          confirmLabel="Delete"
          danger
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {items.length === 0 ? (
        <EmptyState message="No kindling-managed secrets found." />
      ) : (
        <div className="dse-list">
          {items.map((sec) => {
            const keys = sec.data ? Object.keys(sec.data) : [];
            return (
              <div className="card card-wide" key={`${sec.metadata.namespace}/${sec.metadata.name}`}>
                <div className="card-header">
                  <span className="card-icon">🔑</span>
                  <h3>{sec.metadata.name}</h3>
                  <span className="badge badge-neutral">{sec.type ?? 'Opaque'}</span>
                </div>
                <div className="card-body">
                  <div className="stat-row">
                    <span className="label">Namespace</span>
                    <span className="value">{sec.metadata.namespace}</span>
                  </div>
                  {keys.length > 0 ? (
                    <div className="secret-keys">
                      <h4>Keys ({keys.length})</h4>
                      <table className="data-table">
                        <thead>
                          <tr><th>Key</th><th>Value</th></tr>
                        </thead>
                        <tbody>
                          {keys.map((k) => (
                            <tr key={k}>
                              <td className="mono">{k}</td>
                              <td className="mono redacted">{sec.data![k]}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  ) : (
                    <p className="muted">No data keys</p>
                  )}
                </div>
                <div className="card-footer">
                  <TimeAgo timestamp={sec.metadata.creationTimestamp} />
                  <ActionButton icon="🗑" label="Delete" onClick={() => setDeleteTarget({ ns: sec.metadata.namespace || 'default', name: sec.metadata.name })} danger small />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

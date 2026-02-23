import { useState } from 'react';
import { useApi, apiPost } from '../api';
import type { K8sList, RunnerPool } from '../types';
import { StatusBadge, ConditionsTable, EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function RunnersPage() {
  const { data, loading, refresh } = useApi<K8sList<RunnerPool>>('/api/runners');
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [showReset, setShowReset] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ username: '', repo: '', token: '' });

  async function handleCreate() {
    setCreating(true);
    const result = await apiPost('/api/runners/create', form);
    setCreating(false);
    if (result.ok) {
      toast('Runner pool created', 'success');
      setShowCreate(false);
      setForm({ username: '', repo: '', token: '' });
      refresh();
    } else {
      toast(result.error || 'Failed to create runner', 'error');
    }
  }

  async function handleReset() {
    setShowReset(false);
    const result = await apiPost('/api/reset-runners');
    if (result.ok) {
      toast('Runners reset', 'success');
      refresh();
    } else {
      toast(result.error || 'Reset failed', 'error');
    }
  }

  if (loading) return <div className="loading">Loading runner pools…</div>;

  const pools = data?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>GitHub Actions Runners</h1>
          <p className="page-subtitle">Self-hosted runner pools in the cluster</p>
        </div>
        <div className="page-actions">
          <ActionButton icon="+" label="Create Runner" onClick={() => setShowCreate(true)} primary />
          {pools.length > 0 && (
            <ActionButton icon="↻" label="Reset All" onClick={() => setShowReset(true)} danger />
          )}
        </div>
      </div>

      {showCreate && (
        <ActionModal
          title="Create Runner Pool"
          submitLabel="Create"
          loading={creating}
          onSubmit={handleCreate}
          onClose={() => setShowCreate(false)}
        >
          <label className="form-label">GitHub Username</label>
          <input className="form-input" placeholder="your-username" value={form.username}
            onChange={(e) => setForm({ ...form, username: e.target.value })} />
          <label className="form-label">Repository (owner/repo)</label>
          <input className="form-input" placeholder="owner/repo" value={form.repo}
            onChange={(e) => setForm({ ...form, repo: e.target.value })} />
          <label className="form-label">GitHub PAT</label>
          <input className="form-input" type="password" placeholder="ghp_..." value={form.token}
            onChange={(e) => setForm({ ...form, token: e.target.value })} />
        </ActionModal>
      )}

      {showReset && (
        <ConfirmDialog
          title="Reset Runner Pools"
          message="This will delete all runner pools and the GitHub token secret. Runners will be de-registered."
          confirmLabel="Reset"
          danger
          onConfirm={handleReset}
          onCancel={() => setShowReset(false)}
        />
      )}

      {pools.length === 0 ? (
        <EmptyState icon="▶" message="No runner pools configured. Create one to get started." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {pools.map((pool) => (
            <RunnerCard key={pool.metadata.name} pool={pool} />
          ))}
        </div>
      )}
    </div>
  );
}

function RunnerCard({ pool }: { pool: RunnerPool }) {
  const s = pool.status;
  const ready = (s?.readyRunners ?? 0) >= (pool.spec.replicas ?? 1);

  return (
    <div className="card card-wide">
      <div className="card-header">
        <span className="card-icon">▶</span>
        <h3>{pool.metadata.name}</h3>
        <StatusBadge ok={ready} label={ready ? 'Ready' : 'Pending'} />
      </div>
      <div className="card-body">
        <div className="card-body-grid">
          <div>
            <h4>Configuration</h4>
            <div className="stat-row"><span className="label">User</span><span className="value">{pool.spec.githubUsername}</span></div>
            <div className="stat-row"><span className="label">Repo</span><span className="value mono">{pool.spec.repository}</span></div>
            <div className="stat-row"><span className="label">Replicas</span><span className="value">{s?.readyRunners ?? 0} / {pool.spec.replicas ?? 1}</span></div>
            {pool.spec.labels && pool.spec.labels.length > 0 && (
              <div className="stat-row"><span className="label">Labels</span><span className="value">{pool.spec.labels.map(l => <span key={l} className="tag" style={{ marginLeft: 2 }}>{l}</span>)}</span></div>
            )}
          </div>
          <div>
            <h4>Status</h4>
            <div className="stat-row">
              <span className="label">Registered</span>
              <StatusBadge ok={!!s?.runnerRegistered} label={s?.runnerRegistered ? 'Yes' : 'No'} />
            </div>
            <div className="stat-row">
              <span className="label">Active Job</span>
              <span className="value mono">{s?.activeJob || '—'}</span>
            </div>
            {s?.lastJobCompleted && (
              <div className="stat-row">
                <span className="label">Last Job</span>
                <TimeAgo timestamp={s.lastJobCompleted} />
              </div>
            )}
            {s?.devEnvironmentRef && (
              <div className="stat-row">
                <span className="label">DSE</span>
                <span className="value mono">{s.devEnvironmentRef}</span>
              </div>
            )}
          </div>
        </div>

        {s?.conditions && (
          <details>
            <summary>Conditions ({s.conditions.length})</summary>
            <ConditionsTable conditions={s.conditions} />
          </details>
        )}
      </div>
      <div className="card-footer">
        <TimeAgo timestamp={pool.metadata.creationTimestamp} />
      </div>
    </div>
  );
}

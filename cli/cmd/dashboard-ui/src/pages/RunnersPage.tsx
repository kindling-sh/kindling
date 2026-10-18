import { useState } from 'react';
import { useApi, apiPost } from '../api';
import type { K8sList, RunnerPool } from '../types';
import { StatusBadge, ConditionsTable, EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

interface BuildAgentEnvRow {
  name: string;
  secretName: string;
  secretKey: string;
}

const emptyEnvRow: BuildAgentEnvRow = { name: '', secretName: '', secretKey: '' };

export function RunnersPage() {
  const { data, loading, refresh } = useApi<K8sList<RunnerPool>>('/api/runners');
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [showReset, setShowReset] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ username: '', repo: '', token: '' });
  const [enableSnapshotDeploy, setEnableSnapshotDeploy] = useState(false);
  const [buildAgentImage, setBuildAgentImage] = useState('');
  const [buildAgentEnv, setBuildAgentEnv] = useState<BuildAgentEnvRow[]>([]);

  function resetForm() {
    setForm({ username: '', repo: '', token: '' });
    setEnableSnapshotDeploy(false);
    setBuildAgentImage('');
    setBuildAgentEnv([]);
  }

  function updateEnvRow(i: number, patch: Partial<BuildAgentEnvRow>) {
    setBuildAgentEnv((rows) => rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }

  function removeEnvRow(i: number) {
    setBuildAgentEnv((rows) => rows.filter((_, idx) => idx !== i));
  }

  async function handleCreate() {
    setCreating(true);
    const result = await apiPost('/api/runners/create', {
      ...form,
      enableSnapshotDeploy,
      buildAgentImage: buildAgentImage || undefined,
      buildAgentEnv: buildAgentEnv
        .filter((r) => r.name || r.secretName || r.secretKey)
        .map((r) => ({ name: r.name, secretName: r.secretName, secretKey: r.secretKey })),
    });
    setCreating(false);
    if (result.ok) {
      toast('Runner pool created', 'success');
      setShowCreate(false);
      resetForm();
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
          docsUrl="https://kindling.sh/docs/runners"
          onSubmit={handleCreate}
          onClose={() => { setShowCreate(false); resetForm(); }}
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

          <details style={{ marginTop: 16 }}>
            <summary style={{ cursor: 'pointer', userSelect: 'none', fontSize: 13, fontWeight: 600 }}>
              Advanced — staging deploy
              <span className="text-dim" style={{ marginLeft: 8, fontSize: 11, fontWeight: 400 }}>
                Enable `kindling snapshot --deploy` from this runner
              </span>
            </summary>

            <label className="form-checkbox" style={{ marginTop: 12 }}>
              <input type="checkbox" checked={enableSnapshotDeploy}
                onChange={(e) => setEnableSnapshotDeploy(e.target.checked)} />
              Enable staging deploy from this runner (`enableSnapshotDeploy`)
            </label>

            <label className="form-label" style={{ marginTop: 12 }}>Build-agent image override (optional)</label>
            <input className="form-input" placeholder="ghcr.io/kindling-sh/build-agent:latest" value={buildAgentImage}
              onChange={(e) => setBuildAgentImage(e.target.value)} />

            <label className="form-label" style={{ marginTop: 12 }}>
              Build-agent environment variables (`buildAgentEnv`)
            </label>
            <p className="text-dim" style={{ fontSize: 11, marginTop: -4, marginBottom: 8 }}>
              Env vars injected into the build-agent sidecar from existing Secrets — e.g.
              registry credentials for an authenticated push. Each references a Secret
              already in the cluster; no raw credential values are entered here.
            </p>
            {buildAgentEnv.map((row, i) => (
              <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6, alignItems: 'center' }}>
                <input className="form-input" style={{ flex: 1 }} placeholder="ENV_VAR_NAME"
                  value={row.name} onChange={(e) => updateEnvRow(i, { name: e.target.value })} />
                <input className="form-input" style={{ flex: 1 }} placeholder="secret-name"
                  value={row.secretName} onChange={(e) => updateEnvRow(i, { secretName: e.target.value })} />
                <input className="form-input" style={{ flex: 1 }} placeholder="key"
                  value={row.secretKey} onChange={(e) => updateEnvRow(i, { secretKey: e.target.value })} />
                <button className="btn btn-xs btn-ghost" onClick={() => removeEnvRow(i)} title="Remove">✕</button>
              </div>
            ))}
            <button className="btn btn-sm btn-ghost" onClick={() => setBuildAgentEnv((rows) => [...rows, { ...emptyEnvRow }])}>
              + Add env var
            </button>
          </details>
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
            <div className="stat-row">
              <span className="label">Staging Deploy</span>
              <StatusBadge ok={!!pool.spec.enableSnapshotDeploy} label={pool.spec.enableSnapshotDeploy ? 'Enabled' : 'Disabled'} />
            </div>
            {pool.spec.buildAgentImage && (
              <div className="stat-row"><span className="label">Build-Agent Image</span><span className="value mono">{pool.spec.buildAgentImage}</span></div>
            )}
            {pool.spec.buildAgentEnv && pool.spec.buildAgentEnv.length > 0 && (
              <div className="stat-row">
                <span className="label">Build-Agent Env</span>
                <span className="value">{pool.spec.buildAgentEnv.map(e => e.name).join(', ')}</span>
              </div>
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

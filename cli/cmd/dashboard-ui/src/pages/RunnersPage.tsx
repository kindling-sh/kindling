import { useApi } from '../api';
import type { K8sList, RunnerPool } from '../types';
import { StatusBadge, ConditionsTable, EmptyState, TimeAgo } from './shared';

export function RunnersPage() {
  const { data, loading } = useApi<K8sList<RunnerPool>>('/api/runners');

  if (loading) return <div className="loading">Loading runner pools…</div>;

  const pools = data?.items || [];

  return (
    <div className="page">
      <h1>GitHub Actions Runner Pools</h1>

      {pools.length === 0 ? (
        <EmptyState message="No runner pools configured. Create one with: kindling runners" />
      ) : (
        <div className="dse-list">
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
        <span className="card-icon">🏃</span>
        <h3>{pool.metadata.name}</h3>
        <StatusBadge ok={ready} label={ready ? 'Ready' : 'Pending'} />
      </div>
      <div className="card-body">
        <div className="card-grid">
          <div>
            <h4>Config</h4>
            <div className="stat-row"><span className="label">User</span><span className="value">{pool.spec.githubUsername}</span></div>
            <div className="stat-row"><span className="label">Repo</span><span className="value mono">{pool.spec.repository}</span></div>
            <div className="stat-row"><span className="label">Replicas</span><span className="value">{s?.readyRunners ?? 0} / {pool.spec.replicas ?? 1}</span></div>
            {pool.spec.labels && pool.spec.labels.length > 0 && (
              <div className="stat-row"><span className="label">Labels</span><span className="value mono">{pool.spec.labels.join(', ')}</span></div>
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
                <span className="label">Last DSE</span>
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

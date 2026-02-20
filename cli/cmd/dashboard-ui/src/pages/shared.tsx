import type { K8sDeployment, K8sCondition } from '../types';

export function StatusBadge({ ok, label, warn }: { ok: boolean; label: string; warn?: boolean }) {
  const cls = ok ? 'badge badge-ok' : warn ? 'badge badge-warn' : 'badge badge-err';
  return <span className={cls}>{label}</span>;
}

export function DeploymentStatus({ dep }: { dep: K8sDeployment | any }) {
  const ready = dep?.status?.readyReplicas ?? 0;
  const desired = dep?.spec?.replicas ?? dep?.status?.replicas ?? 1;
  const ok = ready >= desired && ready > 0;
  return (
    <>
      <div className="stat-row">
        <span className="label">Replicas</span>
        <span className="value">{ready} / {desired}</span>
      </div>
      <div className="stat-row">
        <span className="label">Status</span>
        <StatusBadge ok={ok} label={ok ? 'Healthy' : 'Degraded'} warn={ready > 0 && ready < desired} />
      </div>
    </>
  );
}

export function TimeAgo({ timestamp }: { timestamp?: string }) {
  if (!timestamp) return <span className="text-dim">—</span>;
  const diff = Date.now() - new Date(timestamp).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return <span>just now</span>;
  if (mins < 60) return <span>{mins}m ago</span>;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return <span>{hours}h ago</span>;
  const days = Math.floor(hours / 24);
  return <span>{days}d ago</span>;
}

export function ConditionsTable({ conditions }: { conditions?: K8sCondition[] }) {
  if (!conditions?.length) return <span className="text-dim">No conditions</span>;
  return (
    <table className="mini-table">
      <thead>
        <tr><th>Type</th><th>Status</th><th>Reason</th><th>Message</th></tr>
      </thead>
      <tbody>
        {conditions.map((c, i) => (
          <tr key={i}>
            <td className="mono">{c.type}</td>
            <td><StatusBadge ok={c.status === 'True'} label={c.status} /></td>
            <td>{c.reason || '—'}</td>
            <td className="text-dim">{c.message?.slice(0, 100) || '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function EmptyState({ message }: { message: string }) {
  return <div className="empty-state">{message}</div>;
}

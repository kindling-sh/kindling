import { useApi } from '../api';
import type { K8sList, K8sDeployment } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';

export function DeploymentsPage() {
  const { data, loading } = useApi<K8sList<K8sDeployment>>('/api/deployments');

  if (loading) return <div className="loading">Loading deployments…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Deployments</h1>

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
            </tr>
          </thead>
          <tbody>
            {items.map((d) => {
              const ready = (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1);
              const image = d.spec?.template?.spec?.containers?.[0]?.image ?? '—';
              return (
                <tr key={`${d.metadata.namespace}/${d.metadata.name}`}>
                  <td className="mono">{d.metadata.name}</td>
                  <td>{d.metadata.namespace}</td>
                  <td>{d.status?.readyReplicas ?? 0} / {d.spec?.replicas ?? 1}</td>
                  <td className="mono truncate" title={image}>{image}</td>
                  <td><StatusBadge ok={ready} label={ready ? 'Ready' : 'Progressing'} /></td>
                  <td><TimeAgo timestamp={d.metadata.creationTimestamp} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

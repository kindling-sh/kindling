import { useApi } from '../api';
import type { K8sList, K8sIngress } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';

export function IngressesPage() {
  const { data, loading } = useApi<K8sList<K8sIngress>>('/api/ingresses');

  if (loading) return <div className="loading">Loading ingresses…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Ingresses</h1>

      {items.length === 0 ? (
        <EmptyState message="No ingresses found." />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Namespace</th>
              <th>Class</th>
              <th>Hosts</th>
              <th>Paths</th>
              <th>Status</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {items.map((ing) => {
              const cls = ing.spec?.ingressClassName ?? '—';
              const rules = ing.spec?.rules ?? [];
              const hosts = rules.map((r) => r.host || '*').join(', ') || '—';
              const paths = rules
                .flatMap((r) =>
                  (r.http?.paths ?? []).map(
                    (p) => `${r.host || '*'}${p.path || '/'}`
                  )
                )
                .join(', ') || '—';
              const hasAddress = (ing.status?.loadBalancer?.ingress ?? []).length > 0;

              return (
                <tr key={`${ing.metadata.namespace}/${ing.metadata.name}`}>
                  <td className="mono">{ing.metadata.name}</td>
                  <td>{ing.metadata.namespace}</td>
                  <td>{cls}</td>
                  <td className="mono">{hosts}</td>
                  <td className="mono truncate" title={paths}>{paths}</td>
                  <td><StatusBadge ok={hasAddress} label={hasAddress ? 'Active' : 'Pending'} /></td>
                  <td><TimeAgo timestamp={ing.metadata.creationTimestamp} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

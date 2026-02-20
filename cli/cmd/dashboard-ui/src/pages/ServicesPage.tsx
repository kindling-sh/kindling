import { useApi } from '../api';
import type { K8sList, K8sService } from '../types';
import { TimeAgo, EmptyState } from './shared';

export function ServicesPage() {
  const { data, loading } = useApi<K8sList<K8sService>>('/api/services');

  if (loading) return <div className="loading">Loading services…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Services</h1>

      {items.length === 0 ? (
        <EmptyState message="No services found." />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Namespace</th>
              <th>Type</th>
              <th>Cluster IP</th>
              <th>Ports</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {items.map((s) => {
              const ports = s.spec?.ports?.map((p) => {
                let txt = `${p.port}`;
                if (p.targetPort) txt += `→${p.targetPort}`;
                if (p.protocol && p.protocol !== 'TCP') txt += `/${p.protocol}`;
                if (p.nodePort) txt += ` (node:${p.nodePort})`;
                return txt;
              }).join(', ') || '—';

              return (
                <tr key={`${s.metadata.namespace}/${s.metadata.name}`}>
                  <td className="mono">{s.metadata.name}</td>
                  <td>{s.metadata.namespace}</td>
                  <td>{s.spec?.type ?? 'ClusterIP'}</td>
                  <td className="mono">{s.spec?.clusterIP ?? '—'}</td>
                  <td className="mono">{ports}</td>
                  <td><TimeAgo timestamp={s.metadata.creationTimestamp} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

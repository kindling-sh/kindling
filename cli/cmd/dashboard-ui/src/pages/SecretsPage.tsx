import { useApi } from '../api';
import type { K8sList, K8sSecret } from '../types';
import { TimeAgo, EmptyState } from './shared';

export function SecretsPage() {
  const { data, loading } = useApi<K8sList<K8sSecret>>('/api/secrets');

  if (loading) return <div className="loading">Loading secrets…</div>;

  const items = data?.items || [];

  return (
    <div className="page">
      <h1>Secrets</h1>

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
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

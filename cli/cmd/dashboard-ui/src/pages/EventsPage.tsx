import { useApi } from '../api';
import type { K8sList, K8sEvent } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';

export function EventsPage() {
  const { data, loading } = useApi<K8sList<K8sEvent>>('/api/events', 10_000);

  if (loading) return <div className="loading">Loading events…</div>;

  const items = (data?.items || []).sort((a, b) => {
    const ta = a.lastTimestamp || a.metadata.creationTimestamp || '';
    const tb = b.lastTimestamp || b.metadata.creationTimestamp || '';
    return tb.localeCompare(ta); // newest first
  });

  return (
    <div className="page">
      <h1>Events</h1>

      {items.length === 0 ? (
        <EmptyState message="No events found." />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Type</th>
              <th>Reason</th>
              <th>Object</th>
              <th>Message</th>
              <th>#</th>
              <th>Last Seen</th>
            </tr>
          </thead>
          <tbody>
            {items.map((e, i) => {
              const ok = e.type === 'Normal';
              const obj = e.involvedObject
                ? `${e.involvedObject.kind}/${e.involvedObject.name}`
                : '—';
              return (
                <tr key={`${e.metadata.name}-${i}`}>
                  <td><StatusBadge ok={ok} label={e.type ?? 'Normal'} /></td>
                  <td className="mono">{e.reason ?? '—'}</td>
                  <td className="mono truncate" title={obj}>{obj}</td>
                  <td className="event-message">{e.message ?? '—'}</td>
                  <td>{e.count ?? 1}</td>
                  <td>
                    <TimeAgo timestamp={e.lastTimestamp || e.metadata.creationTimestamp} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

import { useState } from 'react';
import { useApi } from '../api';
import type { K8sList, K8sEvent, K8sMetadata } from '../types';
import { TimeAgo, EmptyState } from './shared';

export function StagingEventsPage() {
  const [namespace, setNamespace] = useState('');
  const eventsPath = namespace ? `/api/staging/events?namespace=${encodeURIComponent(namespace)}` : '/api/staging/events';
  const { data, loading } = useApi<K8sList<K8sEvent>>(eventsPath);
  const { data: nsData } = useApi<K8sList<{ metadata: K8sMetadata }>>('/api/staging/namespaces');
  const [typeFilter, setTypeFilter] = useState<string>('');
  const [search, setSearch] = useState('');

  if (loading) return <div className="loading">Loading events…</div>;

  const items = (data?.items || []).filter(e => {
    if (typeFilter && e.type !== typeFilter) return false;
    if (search) {
      const q = search.toLowerCase();
      return (
        (e.message || '').toLowerCase().includes(q) ||
        (e.reason || '').toLowerCase().includes(q) ||
        (e.involvedObject?.name || '').toLowerCase().includes(q) ||
        (e.involvedObject?.kind || '').toLowerCase().includes(q)
      );
    }
    return true;
  });

  const warnings = data?.items?.filter(e => e.type === 'Warning').length ?? 0;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Staging Events</h1>
          <p className="page-subtitle">
            {data?.items?.length ?? 0} events
            {warnings > 0 && <span className="text-yellow" style={{ marginLeft: 8 }}>⚠ {warnings} warnings</span>}
          </p>
        </div>
      </div>

      <div className="staging-filter-bar">
        <div className="staging-filter-group">
          <button className={`staging-filter-btn ${typeFilter === '' ? 'active' : ''}`} onClick={() => setTypeFilter('')}>All</button>
          <button className={`staging-filter-btn ${typeFilter === 'Normal' ? 'active' : ''}`} onClick={() => setTypeFilter('Normal')}>Normal</button>
          <button className={`staging-filter-btn ${typeFilter === 'Warning' ? 'active' : ''}`} onClick={() => setTypeFilter('Warning')}>
            Warning {warnings > 0 && <span className="badge">{warnings}</span>}
          </button>
        </div>
        <input className="form-input" style={{ width: 220, fontSize: 12 }} placeholder="Search events…"
          value={search} onChange={e => setSearch(e.target.value)} />
        <select className="form-input" style={{ width: 160, fontSize: 12 }} value={namespace} onChange={e => setNamespace(e.target.value)}>
          <option value="">All namespaces</option>
          {(nsData?.items || []).map(ns => (
            <option key={ns.metadata.name} value={ns.metadata.name}>{ns.metadata.name}</option>
          ))}
        </select>
      </div>

      {items.length === 0 ? (
        <EmptyState icon="◇" message={search || typeFilter ? 'No matching events.' : 'No events in the cluster.'} />
      ) : (
        <div className="staging-event-list">
          {items.map((ev, i) => (
            <div key={ev.metadata.uid || i} className={`staging-event-item ${ev.type === 'Warning' ? 'warning' : ''}`}>
              <span className="staging-event-icon">
                {ev.type === 'Warning' ? '⚠' : '✓'}
              </span>
              <div className="staging-event-body">
                <div className="staging-event-header">
                  <span className="staging-event-reason">{ev.reason}</span>
                  <span className="staging-event-object">
                    {ev.involvedObject?.kind}/{ev.involvedObject?.name}
                  </span>
                  {ev.involvedObject?.namespace && (
                    <span className="staging-event-ns">{ev.involvedObject.namespace}</span>
                  )}
                </div>
                <div className="staging-event-message">{ev.message}</div>
              </div>
              <div className="staging-event-meta">
                {(ev.count ?? 0) > 1 && <span className="badge">×{ev.count}</span>}
                <TimeAgo timestamp={ev.lastTimestamp || ev.metadata.creationTimestamp} />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

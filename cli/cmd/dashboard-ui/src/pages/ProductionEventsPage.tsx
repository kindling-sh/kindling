import { useState } from 'react';
import { useApi } from '../api';
import type { K8sList, K8sEvent } from '../types';
import { TimeAgo, EmptyState } from './shared';

export function ProductionEventsPage() {
  const { data, loading } = useApi<K8sList<K8sEvent>>('/api/prod/events');
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
          <h1>Production Events</h1>
          <p className="page-subtitle">
            {data?.items?.length ?? 0} events
            {warnings > 0 && <span className="text-yellow" style={{ marginLeft: 8 }}>⚠ {warnings} warnings</span>}
          </p>
        </div>
      </div>

      <div className="prod-filter-bar">
        <div className="prod-filter-group">
          <button className={`prod-filter-btn ${typeFilter === '' ? 'active' : ''}`} onClick={() => setTypeFilter('')}>All</button>
          <button className={`prod-filter-btn ${typeFilter === 'Normal' ? 'active' : ''}`} onClick={() => setTypeFilter('Normal')}>Normal</button>
          <button className={`prod-filter-btn prod-filter-warn ${typeFilter === 'Warning' ? 'active' : ''}`} onClick={() => setTypeFilter('Warning')}>
            Warning {warnings > 0 && <span className="prod-filter-count">{warnings}</span>}
          </button>
        </div>
        <input className="form-input" style={{ width: 220, fontSize: 12 }} placeholder="Search events…"
          value={search} onChange={e => setSearch(e.target.value)} />
      </div>

      {items.length === 0 ? (
        <EmptyState icon="⚡" message={search || typeFilter ? 'No matching events.' : 'No events in the cluster.'} />
      ) : (
        <div className="prod-event-list">
          {items.map((ev, i) => (
            <div key={ev.metadata.uid || i} className={`prod-event-item ${ev.type === 'Warning' ? 'prod-event-warn' : ''}`}>
              <div className="prod-event-left">
                <span className={`prod-event-type ${ev.type === 'Warning' ? 'warn' : 'normal'}`}>
                  {ev.type === 'Warning' ? '⚠' : '✓'}
                </span>
                <div className="prod-event-body">
                  <div className="prod-event-reason">
                    <span className="mono" style={{ fontWeight: 600 }}>{ev.reason}</span>
                    <span className="prod-event-object">
                      {ev.involvedObject?.kind}/{ev.involvedObject?.name}
                    </span>
                    {ev.involvedObject?.namespace && (
                      <span className="tag" style={{ fontSize: 10 }}>{ev.involvedObject.namespace}</span>
                    )}
                  </div>
                  <div className="prod-event-message">{ev.message}</div>
                </div>
              </div>
              <div className="prod-event-right">
                {(ev.count ?? 0) > 1 && <span className="prod-event-count">×{ev.count}</span>}
                <TimeAgo timestamp={ev.lastTimestamp || ev.metadata.creationTimestamp} />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

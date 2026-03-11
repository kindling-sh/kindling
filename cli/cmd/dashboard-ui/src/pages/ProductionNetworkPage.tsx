import { useApi, fetchProdIngressController } from '../api';
import { useState, useEffect } from 'react';
import type { K8sList, K8sService, K8sIngress, IngressControllerInfo } from '../types';
import { TimeAgo, EmptyState } from './shared';

export function ProductionNetworkPage() {
  const { data: services } = useApi<K8sList<K8sService>>('/api/prod/services');
  const { data: ingresses } = useApi<K8sList<K8sIngress>>('/api/prod/ingresses');
  const [ic, setIc] = useState<IngressControllerInfo | null>(null);
  const [tab, setTab] = useState<'ingresses' | 'services'>('ingresses');

  useEffect(() => {
    fetchProdIngressController().then(setIc).catch(() => {});
  }, []);

  const svcItems = services?.items || [];
  const ingItems = ingresses?.items || [];

  function goToTLS() {
    window.dispatchEvent(new CustomEvent('navigate', { detail: 'prod-tls' }));
  }

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Network</h1>
          <p className="page-subtitle">Ingress routing and services</p>
        </div>
      </div>

      {/* Ingress Controller */}
      {ic && ic.found && (() => {
        const addr = ic.external_ip || ic.hostname;
        return (
          <div className="card" style={{ marginBottom: 16 }}>
            <div className="card-header">
              <span className="card-icon">⊕</span>
              <h3>Ingress Controller</h3>
              <span className="tag tag-green" style={{ marginLeft: 8, textTransform: 'capitalize' }}>{ic.class}</span>
              <span className="tag" style={{ marginLeft: 4 }}>{ic.type}</span>
            </div>
            <div className="card-body">
              <div className="stat-row">
                <span className="label">Service</span>
                <span className="mono">{ic.namespace}/{ic.name}</span>
              </div>
              <div className="stat-row">
                <span className="label">Public Address</span>
                {addr ? (
                  <span className="mono" style={{ fontWeight: 600, color: 'var(--accent)' }}>{addr}</span>
                ) : (
                  <span className="text-dim">Pending — no external IP assigned yet</span>
                )}
              </div>
              {ic.ports.length > 0 && (
                <div className="stat-row">
                  <span className="label">Ports</span>
                  <span className="mono">
                    {ic.ports.map(p => `${p.name ? p.name + ':' : ''}${p.port}${p.nodePort ? '→' + p.nodePort : ''}`).join(', ')}
                  </span>
                </div>
              )}
              <div className="stat-row">
                <span className="label">Cluster IP</span>
                <span className="mono text-dim">{ic.cluster_ip}</span>
              </div>
            </div>
          </div>
        );
      })()}

      <div className="prod-tabs">
        <button className={`prod-tab ${tab === 'ingresses' ? 'active' : ''}`} onClick={() => setTab('ingresses')}>
          Ingresses <span className="prod-tab-count">{ingItems.length}</span>
        </button>
        <button className={`prod-tab ${tab === 'services' ? 'active' : ''}`} onClick={() => setTab('services')}>
          Services <span className="prod-tab-count">{svcItems.length}</span>
        </button>
      </div>

      {tab === 'ingresses' && (
        ingItems.length === 0 ? <EmptyState icon="⊕" message="No ingresses found." /> : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>Name</th><th>Namespace</th><th>Host</th><th>Path</th><th>Backend</th><th>TLS</th><th>Age</th><th></th></tr>
              </thead>
              <tbody>
                {ingItems.map(ing => {
                  const ns = ing.metadata.namespace || 'default';
                  const rule = ing.spec?.rules?.[0];
                  const path = rule?.http?.paths?.[0];
                  const hasTLS = !!ing.spec?.tls?.length;
                  return (
                    <tr key={`${ns}/${ing.metadata.name}`}>
                      <td className="mono" style={{ fontWeight: 550 }}>{ing.metadata.name}</td>
                      <td><span className="tag">{ns}</span></td>
                      <td className="mono">{rule?.host || '—'}</td>
                      <td className="mono">{path?.path || '/'}</td>
                      <td className="mono">{path?.backend?.service?.name || '—'}:{path?.backend?.service?.port?.number || '—'}</td>
                      <td>
                        {hasTLS ? (
                          <span className="prod-tls-badge prod-tls-ok">◈ TLS</span>
                        ) : (
                          <span className="prod-tls-badge prod-tls-none">—</span>
                        )}
                      </td>
                      <td><TimeAgo timestamp={ing.metadata.creationTimestamp} /></td>
                      <td>
                        {!hasTLS && (
                          <button className="btn btn-secondary" style={{ fontSize: 11, padding: '2px 8px' }} onClick={goToTLS}>Configure TLS</button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      )}

      {tab === 'services' && (
        svcItems.length === 0 ? <EmptyState icon="◎" message="No services found." /> : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>Name</th><th>Namespace</th><th>Type</th><th>Cluster IP</th><th>Ports</th><th>Age</th></tr>
              </thead>
              <tbody>
                {svcItems.map(svc => {
                  const ns = svc.metadata.namespace || 'default';
                  const ports = svc.spec?.ports?.map(p => `${p.port}${p.nodePort ? `→${p.nodePort}` : ''}`).join(', ') || '—';
                  return (
                    <tr key={`${ns}/${svc.metadata.name}`}>
                      <td className="mono" style={{ fontWeight: 550 }}>{svc.metadata.name}</td>
                      <td><span className="tag">{ns}</span></td>
                      <td><span className={`tag ${svc.spec?.type === 'LoadBalancer' ? 'tag-green' : svc.spec?.type === 'NodePort' ? 'tag-purple' : ''}`}>{svc.spec?.type || 'ClusterIP'}</span></td>
                      <td className="mono" style={{ fontSize: 12 }}>{svc.spec?.clusterIP || '—'}</td>
                      <td className="mono" style={{ fontSize: 12 }}>{ports}</td>
                      <td><TimeAgo timestamp={svc.metadata.creationTimestamp} /></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      )}

    </div>
  );
}

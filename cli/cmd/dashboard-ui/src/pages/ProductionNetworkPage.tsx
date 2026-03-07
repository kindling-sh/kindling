import { useApi, fetchProdCertificates, fetchProdClusterIssuers, fetchTLSStatus, streamTLSInstall } from '../api';
import { useState, useEffect } from 'react';
import type { K8sList, K8sService, K8sIngress, CertificateItem, ClusterIssuerItem, TLSStatus } from '../types';
import { StatusBadge, TimeAgo, EmptyState } from './shared';

export function ProductionNetworkPage() {
  const { data: services } = useApi<K8sList<K8sService>>('/api/prod/services');
  const { data: ingresses } = useApi<K8sList<K8sIngress>>('/api/prod/ingresses');

  const [certs, setCerts] = useState<CertificateItem[]>([]);
  const [issuers, setIssuers] = useState<ClusterIssuerItem[]>([]);
  const [tlsStatus, setTlsStatus] = useState<TLSStatus | null>(null);

  // TLS install form
  const [showTLSInstall, setShowTLSInstall] = useState(false);
  const [tlsEmail, setTlsEmail] = useState('');
  const [tlsDomain, setTlsDomain] = useState('');
  const [tlsIssuer, setTlsIssuer] = useState('letsencrypt-prod');
  const [tlsIngressClass, setTlsIngressClass] = useState('traefik');
  const [tlsStaging, setTlsStaging] = useState(false);
  const [tlsInstalling, setTlsInstalling] = useState(false);
  const [tlsLogs, setTlsLogs] = useState<{ type: string; message: string }[]>([]);

  useEffect(() => {
    fetchProdCertificates().then(r => setCerts(r.items || [])).catch(() => {});
    fetchProdClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
    fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
  }, []);

  function doTLSInstall() {
    if (!tlsEmail || !tlsDomain) return;
    setTlsInstalling(true);
    setTlsLogs([]);
    streamTLSInstall(
      { email: tlsEmail, domain: tlsDomain, issuer: tlsIssuer, ingress_class: tlsIngressClass, staging: tlsStaging },
      (msg) => {
        setTlsLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setTlsInstalling(false);
          // Refresh status
          fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
          fetchProdClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
          fetchProdCertificates().then(r => setCerts(r.items || [])).catch(() => {});
        }
      },
    );
  }

  const [tab, setTab] = useState<'services' | 'ingresses' | 'tls'>('ingresses');

  const svcItems = services?.items || [];
  const ingItems = ingresses?.items || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Network & TLS</h1>
          <p className="page-subtitle">Services, ingress routing, and TLS certificates</p>
        </div>
      </div>

      <div className="prod-tabs">
        <button className={`prod-tab ${tab === 'ingresses' ? 'active' : ''}`} onClick={() => setTab('ingresses')}>
          Ingresses <span className="prod-tab-count">{ingItems.length}</span>
        </button>
        <button className={`prod-tab ${tab === 'services' ? 'active' : ''}`} onClick={() => setTab('services')}>
          Services <span className="prod-tab-count">{svcItems.length}</span>
        </button>
        <button className={`prod-tab ${tab === 'tls' ? 'active' : ''}`} onClick={() => setTab('tls')}>
          TLS Certificates <span className="prod-tab-count">{certs.length}</span>
        </button>
      </div>

      {tab === 'ingresses' && (
        ingItems.length === 0 ? <EmptyState icon="⊕" message="No ingresses found." /> : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>Name</th><th>Namespace</th><th>Host</th><th>Path</th><th>Backend</th><th>TLS</th><th>Age</th></tr>
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
                          <span className="prod-tls-badge prod-tls-ok">🔒 TLS</span>
                        ) : (
                          <span className="prod-tls-badge prod-tls-none">—</span>
                        )}
                      </td>
                      <td><TimeAgo timestamp={ing.metadata.creationTimestamp} /></td>
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

      {tab === 'tls' && (
        <div>
          {/* cert-manager status + Install TLS button */}
          <div className="card" style={{ marginBottom: 16 }}>
            <div className="card-header">
              <span className="card-icon">◈</span>
              <h3>TLS Management</h3>
              {!showTLSInstall && (
                <button className="btn btn-primary" style={{ marginLeft: 'auto', fontSize: 12, padding: '4px 12px' }} onClick={() => setShowTLSInstall(true)}>
                  {tlsStatus?.cert_manager ? 'Add ClusterIssuer' : 'Install cert-manager + TLS'}
                </button>
              )}
            </div>
            <div className="card-body">
              <div className="stat-row">
                <span className="label">cert-manager</span>
                <StatusBadge ok={!!tlsStatus?.cert_manager} label={tlsStatus?.cert_manager ? 'Installed' : 'Not Installed'} />
              </div>

              {showTLSInstall && !tlsInstalling && tlsLogs.length === 0 && (
                <div style={{ marginTop: 16, padding: '16px 0', borderTop: '1px solid var(--border)' }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
                    <div className="form-group">
                      <label className="form-label">Email *</label>
                      <input className="form-input" value={tlsEmail} onChange={e => setTlsEmail(e.target.value)} placeholder="admin@example.com" />
                    </div>
                    <div className="form-group">
                      <label className="form-label">Domain *</label>
                      <input className="form-input" value={tlsDomain} onChange={e => setTlsDomain(e.target.value)} placeholder="example.com" />
                    </div>
                    <div className="form-group">
                      <label className="form-label">Issuer Name</label>
                      <input className="form-input" value={tlsIssuer} onChange={e => setTlsIssuer(e.target.value)} placeholder="letsencrypt-prod" />
                    </div>
                    <div className="form-group">
                      <label className="form-label">Ingress Class</label>
                      <input className="form-input" value={tlsIngressClass} onChange={e => setTlsIngressClass(e.target.value)} placeholder="traefik" />
                    </div>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <label className="deploy-ingress-toggle">
                      <input type="checkbox" checked={tlsStaging} onChange={e => setTlsStaging(e.target.checked)} />
                      <span style={{ marginLeft: 4, fontSize: 13 }}>Use staging server</span>
                    </label>
                    <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
                      <button className="btn btn-secondary" onClick={() => setShowTLSInstall(false)}>Cancel</button>
                      <button className="btn btn-primary" disabled={!tlsEmail || !tlsDomain} onClick={doTLSInstall}>Install TLS</button>
                    </div>
                  </div>
                </div>
              )}

              {tlsLogs.length > 0 && (
                <div className="deploy-log" style={{ maxHeight: 200, marginTop: 12 }}>
                  {tlsLogs.map((log, i) => (
                    <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                      <span className="deploy-log-icon">
                        {log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}
                      </span>
                      <span>{log.message}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Cluster Issuers */}
          {issuers.length > 0 && (
            <div className="card" style={{ marginBottom: 16 }}>
              <div className="card-header">
                <span className="card-icon">⊕</span>
                <h3>Cluster Issuers</h3>
              </div>
              <div className="card-body">
                {issuers.map(iss => {
                  const ready = iss.status?.conditions?.some(c => c.type === 'Ready' && c.status === 'True');
                  return (
                    <div key={iss.metadata.name} className="stat-row" style={{ padding: '6px 0' }}>
                      <span className="mono" style={{ fontWeight: 550 }}>{iss.metadata.name}</span>
                      <span className="text-dim mono" style={{ fontSize: 11 }}>{iss.spec?.acme?.server?.replace('https://', '').split('/')[0] || ''}</span>
                      <StatusBadge ok={!!ready} label={ready ? 'Ready' : 'Not Ready'} />
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Certificates */}
          {certs.length === 0 ? (
            <EmptyState icon="◈" message="No TLS certificates found. Install cert-manager and create a ClusterIssuer." />
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr><th>Certificate</th><th>Namespace</th><th>DNS Names</th><th>Issuer</th><th>Status</th><th>Expires</th></tr>
                </thead>
                <tbody>
                  {certs.map(cert => {
                    const ns = cert.metadata.namespace || 'default';
                    const ready = cert.status?.conditions?.some(c => c.type === 'Ready' && c.status === 'True');
                    const dnsNames = cert.spec?.dnsNames?.join(', ') || '—';
                    return (
                      <tr key={`${ns}/${cert.metadata.name}`}>
                        <td className="mono" style={{ fontWeight: 550 }}>{cert.metadata.name}</td>
                        <td><span className="tag">{ns}</span></td>
                        <td className="mono" style={{ fontSize: 12 }}>{dnsNames}</td>
                        <td className="mono">{cert.spec?.issuerRef?.name || '—'}</td>
                        <td>
                          <StatusBadge ok={!!ready} label={ready ? 'Valid' : 'Pending'} warn={!ready} />
                        </td>
                        <td>
                          {cert.status?.notAfter ? (
                            <span className={`mono ${new Date(cert.status.notAfter).getTime() - Date.now() < 7 * 86400000 ? 'text-red' : ''}`}>
                              {new Date(cert.status.notAfter).toLocaleDateString()}
                            </span>
                          ) : '—'}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

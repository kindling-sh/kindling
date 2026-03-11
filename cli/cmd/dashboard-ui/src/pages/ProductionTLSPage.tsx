import { useApi, fetchProdCertificates, fetchProdClusterIssuers, fetchTLSStatus, streamTLSInstall, fetchProdIngressController } from '../api';
import { useState, useEffect, useRef } from 'react';
import type { K8sList, K8sIngress, CertificateItem, ClusterIssuerItem, TLSStatus, IngressControllerInfo } from '../types';
import { StatusBadge, EmptyState } from './shared';

export function ProductionTLSPage() {
  const { data: ingresses } = useApi<K8sList<K8sIngress>>('/api/prod/ingresses');

  const [certs, setCerts] = useState<CertificateItem[]>([]);
  const [issuers, setIssuers] = useState<ClusterIssuerItem[]>([]);
  const [tlsStatus, setTlsStatus] = useState<TLSStatus | null>(null);
  const [icInfo, setIcInfo] = useState<IngressControllerInfo | null>(null);
  const [copied, setCopied] = useState('');

  // Install form
  const [showForm, setShowForm] = useState(false);
  const [email, setEmail] = useState('');
  const [domain, setDomain] = useState('');
  const [issuerName, setIssuerName] = useState('letsencrypt-prod');
  const [ingressClass, setIngressClass] = useState('traefik');
  const [staging, setStaging] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [logs, setLogs] = useState<{ type: string; message: string }[]>([]);
  const [ingressName, setIngressName] = useState('');
  const [ingressNs, setIngressNs] = useState('');
  const abortRef = useRef<(() => void) | null>(null);

  const ingItems = ingresses?.items || [];
  const untlsIngresses = ingItems.filter(ing => !ing.spec?.tls?.length);

  useEffect(() => {
    fetchProdCertificates().then(r => setCerts(r.items || [])).catch(() => {});
    fetchProdClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
    fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
    fetchProdIngressController().then(ic => setIcInfo(ic)).catch(() => {});
    return () => { abortRef.current?.(); };
  }, []);

  function refreshAll() {
    fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
    fetchProdClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
    fetchProdCertificates().then(r => setCerts(r.items || [])).catch(() => {});
  }

  function doInstall() {
    if (!email || !domain) return;
    setInstalling(true);
    setLogs([]);
    const abort = streamTLSInstall(
      { email, domain, issuer: issuerName, ingress_class: ingressClass, staging, ingress_name: ingressName, ingress_namespace: ingressNs },
      (msg) => {
        setLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setInstalling(false);
          refreshAll();
        }
      },
    );
    abortRef.current = abort;
  }

  function openFormForIngress(name: string, ns: string) {
    setIngressName(name);
    setIngressNs(ns);
    setShowForm(true);
    setLogs([]);
  }

  const hasCertManager = !!tlsStatus?.cert_manager;
  const publicIP = icInfo?.external_ip || '';
  const publicHostname = icInfo?.hostname || '';
  const publicAddr = publicIP || publicHostname;
  const pendingCerts = certs.filter(c => !c.status?.conditions?.some(cond => cond.type === 'Ready' && cond.status === 'True'));
  const configuredDomains = [...new Set(certs.flatMap(c => c.spec?.dnsNames || []))];
  const ingressDomains = [...new Set(ingItems.map(i => i.spec?.rules?.[0]?.host).filter(Boolean) as string[])];
  const allDomains = [...new Set([...configuredDomains, ...ingressDomains])];

  function copyToClipboard(text: string, label: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(label);
      setTimeout(() => setCopied(''), 2000);
    }).catch(() => {});
  }

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>TLS Certificates</h1>
          <p className="page-subtitle">Manage cert-manager, cluster issuers, and HTTPS certificates <a href="https://kindling.sh/docs/tls" target="_blank" rel="noopener noreferrer" style={{ fontSize: 11, color: 'var(--accent)', textDecoration: 'none' }}>Docs ↗</a></p>
        </div>
        {!showForm && logs.length === 0 && (
          <button className="btn btn-primary" style={{ fontSize: 12, padding: '6px 14px' }} onClick={() => setShowForm(true)}>
            {hasCertManager ? 'Configure TLS' : 'Install cert-manager + TLS'}
          </button>
        )}
      </div>

      {/* cert-manager status bar */}
      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-body" style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '10px 16px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="label" style={{ margin: 0 }}>cert-manager</span>
            <StatusBadge ok={hasCertManager} label={hasCertManager ? 'Installed' : 'Not Installed'} />
          </div>
          {issuers.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span className="label" style={{ margin: 0 }}>Issuers</span>
              <span className="mono" style={{ fontSize: 12 }}>{issuers.length}</span>
            </div>
          )}
          {certs.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span className="label" style={{ margin: 0 }}>Certificates</span>
              <span className="mono" style={{ fontSize: 12 }}>{certs.length}</span>
            </div>
          )}
        </div>
      </div>

      {/* ── DNS Configuration Guide ── */}
      {publicAddr && (hasCertManager || certs.length > 0) && !showForm && !installing && (
        <div className="card" style={{ marginBottom: 16, borderLeft: pendingCerts.length > 0 ? '3px solid var(--info, #3b82f6)' : '3px solid var(--success, #22c55e)' }}>
          <div className="card-header" style={{ borderBottom: '1px solid var(--border)' }}>
            <span className="card-icon">{pendingCerts.length > 0 ? '◎' : '✓'}</span>
            <h3>{pendingCerts.length > 0 ? 'DNS Configuration Required' : 'DNS Configuration'}</h3>
            {pendingCerts.length > 0 && (
              <span className="tag tag-yellow" style={{ marginLeft: 'auto', fontSize: 11 }}>
                {pendingCerts.length} cert{pendingCerts.length > 1 ? 's' : ''} pending
              </span>
            )}
          </div>
          <div className="card-body" style={{ padding: '16px' }}>
            {pendingCerts.length > 0 && (
              <div style={{ marginBottom: 16, padding: '10px 14px', background: 'var(--surface-2, rgba(59,130,246,0.08))', borderRadius: 8, fontSize: 13, lineHeight: 1.6 }}>
                <strong>Your certificate is pending validation.</strong> Let's Encrypt needs to verify you own the domain.
                Create the DNS record below, wait for propagation (usually 1–5 minutes), and the certificate will be issued automatically.
              </div>
            )}

            {/* Public IP / Hostname */}
            <div style={{ marginBottom: 16 }}>
              <div className="label" style={{ marginBottom: 6, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                {publicIP ? 'Load Balancer IP' : 'Load Balancer Hostname'}
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <code style={{
                  flex: 1, padding: '10px 14px', background: 'var(--surface-1, #1a1a2e)', border: '1px solid var(--border)',
                  borderRadius: 6, fontSize: 15, fontWeight: 600, fontFamily: 'var(--font-mono)', letterSpacing: '0.5px',
                  color: 'var(--accent, #60a5fa)'
                }}>
                  {publicAddr}
                </code>
                <button
                  className="btn btn-secondary"
                  style={{ fontSize: 12, padding: '8px 14px', whiteSpace: 'nowrap' }}
                  onClick={() => copyToClipboard(publicAddr, 'ip')}
                >
                  {copied === 'ip' ? '✓ Copied' : 'Copy'}
                </button>
              </div>
            </div>

            {/* DNS records table */}
            {allDomains.length > 0 && (
              <div>
                <div className="label" style={{ marginBottom: 6, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Required DNS Records
                </div>
                <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
                  <table className="data-table" style={{ margin: 0 }}>
                    <thead>
                      <tr>
                        <th style={{ width: 60 }}>Type</th>
                        <th>Name / Host</th>
                        <th>Value / Points to</th>
                        <th style={{ width: 60 }}>TTL</th>
                        <th style={{ width: 80 }}>Status</th>
                        <th style={{ width: 40 }}></th>
                      </tr>
                    </thead>
                    <tbody>
                      {allDomains.map(dom => {
                        const matchingCert = certs.find(c => c.spec?.dnsNames?.includes(dom));
                        const isReady = matchingCert?.status?.conditions?.some(c => c.type === 'Ready' && c.status === 'True');
                        const isPending = matchingCert && !isReady;
                        return (
                          <tr key={dom}>
                            <td>
                              <span className="tag" style={{ fontWeight: 600 }}>{publicIP ? 'A' : 'CNAME'}</span>
                            </td>
                            <td className="mono" style={{ fontWeight: 550, fontSize: 13 }}>{dom}</td>
                            <td className="mono" style={{ fontSize: 13, color: 'var(--accent, #60a5fa)' }}>{publicAddr}</td>
                            <td className="mono text-dim" style={{ fontSize: 12 }}>300</td>
                            <td>
                              {isReady ? (
                                <span style={{ color: 'var(--success, #22c55e)', fontWeight: 600, fontSize: 12 }}>✓ Verified</span>
                              ) : isPending ? (
                                <span style={{ color: 'var(--warning, #f59e0b)', fontWeight: 600, fontSize: 12 }}>◔ Pending</span>
                              ) : (
                                <span className="text-dim" style={{ fontSize: 12 }}>—</span>
                              )}
                            </td>
                            <td>
                              <button
                                className="btn btn-secondary"
                                style={{ fontSize: 10, padding: '2px 6px' }}
                                onClick={() => copyToClipboard(dom, dom)}
                                title="Copy hostname"
                              >
                                {copied === dom ? '✓' : '⎘'}
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              </div>
            )}

            {/* Provider-specific hint */}
            <div style={{ marginTop: 14, display: 'flex', alignItems: 'flex-start', gap: 8, fontSize: 12, color: 'var(--text-dim)', lineHeight: 1.5 }}>
              <span style={{ flexShrink: 0, marginTop: 1 }}>◇</span>
              <span>
                Go to your DNS provider (Cloudflare, Namecheap, Route 53, etc.) and create
                {publicIP ? ' an A record' : ' a CNAME record'} for each domain above pointing
                to <strong style={{ color: 'var(--text)' }}>{publicAddr}</strong>.
                Once DNS propagates, Let's Encrypt will complete the HTTP-01 challenge and your certificate will be issued.
                {pendingCerts.length > 0 && (
                  <button className="btn btn-secondary" style={{ fontSize: 11, padding: '2px 8px', marginLeft: 8, verticalAlign: 'middle' }} onClick={refreshAll}>
                    ↻ Refresh status
                  </button>
                )}
              </span>
            </div>
          </div>
        </div>
      )}

      {/* Warning: ingresses without TLS */}
      {untlsIngresses.length > 0 && !installing && !showForm && logs.length === 0 && (
        <div className="card" style={{ marginBottom: 16, borderLeft: '3px solid var(--warning, #f59e0b)' }}>
          <div className="card-body" style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px' }}>
            <span style={{ fontSize: 18 }}>⚠</span>
            <div style={{ flex: 1 }}>
              <span style={{ fontWeight: 600, fontSize: 13 }}>{untlsIngresses.length} ingress{untlsIngresses.length > 1 ? 'es' : ''} without TLS</span>
              <span className="text-dim" style={{ marginLeft: 8, fontSize: 12 }}>
                {untlsIngresses.map(i => i.metadata.name).join(', ')}
              </span>
            </div>
            <button className="btn btn-primary" style={{ fontSize: 12, padding: '4px 12px', whiteSpace: 'nowrap' }} onClick={() => {
              const first = untlsIngresses[0];
              openFormForIngress(first.metadata.name, first.metadata.namespace || 'default');
            }}>Configure TLS</button>
          </div>
        </div>
      )}

      {/* Install / Configure form */}
      {showForm && !installing && logs.length === 0 && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card-header">
            <span className="card-icon">◈</span>
            <h3>{hasCertManager ? 'Configure TLS for Ingress' : 'Install cert-manager + Configure TLS'}</h3>
          </div>
          <div className="card-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
              <div className="form-group">
                <label className="form-label">Email *</label>
                <input className="form-input" value={email} onChange={e => setEmail(e.target.value)} placeholder="admin@example.com" />
              </div>
              <div className="form-group">
                <label className="form-label">Domain *</label>
                <input className="form-input" value={domain} onChange={e => setDomain(e.target.value)} placeholder="example.com" />
              </div>
              <div className="form-group">
                <label className="form-label">Issuer Name</label>
                <input className="form-input" value={issuerName} onChange={e => setIssuerName(e.target.value)} placeholder="letsencrypt-prod" />
              </div>
              <div className="form-group">
                <label className="form-label">Ingress Class</label>
                <input className="form-input" value={ingressClass} onChange={e => setIngressClass(e.target.value)} placeholder="traefik" />
              </div>
            </div>
            <div className="form-group" style={{ marginBottom: 12 }}>
              <label className="form-label">Target Ingress</label>
              <select className="form-input" value={ingressName ? `${ingressNs}/${ingressName}` : ''} onChange={e => {
                if (!e.target.value) { setIngressName(''); setIngressNs(''); return; }
                const parts = e.target.value.split('/');
                setIngressNs(parts[0]);
                setIngressName(parts.slice(1).join('/'));
              }}>
                <option value="">None — only install cert-manager + issuer</option>
                {ingItems.map(ing => {
                  const ns = ing.metadata.namespace || 'default';
                  const host = ing.spec?.rules?.[0]?.host;
                  const hasTLS = !!ing.spec?.tls?.length;
                  return (
                    <option key={`${ns}/${ing.metadata.name}`} value={`${ns}/${ing.metadata.name}`}>
                      {ns}/{ing.metadata.name}{host ? ` (${host})` : ''}{hasTLS ? ' ✓ TLS' : ''}
                    </option>
                  );
                })}
              </select>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <label className="deploy-ingress-toggle">
                <input type="checkbox" checked={staging} onChange={e => setStaging(e.target.checked)} />
                <span className="toggle-track" />
                <span style={{ fontSize: 13 }}>Use staging server</span>
              </label>
              <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
                <button className="btn btn-secondary" onClick={() => setShowForm(false)}>Cancel</button>
                <button className="btn btn-primary" disabled={!email || !domain} onClick={doInstall}>
                  {hasCertManager ? 'Configure TLS' : 'Install & Configure'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Progress log */}
      {logs.length > 0 && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card-header">
            <span className="card-icon">◈</span>
            <h3>{installing ? 'Installing…' : logs.some(l => l.type === 'error') ? 'Error' : 'Complete'}</h3>
          </div>
          <div className="card-body">
            <div className="deploy-log" style={{ maxHeight: 240 }}>
              {logs.map((log, i) => (
                <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                  <span className="deploy-log-icon">
                    {log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}
                  </span>
                  <span>{log.message}</span>
                </div>
              ))}
            </div>
            {!installing && (
              <div style={{ marginTop: 8, display: 'flex', gap: 8 }}>
                <button className="btn btn-secondary" style={{ fontSize: 12, padding: '4px 10px' }} onClick={() => { setLogs([]); setShowForm(false); }}>Close</button>
                <button className="btn btn-secondary" style={{ fontSize: 12, padding: '4px 10px' }} onClick={() => setLogs([])}>Configure Another</button>
              </div>
            )}
          </div>
        </div>
      )}

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

      {/* Certificates table */}
      {certs.length === 0 && !showForm && logs.length === 0 ? (
        <EmptyState icon="◈" message="No TLS certificates found. Click Configure TLS to get started with Let's Encrypt." />
      ) : certs.length > 0 ? (
        <div className="card">
          <div className="card-header">
            <span className="card-icon">◈</span>
            <h3>Certificates</h3>
          </div>
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
        </div>
      ) : null}

      {/* Ingress TLS summary at bottom */}
      {ingItems.length > 0 && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-header">
            <span className="card-icon">⊕</span>
            <h3>Ingress TLS Status</h3>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>Ingress</th><th>Namespace</th><th>Host</th><th>TLS</th><th></th></tr>
              </thead>
              <tbody>
                {ingItems.map(ing => {
                  const ns = ing.metadata.namespace || 'default';
                  const host = ing.spec?.rules?.[0]?.host;
                  const hasTLS = !!ing.spec?.tls?.length;
                  const secret = ing.spec?.tls?.[0]?.secretName;
                  return (
                    <tr key={`${ns}/${ing.metadata.name}`}>
                      <td className="mono" style={{ fontWeight: 550 }}>{ing.metadata.name}</td>
                      <td><span className="tag">{ns}</span></td>
                      <td className="mono">{host || '—'}</td>
                      <td>
                        {hasTLS ? (
                          <span className="prod-tls-badge prod-tls-ok">◈ {secret || 'TLS'}</span>
                        ) : (
                          <span className="prod-tls-badge prod-tls-none">No TLS</span>
                        )}
                      </td>
                      <td>
                        {!hasTLS && (
                          <button className="btn btn-secondary" style={{ fontSize: 11, padding: '2px 8px' }} onClick={() => openFormForIngress(ing.metadata.name, ns)}>Configure</button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

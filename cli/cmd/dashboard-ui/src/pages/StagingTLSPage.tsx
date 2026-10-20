import { useApi, fetchStagingCertificates, fetchStagingClusterIssuers, fetchTLSStatus, streamTLSInstall, fetchStagingIngressController, checkWildcardNameservers, validateWildcardDNSToken, checkWildcardRecord, streamWildcardTLSInstall, streamEnsureIngressController } from '../api';
import { useState, useEffect, useRef } from 'react';
import type { K8sList, K8sIngress, CertificateItem, ClusterIssuerItem, TLSStatus, IngressControllerInfo, WildcardNSCheckResult, WildcardTokenValidationResult, WildcardRecordCheckResult } from '../types';
import { StatusBadge, EmptyState } from './shared';

export function StagingTLSPage() {
  const { data: ingresses } = useApi<K8sList<K8sIngress>>('/api/staging/ingresses');

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
  const [useACMEStaging, setUseACMEStaging] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [logs, setLogs] = useState<{ type: string; message: string }[]>([]);
  const [ingressName, setIngressName] = useState('');
  const [ingressNs, setIngressNs] = useState('');
  const abortRef = useRef<(() => void) | null>(null);

  // Wildcard DNS-01 wizard (C3)
  const [showWildcardWizard, setShowWildcardWizard] = useState(false);
  const [wcStep, setWcStep] = useState<1 | 2 | 3 | 4>(1);
  const [wcDomain, setWcDomain] = useState('');
  const [wcEmail, setWcEmail] = useState('');
  const [wcToken, setWcToken] = useState('');
  const [wcUseACMEStaging, setWcUseACMEStaging] = useState(false);
  const [wcNSChecking, setWcNSChecking] = useState(false);
  const [wcNSResult, setWcNSResult] = useState<WildcardNSCheckResult | null>(null);
  const [wcTokenValidating, setWcTokenValidating] = useState(false);
  const [wcTokenResult, setWcTokenResult] = useState<WildcardTokenValidationResult | null>(null);
  const [wcRecordChecking, setWcRecordChecking] = useState(false);
  const [wcRecordResult, setWcRecordResult] = useState<WildcardRecordCheckResult | null>(null);
  const [wcExpectedAddr, setWcExpectedAddr] = useState('');
  const [wcExpectedAddrTouched, setWcExpectedAddrTouched] = useState(false);
  const [wcInstalling, setWcInstalling] = useState(false);
  const [wcLogs, setWcLogs] = useState<{ type: string; message: string }[]>([]);
  const wcAbortRef = useRef<(() => void) | null>(null);
  const [wcEnsuringIngress, setWcEnsuringIngress] = useState(false);
  const [wcEnsureIngressLogs, setWcEnsureIngressLogs] = useState<{ type: string; message: string }[]>([]);
  const [wcIngressPolling, setWcIngressPolling] = useState(false);
  const wcPollCancelledRef = useRef(false);

  const ingItems = ingresses?.items || [];
  const untlsIngresses = ingItems.filter(ing => !ing.spec?.tls?.length);

  useEffect(() => {
    fetchStagingCertificates().then(r => setCerts(r.items || [])).catch(() => {});
    fetchStagingClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
    fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
    fetchStagingIngressController().then(ic => setIcInfo(ic)).catch(() => {});
    return () => { abortRef.current?.(); };
  }, []);

  // Pre-fill the wildcard wizard's "expected address" from auto-detection
  // once it resolves, but only until the user actually edits the field --
  // auto-detection failing (a common case: this feature's own ingress
  // controller search only checks a handful of hardcoded namespace/label
  // combinations) must never permanently block entering one by hand.
  useEffect(() => {
    const ic = icInfo?.external_ip || icInfo?.hostname || '';
    if (ic && !wcExpectedAddrTouched) setWcExpectedAddr(ic);
  }, [icInfo, wcExpectedAddrTouched]);

  function refreshAll() {
    fetchTLSStatus().then(s => setTlsStatus(s)).catch(() => {});
    fetchStagingClusterIssuers().then(r => setIssuers(r.items || [])).catch(() => {});
    fetchStagingCertificates().then(r => setCerts(r.items || [])).catch(() => {});
  }

  function doInstall() {
    if (!email || !domain) return;
    setInstalling(true);
    setLogs([]);
    const abort = streamTLSInstall(
      { email, domain, issuer: issuerName, ingress_class: ingressClass, staging: useACMEStaging, ingress_name: ingressName, ingress_namespace: ingressNs },
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

  // ── Wildcard DNS-01 wizard (C3) ──────────────────────────────────

  function openWildcardWizard() {
    setShowWildcardWizard(true);
    setShowForm(false);
    setWcStep(1);
    setWcNSResult(null);
    setWcTokenResult(null);
    setWcRecordResult(null);
    setWcLogs([]);
    wcPollCancelledRef.current = false;
  }

  function closeWildcardWizard() {
    if (wcAbortRef.current) wcAbortRef.current();
    wcPollCancelledRef.current = true;
    setShowWildcardWizard(false);
  }

  function wcCheckNS() {
    if (!wcDomain) return;
    setWcNSChecking(true);
    setWcNSResult(null);
    checkWildcardNameservers(wcDomain)
      .then(setWcNSResult)
      .catch(() => setWcNSResult({ ok: false, error: 'Nameserver lookup failed' }))
      .finally(() => setWcNSChecking(false));
  }

  function wcValidateToken() {
    if (!wcDomain || !wcToken) return;
    setWcTokenValidating(true);
    setWcTokenResult(null);
    validateWildcardDNSToken(wcDomain, wcToken)
      .then(setWcTokenResult)
      .catch(() => setWcTokenResult({ ok: false, error: 'Token validation request failed' }))
      .finally(() => setWcTokenValidating(false));
  }

  function wcCheckRecord() {
    if (!wcDomain || !wcExpectedAddr) return;
    setWcRecordChecking(true);
    setWcRecordResult(null);
    checkWildcardRecord(wcDomain, wcExpectedAddr)
      .then(setWcRecordResult)
      .catch(() => setWcRecordResult({ ok: false, error: 'DNS record check failed' }))
      .finally(() => setWcRecordChecking(false));
  }

  // No ingress controller has ever been installed on this staging
  // cluster yet (e.g. TLS is being configured before any deploy has
  // run there) -- step 3 has nothing to confirm a DNS record against
  // until one exists. Installs it on demand, then polls for its
  // external address so wcExpectedAddr gets auto-filled the normal way
  // -- a single fetch right after install isn't enough, since a cloud
  // load balancer can take a minute or two to get an external IP after
  // the Service itself is created.
  function wcEnsureIngress() {
    setWcEnsuringIngress(true);
    setWcEnsureIngressLogs([]);
    wcPollCancelledRef.current = false;
    streamEnsureIngressController((msg) => {
      setWcEnsureIngressLogs(prev => [...prev, msg]);
      if (msg.type === 'done' || msg.type === 'error') {
        setWcEnsuringIngress(false);
        if (msg.type === 'done') pollForIngressAddress();
      }
    });
  }

  function pollForIngressAddress(attempt = 0) {
    setWcIngressPolling(true);
    fetchStagingIngressController().then(ic => {
      if (wcPollCancelledRef.current) return;
      setIcInfo(ic);
      const addr = ic.external_ip || ic.hostname;
      if (addr || attempt >= 24) {
        setWcIngressPolling(false);
        return;
      }
      setTimeout(() => pollForIngressAddress(attempt + 1), 5000);
    }).catch(() => {
      if (wcPollCancelledRef.current) return;
      if (attempt >= 24) {
        setWcIngressPolling(false);
        return;
      }
      setTimeout(() => pollForIngressAddress(attempt + 1), 5000);
    });
  }

  function wcStartInstall() {
    if (!wcDomain || !wcEmail || !wcToken) return;
    setWcInstalling(true);
    setWcLogs([]);
    const abort = streamWildcardTLSInstall(
      { domain: wcDomain, email: wcEmail, dns_provider: 'cloudflare', dns_api_token: wcToken, use_acme_staging: wcUseACMEStaging },
      (msg) => {
        setWcLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setWcInstalling(false);
          refreshAll();
        }
      },
    );
    wcAbortRef.current = abort;
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
        {!showForm && logs.length === 0 && !showWildcardWizard && (
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn btn-secondary" style={{ fontSize: 12, padding: '6px 14px' }} onClick={openWildcardWizard}>
              Wildcard TLS (shared staging)
            </button>
            <button className="btn btn-primary" style={{ fontSize: 12, padding: '6px 14px' }} onClick={() => setShowForm(true)}>
              {hasCertManager ? 'Configure TLS' : 'Install cert-manager + TLS'}
            </button>
          </div>
        )}
      </div>

      {/* ── Wildcard DNS-01 wizard (C3) ──────────────────────────── */}
      {showWildcardWizard && (
        <div className="card" style={{ marginBottom: 16 }}>
          <div className="card-header">
            <span className="card-icon">◈</span>
            <h3>Wildcard TLS for shared staging</h3>
            <span className="text-dim" style={{ marginLeft: 8, fontSize: 11 }}>Step {wcStep} of 4</span>
            <button className="btn btn-sm btn-ghost" style={{ marginLeft: 'auto' }} onClick={closeWildcardWizard}>✕</button>
          </div>
          <div className="card-body">
            <p className="text-dim" style={{ fontSize: 13, marginBottom: 16 }}>
              Issues one wildcard certificate covering every branch's <code>&lt;slug&gt;.staging.&lt;domain&gt;</code> host
              automatically — no per-branch TLS setup, matching <code>kindling staging tls --wildcard</code>. Cloudflare only, today.
            </p>

            {/* Step 1: Domain & DNS provider check */}
            {wcStep === 1 && (
              <>
                <div className="form-group">
                  <label className="form-label">Staging Domain *</label>
                  <input className="form-input" placeholder="subnode1.xyz (bare apex domain, not staging.subnode1.xyz)"
                    value={wcDomain} onChange={e => { setWcDomain(e.target.value); setWcNSResult(null); }} />
                  <span className="form-hint">Wildcard cert will cover *.staging.{wcDomain || '<domain>'}</span>
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                  <button className="btn btn-secondary" disabled={!wcDomain || wcNSChecking} onClick={wcCheckNS}>
                    {wcNSChecking ? 'Checking…' : 'Check nameservers'}
                  </button>
                </div>
                {wcNSResult && (
                  <div style={{ marginTop: 12, padding: '10px 14px', borderRadius: 8, fontSize: 13, background: wcNSResult.ok && wcNSResult.cloudflare ? 'var(--success-bg, rgba(34,197,94,0.1))' : 'var(--warning-bg, rgba(245,158,11,0.1))' }}>
                    {wcNSResult.ok ? (
                      wcNSResult.cloudflare ? (
                        <span>✓ Your domain's DNS is managed by Cloudflare — wildcard records are supported.</span>
                      ) : (
                        <>
                          <div>✗ This domain's nameservers don't look like Cloudflare's:</div>
                          <div className="mono" style={{ marginTop: 4 }}>{(wcNSResult.nameservers || []).join(', ') || '(none found)'}</div>
                          <div style={{ marginTop: 6 }}>Migrate this domain's nameservers to Cloudflare first — this is the one step this wizard can't do for you.</div>
                        </>
                      )
                    ) : (
                      <span>✗ {wcNSResult.error}</span>
                    )}
                  </div>
                )}
                <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
                  <button className="btn btn-primary" disabled={!wcNSResult?.ok || !wcNSResult?.cloudflare} onClick={() => setWcStep(2)}>
                    Next: API token
                  </button>
                </div>
              </>
            )}

            {/* Step 2: API token */}
            {wcStep === 2 && (
              <>
                <div className="form-group">
                  <label className="form-label">Cloudflare API Token *</label>
                  <input className="form-input" type="password" placeholder="Edit zone DNS token, scoped to this zone"
                    value={wcToken} onChange={e => { setWcToken(e.target.value); setWcTokenResult(null); }} />
                  <span className="form-hint">
                    Create one at Cloudflare → My Profile → API Tokens, using the "Edit zone DNS" template scoped to{' '}
                    <span className="mono">{wcDomain}</span> — never "All zones".
                  </span>
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                  <button className="btn btn-secondary" disabled={!wcToken || wcTokenValidating} onClick={wcValidateToken}>
                    {wcTokenValidating ? 'Validating…' : 'Validate token'}
                  </button>
                </div>
                {wcTokenResult && (
                  <div style={{ marginTop: 12, padding: '10px 14px', borderRadius: 8, fontSize: 13, background: wcTokenResult.ok ? 'var(--success-bg, rgba(34,197,94,0.1))' : 'var(--warning-bg, rgba(245,158,11,0.1))' }}>
                    {wcTokenResult.ok ? (
                      <span>✓ Token is valid and has access to zone <span className="mono">{wcTokenResult.zone_name}</span>.</span>
                    ) : (
                      <span>✗ {wcTokenResult.error}</span>
                    )}
                  </div>
                )}
                <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 16 }}>
                  <button className="btn btn-secondary" onClick={() => setWcStep(1)}>Back</button>
                  <button className="btn btn-primary" disabled={!wcTokenResult?.ok} onClick={() => setWcStep(3)}>
                    Next: Confirm DNS record
                  </button>
                </div>
              </>
            )}

            {/* Step 3: Confirm the wildcard record */}
            {wcStep === 3 && (
              <>
                <div style={{ marginBottom: 12, fontSize: 13 }}>
                  Add this record at Cloudflare, then confirm it's propagated:
                </div>
                <div style={{ marginBottom: 12, padding: '10px 14px', borderRadius: 8, fontSize: 13, background: 'var(--info-bg, rgba(59,130,246,0.08))' }}>
                  ⏳ DNS propagation isn't instant — it usually takes a few minutes after you add the record,
                  but can occasionally run longer depending on your registrar/resolver. If "Check now" doesn't
                  find it right away, that's expected — wait a minute and try again rather than assuming
                  something's wrong. (Certificate issuance in the next step has its own, separate wait on top of
                  this — typically 6–8 minutes end to end — so there's no need to rush this check.)
                </div>
                <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden', marginBottom: 12 }}>
                  <table className="data-table" style={{ margin: 0 }}>
                    <thead><tr><th style={{ width: 60 }}>Type</th><th>Name</th><th>Points to</th></tr></thead>
                    <tbody>
                      <tr>
                        <td><span className="tag" style={{ fontWeight: 600 }}>{/^\d{1,3}(\.\d{1,3}){3}$/.test(wcExpectedAddr) ? 'A' : 'CNAME'}</span></td>
                        <td className="mono">*.staging.{wcDomain}</td>
                        <td>
                          <input
                            className="form-input mono"
                            style={{ padding: '4px 8px', fontSize: 13 }}
                            placeholder={wcIngressPolling ? 'waiting for external IP…' : 'ingress controller IP or hostname'}
                            value={wcExpectedAddr}
                            onChange={e => { setWcExpectedAddr(e.target.value); setWcExpectedAddrTouched(true); }}
                          />
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                {!publicAddr && (
                  <div style={{ marginBottom: 12, padding: '10px 14px', borderRadius: 8, fontSize: 13, background: 'var(--warning-bg, rgba(245,158,11,0.1))' }}>
                    <div>
                      No ingress controller was detected on this cluster at all — if TLS is being configured
                      before any staging deploy has run here, that's expected: nothing has installed one yet.
                    </div>
                    <div style={{ marginTop: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
                      <button className="btn btn-secondary" style={{ fontSize: 12, padding: '4px 10px' }} disabled={wcEnsuringIngress} onClick={wcEnsureIngress}>
                        {wcEnsuringIngress ? 'Installing…' : 'Install ingress controller now'}
                      </button>
                      <span className="text-dim" style={{ fontSize: 12 }}>or enter the address below by hand once you know it</span>
                    </div>
                    {wcEnsureIngressLogs.length > 0 && (
                      <div className="deploy-log" style={{ maxHeight: 140, marginTop: 8 }}>
                        {wcEnsureIngressLogs.map((log, i) => (
                          <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                            <span className="deploy-log-icon">{log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}</span>
                            <span>{log.message}</span>
                          </div>
                        ))}
                        {wcIngressPolling && (
                          <div className="deploy-log-line deploy-log-step">
                            <span className="deploy-log-icon deploy-log-spinner">◌</span>
                            <span className="text-dim">Waiting for an external IP/hostname to be assigned (cloud load balancers can take a minute or two)…</span>
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )}
                <button className="btn btn-secondary" disabled={!wcExpectedAddr || wcRecordChecking} onClick={wcCheckRecord}>
                  {wcRecordChecking ? 'Checking…' : 'Check now'}
                </button>
                {wcRecordResult && (
                  <div style={{ marginTop: 12, padding: '10px 14px', borderRadius: 8, fontSize: 13, background: wcRecordResult.ok ? 'var(--success-bg, rgba(34,197,94,0.1))' : 'var(--warning-bg, rgba(245,158,11,0.1))' }}>
                    {wcRecordResult.ok ? (
                      <span>✓ <span className="mono">{wcRecordResult.probe}</span> resolves correctly — the wildcard record is live.</span>
                    ) : (
                      <span>✗ {wcRecordResult.error || 'Record does not resolve to the expected address yet.'}</span>
                    )}
                  </div>
                )}
                <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 16 }}>
                  <button className="btn btn-secondary" onClick={() => setWcStep(2)}>Back</button>
                  <button className="btn btn-primary" disabled={!wcRecordResult?.ok} onClick={() => setWcStep(4)}>
                    Next: Issue certificate
                  </button>
                </div>
              </>
            )}

            {/* Step 4: Issue the certificate */}
            {wcStep === 4 && (
              <>
                {wcLogs.length === 0 && (
                  <>
                    <div className="form-group">
                      <label className="form-label">Email *</label>
                      <input className="form-input" placeholder="admin@example.com" value={wcEmail} onChange={e => setWcEmail(e.target.value)} />
                    </div>
                    <label className="deploy-ingress-toggle" style={{ marginTop: 8 }}>
                      <input type="checkbox" checked={wcUseACMEStaging} onChange={e => setWcUseACMEStaging(e.target.checked)} />
                      <span className="toggle-track" />
                      <span style={{ fontSize: 13 }}>Use Let's Encrypt staging server (for testing)</span>
                    </label>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 16 }}>
                      <button className="btn btn-secondary" onClick={() => setWcStep(3)}>Back</button>
                      <button className="btn btn-primary" disabled={!wcEmail || wcInstalling} onClick={wcStartInstall}>
                        {wcInstalling ? 'Issuing…' : 'Issue certificate'}
                      </button>
                    </div>
                  </>
                )}
                {wcLogs.length > 0 && (
                  <>
                    <div className="deploy-log" style={{ maxHeight: 240 }}>
                      {wcLogs.map((log, i) => (
                        <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                          <span className="deploy-log-icon">{log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}</span>
                          <span>{log.message}</span>
                        </div>
                      ))}
                      {wcInstalling && (
                        <div className="deploy-log-line deploy-log-step">
                          <span className="deploy-log-icon deploy-log-spinner">◌</span>
                          <span className="text-dim">Waiting for DNS-01 validation (can take several minutes)…</span>
                        </div>
                      )}
                    </div>
                    {!wcInstalling && (
                      <div style={{ marginTop: 8, display: 'flex', gap: 8 }}>
                        <button className="btn btn-secondary" style={{ fontSize: 12, padding: '4px 10px' }} onClick={closeWildcardWizard}>Close</button>
                      </div>
                    )}
                  </>
                )}
              </>
            )}
          </div>
        </div>
      )}

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
                <input type="checkbox" checked={useACMEStaging} onChange={e => setUseACMEStaging(e.target.checked)} />
                <span className="toggle-track" />
                <span style={{ fontSize: 13 }}>Use Let's Encrypt staging server (for testing)</span>
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
                          <span className="staging-tls-badge staging-tls-ok">◈ {secret || 'TLS'}</span>
                        ) : (
                          <span className="staging-tls-badge staging-tls-none">No TLS</span>
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

import { useState, useEffect, useRef } from 'react';
import { fetchSnapshotStatus, fetchSnapshotCredentials, streamSnapshotDeploy, updateStagingSecrets, fetchStagingIngressController, fetchStagingContexts, setStagingContext } from '../api';
import type { SnapshotStatus, SnapshotService, IngressControllerInfo } from '../types';
import type { SnapshotCredential } from '../api';
import { StatusBadge, EmptyState } from './shared';

type DeployStep = 'configure' | 'deploying' | 'done';

export function StagingDeployPage() {
  const [status, setStatus] = useState<SnapshotStatus | null>(null);
  const [loading, setLoading] = useState(true);

  // Form state
  const [registry, setRegistry] = useState('');
  const [registryUser, setRegistryUser] = useState('');
  const [registryPass, setRegistryPass] = useState('');
  const [tag, setTag] = useState('latest');
  const [format, setFormat] = useState<'helm' | 'kustomize'>('helm');
  const [namespace, setNamespace] = useState('');
  const [selectedIngress, setSelectedIngress] = useState<Set<string>>(new Set());

  // Branch-derived naming (optional -- blank reproduces this page's
  // original single-environment behavior exactly: chart name/namespace
  // fall back to "kindling-snapshot"/"default", and no Ingress host is
  // derived).
  const [branch, setBranch] = useState('');
  const [stagingDomain, setStagingDomain] = useState('');

  // Production values (--render-prod-values/--prod-env-prefix parity)
  const [renderProdValues, setRenderProdValues] = useState(false);
  const [prodEnvPrefix, setProdEnvPrefix] = useState('');
  const [prodValuesYAML, setProdValuesYAML] = useState('');

  // --creds-config file picker (reads a local YAML file's contents into
  // state -- never written anywhere server-side except a temp chart dir
  // this same deploy already generates).
  const [credsConfigYAML, setCredsConfigYAML] = useState('');
  const [credsConfigFileName, setCredsConfigFileName] = useState('');

  // Staging context -- settable at runtime from this page's own empty
  // state, no dashboard restart required.
  const [availableContexts, setAvailableContexts] = useState<string[]>([]);
  const [contextInput, setContextInput] = useState('');
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState('');
  const [showContextPicker, setShowContextPicker] = useState(false);

  // Credential state
  const [devCreds, setDevCreds] = useState<SnapshotCredential[]>([]);
  const [credValues, setCredValues] = useState<Record<string, string>>({});
  const [cachedAt, setCachedAt] = useState('');
  const [showCredentials, setShowCredentials] = useState(false);

  // Deploy state
  const [step, setStep] = useState<DeployStep>('configure');
  const [logs, setLogs] = useState<{ type: string; message: string }[]>([]);
  const logRef = useRef<HTMLDivElement>(null);
  const cancelRef = useRef<(() => void) | null>(null);

  // Post-deploy secret update state
  const [postDeploySecretValues, setPostDeploySecretValues] = useState<Record<string, string>>({});
  const [secretUpdateStatus, setSecretUpdateStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [secretUpdateMsg, setSecretUpdateMsg] = useState('');

  // Post-deploy external IP lookup — the ingress controller's cloud LB
  // can take a minute or two to provision, so poll until it shows up.
  const [ingressInfo, setIngressInfo] = useState<IngressControllerInfo | null>(null);
  const [ingressLookupFailed, setIngressLookupFailed] = useState(false);

  function loadStatus() {
    return Promise.all([
      fetchSnapshotStatus(),
      fetchSnapshotCredentials().catch(() => ({ credentials: [], cached_at: '' })),
    ]).then(([s, creds]) => {
      setStatus(s);
      // Pre-select all services with ingress enabled
      const ing = new Set<string>();
      for (const svc of s.services) {
        if (svc.ingress?.enabled) ing.add(svc.name);
      }
      setSelectedIngress(ing);
      applyCredentialsResponse(creds, /* resetUntouchedFields */ true);
    }).catch(() => {});
  }

  // Shared by loadStatus (full page (re)load -- resets every untouched
  // credential field, including dev-default ones, back to whatever's now
  // cached) and refreshCredentialHints (branch-change only -- see there
  // for why it must *not* reuse loadStatus wholesale).
  function applyCredentialsResponse(creds: { credentials: SnapshotCredential[]; cached_at: string }, resetUntouchedFields: boolean) {
    if (!creds.credentials?.length) return;
    setDevCreds(creds.credentials);
    setCachedAt(creds.cached_at || '');
    setShowCredentials(true);
    // Pre-fill *only* from a legitimately cached (chart-scoped) staging
    // value -- never from dev_value. dev_value is just an illustrative
    // hint (shown as the input's placeholder) of what the *dev*
    // cluster's value looks like; it is never a real staging
    // credential, and pre-filling the actual submitted value with it
    // meant every first deploy of a new branch silently submitted that
    // guess as if the user had typed it -- bypassing this deploy's own
    // correctly chart-scoped dev-default fallback entirely. Leaving it
    // blank here lets a field the user never touches fall through to
    // that fallback instead, exactly like the CLI's own "leave blank to
    // use dev default" behavior.
    setCredValues(prev => {
      const next = resetUntouchedFields ? {} : { ...prev };
      for (const c of creds.credentials) {
        if (!c.cached) continue;
        // Never clobber a value the user already typed into this field
        // (relevant for the branch-change refresh path, where the user
        // may well have started filling the form in before adjusting
        // Branch -- re-deriving the dev-value hint must not wipe that
        // out from under them).
        if (resetUntouchedFields || !(c.env_var in prev)) {
          next[c.env_var] = c.cached;
        }
      }
      return next;
    });
  }

  useEffect(() => {
    loadStatus().finally(() => setLoading(false));
    fetchStagingContexts().then(setAvailableContexts).catch(() => {});
  }, []);

  // Re-fetch credential hints (dev_value is chart-scoped) whenever the
  // branch field settles on a new value, so the placeholder text shown
  // for each credential reflects *this* branch's actual dependency
  // hostnames instead of the pre-branch-resolution default.
  //
  // Deliberately does NOT call loadStatus() -- loadStatus also resets
  // selectedIngress back to "whatever's enabled in dev" on every call,
  // which would silently undo a user's manual Ingress-service selection
  // the moment they next blur the Branch field after checking/unchecking
  // boxes (confirmed live: exactly this caused an unintended multi-
  // Ingress host collision on a deploy the user believed had only one
  // service selected). This fetches credential hints in isolation
  // instead, leaving selectedIngress and any already-typed credential
  // values alone.
  function handleBranchBlur() {
    fetchSnapshotCredentials(branch.trim() || undefined)
      .then(creds => applyCredentialsResponse(creds, /* resetUntouchedFields */ false))
      .catch(() => {});
  }

  async function handleConnect() {
    if (!contextInput) return;
    setConnecting(true);
    setConnectError('');
    const result = await setStagingContext(contextInput);
    setConnecting(false);
    if (!result.ok) {
      setConnectError(result.error || 'Failed to set staging context');
      return;
    }
    setShowContextPicker(false);
    setLoading(true);
    await loadStatus();
    setLoading(false);
  }

  // Auto-scroll log
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [logs]);

  // Once the deploy finishes successfully, look up the ingress controller's
  // external address so the user can point DNS at it right away. Poll for
  // a couple of minutes in case a cloud load balancer is still provisioning.
  useEffect(() => {
    if (step !== 'done' || logs.some(l => l.type === 'error')) return;
    let cancelled = false;
    let attempts = 0;
    setIngressLookupFailed(false);

    const poll = () => {
      fetchStagingIngressController()
        .then(info => {
          if (cancelled) return;
          setIngressInfo(info);
          attempts += 1;
          if (!info.external_ip && attempts < 24) {
            setTimeout(poll, 5000);
          } else if (!info.external_ip) {
            setIngressLookupFailed(true);
          }
        })
        .catch(() => {
          if (!cancelled) setIngressLookupFailed(true);
        });
    };
    poll();

    return () => { cancelled = true; };
  }, [step, logs]);

  function toggleIngress(name: string) {
    setSelectedIngress(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  function startDeploy() {
    if (!registry) return;
    setStep('deploying');
    setLogs([]);
    setIngressInfo(null);
    setIngressLookupFailed(false);
    setProdValuesYAML('');

    // Build credentials map — only include non-empty values
    const credentials: Record<string, string> = {};
    for (const [k, v] of Object.entries(credValues)) {
      if (v.trim()) credentials[k] = v.trim();
    }

    const cancel = streamSnapshotDeploy(
      {
        registry, registry_user: registryUser, registry_pass: registryPass,
        tag, format, namespace, ingress: Array.from(selectedIngress),
        ...(Object.keys(credentials).length > 0 ? { credentials } : {}),
        ...(branch.trim() ? { branch: branch.trim() } : {}),
        ...(stagingDomain.trim() ? { staging_domain: stagingDomain.trim() } : {}),
        ...(renderProdValues ? { render_prod_values: true, prod_env_prefix: prodEnvPrefix.trim() || undefined } : {}),
        ...(credsConfigYAML ? { creds_config_yaml: credsConfigYAML } : {}),
      },
      (msg) => {
        if (msg.type === 'prod-values') {
          setProdValuesYAML(msg.message);
          return;
        }
        setLogs(prev => [...prev, msg]);
        if (msg.type === 'done' || msg.type === 'error') {
          setStep('done');
        }
      },
    );
    cancelRef.current = cancel;
  }

  function reset() {
    if (cancelRef.current) cancelRef.current();
    setStep('configure');
    setLogs([]);
    setIngressInfo(null);
    setIngressLookupFailed(false);
    setProdValuesYAML('');
    fetchSnapshotStatus().then(s => setStatus(s)).catch(() => {});
  }

  function handleCredsConfigFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setCredsConfigFileName(file.name);
    const reader = new FileReader();
    reader.onload = () => setCredsConfigYAML(String(reader.result || ''));
    reader.readAsText(file);
  }

  function downloadProdValues() {
    const blob = new Blob([prodValuesYAML], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'values-prod.yaml';
    a.click();
    URL.revokeObjectURL(url);
  }

  if (loading) return <div className="loading">Reading cluster state…</div>;

  if (!status?.connected || showContextPicker) {
    return (
      <div className="page">
        <div className="page-header"><div className="page-header-left">
          <h1>Deploy to Staging</h1>
          <p className="page-subtitle">{status?.connected ? `Currently connected to ${status.context}` : 'Not connected — set a staging cluster context below'}</p>
        </div></div>
        <div className="staging-disconnected">
          <div className="staging-disconnected-icon">⚠</div>
          <h2>No Staging Context</h2>
          <p>Pick (or type) the kubeconfig context for your staging cluster — no dashboard restart needed.</p>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'center', marginTop: 12, flexWrap: 'wrap' }}>
            <input
              className="form-input"
              style={{ maxWidth: 320 }}
              list="staging-context-options"
              placeholder="my-staging-context"
              value={contextInput}
              onChange={e => setContextInput(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') handleConnect(); }}
            />
            <datalist id="staging-context-options">
              {availableContexts.map(c => <option key={c} value={c} />)}
            </datalist>
            <button className="btn btn-primary" disabled={!contextInput || connecting} onClick={handleConnect}>
              {connecting ? 'Connecting…' : 'Connect'}
            </button>
            {status?.connected && (
              <button className="btn btn-ghost" onClick={() => setShowContextPicker(false)}>Cancel</button>
            )}
          </div>
          {connectError && <p style={{ color: 'var(--danger, #e05252)', marginTop: 8 }}>{connectError}</p>}
          <p className="text-dim" style={{ fontSize: 12, marginTop: 16 }}>
            Or launch the dashboard with a context already set:
          </p>
          <pre className="staging-code-block">kindling dashboard --staging-context &lt;your-context&gt;</pre>
        </div>
      </div>
    );
  }

  const services: SnapshotService[] = status?.services || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Deploy to Staging</h1>
          <p className="page-subtitle">
            Snapshot dev environment and deploy to <span className="mono">{status?.context}</span>
            {' '}<a href="https://kindling.sh/docs/graduation" target="_blank" rel="noopener noreferrer" style={{ fontSize: 11, color: 'var(--accent)', textDecoration: 'none' }}>Docs ↗</a>
          </p>
        </div>
        <div className="page-actions">
          <div className="deploy-tools">
            <StatusBadge ok={!!status?.helm} label={status?.helm ? 'Helm ✓' : 'Helm ✗'} />
            <StatusBadge ok={!!status?.crane || !!status?.docker} label={status?.crane ? 'Crane ✓' : status?.docker ? 'Docker ✓' : 'No push tool'} />
          </div>
        </div>
      </div>

      {step === 'configure' && (
        <>
          {/* Services detected */}
          {services.length === 0 ? (
            <EmptyState icon="□" message="No DevStagingEnvironments found in the local Kind cluster. Deploy services with kindling first." />
          ) : (
            <>
              <div className="card" style={{ marginBottom: 16 }}>
                <div className="card-header">
                  <span className="card-icon">□</span>
                  <h3>Services ({services.length})</h3>
                </div>
                <div className="card-body" style={{ padding: 0 }}>
                  <div className="table-wrap">
                    <table className="data-table">
                      <thead>
                        <tr><th>Service</th><th>Image</th><th>Port</th><th>Replicas</th><th>Dependencies</th><th>Compute</th><th>Ingress</th></tr>
                      </thead>
                      <tbody>
                        {services.map(svc => (
                          <tr key={svc.name}>
                            <td className="mono" style={{ fontWeight: 550 }}>{svc.name}</td>
                            <td className="mono" style={{ fontSize: 12 }}>{svc.image}</td>
                            <td>{svc.port}</td>
                            <td>{svc.replicas}</td>
                            <td>
                              {svc.deps?.length ? svc.deps.map(d => (
                                <span key={d} className="tag" style={{ marginRight: 4 }}>{d}</span>
                              )) : <span className="text-dim">—</span>}
                            </td>
                            <td>
                              {svc.compute ? (
                                <span className="tag tag-compute">{svc.compute}</span>
                              ) : <span className="text-dim">—</span>}
                            </td>
                            <td>
                              <label className="deploy-ingress-toggle">
                                <input
                                  type="checkbox"
                                  checked={selectedIngress.has(svc.name)}
                                  onChange={() => toggleIngress(svc.name)}
                                />
                                <span className="toggle-track" />
                                <span className="mono" style={{ fontSize: 12 }}>
                                  {svc.ingress?.enabled ? (svc.ingress.host || 'enabled in dev') : 'expose'}
                                </span>
                              </label>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              </div>

              {/* Deploy configuration */}
              <div className="card-grid card-grid-2" style={{ marginBottom: 16 }}>
                <div className="card">
                  <div className="card-header">
                    <span className="card-icon">◇</span>
                    <h3>Registry & Image</h3>
                  </div>
                  <div className="card-body">
                    <div className="form-group">
                      <label className="form-label">Container Registry *</label>
                      <input
                        className="form-input"
                        placeholder="ghcr.io/org or docker.io/user"
                        value={registry}
                        onChange={e => setRegistry(e.target.value)}
                      />
                      <span className="form-hint">Images will be pushed as registry/service:tag</span>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Username</label>
                      <input
                        className="form-input"
                        placeholder="Registry username"
                        value={registryUser}
                        onChange={e => setRegistryUser(e.target.value)}
                      />
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Password / Token</label>
                      <input
                        className="form-input"
                        type="password"
                        placeholder="Registry password or access token"
                        value={registryPass}
                        onChange={e => setRegistryPass(e.target.value)}
                      />
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Tag</label>
                      <input
                        className="form-input"
                        placeholder="latest"
                        value={tag}
                        onChange={e => setTag(e.target.value)}
                      />
                    </div>
                  </div>
                </div>

                <div className="card">
                  <div className="card-header">
                    <span className="card-icon">⬡</span>
                    <h3>Deploy Target</h3>
                  </div>
                  <div className="card-body">
                    <div className="form-group">
                      <label className="form-label">Format</label>
                      <div className="deploy-format-toggle">
                        <button
                          className={`deploy-format-btn ${format === 'helm' ? 'active' : ''}`}
                          onClick={() => setFormat('helm')}
                        >
                          Helm
                        </button>
                        <button
                          className={`deploy-format-btn ${format === 'kustomize' ? 'active' : ''}`}
                          onClick={() => setFormat('kustomize')}
                        >
                          Kustomize
                        </button>
                      </div>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Namespace</label>
                      <input
                        className="form-input"
                        placeholder={namespace ? undefined : 'default (or branch slug, if Branch is set below)'}
                        value={namespace}
                        onChange={e => setNamespace(e.target.value)}
                      />
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Branch <span className="text-dim" style={{ fontWeight: 400 }}>(optional)</span></label>
                      <input
                        className="form-input"
                        placeholder="current branch, if unset"
                        value={branch}
                        onChange={e => setBranch(e.target.value)}
                        onBlur={handleBranchBlur}
                      />
                      <span className="form-hint">Derives the chart name/namespace, same as `kindling snapshot --deploy --branch`</span>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Staging Domain <span className="text-dim" style={{ fontWeight: 400 }}>(optional)</span></label>
                      <input
                        className="form-input"
                        placeholder="example.com"
                        value={stagingDomain}
                        onChange={e => setStagingDomain(e.target.value)}
                      />
                      <span className="form-hint">Derives an Ingress host as `&lt;branch-slug&gt;.staging.&lt;domain&gt;` for selected services with no explicit host</span>
                    </div>
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Target Context</label>
                      <input
                        className="form-input"
                        value={status?.context || ''}
                        disabled
                      />
                      <span className="form-hint">
                        <a href="#" onClick={e => { e.preventDefault(); setShowContextPicker(true); }}>Change context</a>
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Production values + creds-config */}
              <div className="card" style={{ marginBottom: 16 }}>
                <div className="card-header">
                  <span className="card-icon">◈</span>
                  <h3>Production Values</h3>
                </div>
                <div className="card-body">
                  <label className="form-checkbox">
                    <input type="checkbox" checked={renderProdValues} onChange={e => setRenderProdValues(e.target.checked)} />
                    Also render production values (`--render-prod-values`)
                  </label>
                  {renderProdValues && (
                    <div className="form-group" style={{ marginTop: 12 }}>
                      <label className="form-label">Production env prefix</label>
                      <input
                        className="form-input"
                        placeholder="prod-"
                        value={prodEnvPrefix}
                        onChange={e => setProdEnvPrefix(e.target.value)}
                      />
                      <span className="form-hint">KINDLING_ENV_PREFIX value injected for the production render</span>
                    </div>
                  )}

                  <div className="form-group" style={{ marginTop: 16 }}>
                    <label className="form-label">Credentials config file <span className="text-dim" style={{ fontWeight: 400 }}>(optional, `--creds-config`)</span></label>
                    <input type="file" accept=".yaml,.yml" onChange={handleCredsConfigFile} />
                    {credsConfigFileName && <span className="form-hint">Loaded {credsConfigFileName} — values below override any matching entry</span>}
                  </div>
                </div>
              </div>

              {/* Dependency credentials (devuser/devpass) */}
              {showCredentials && devCreds.filter(c => c.dep_type !== 'secret').length > 0 && (
                <div className="card" style={{ marginBottom: 16 }}>
                  <div className="card-header">
                    <span className="card-icon">◈</span>
                    <h3>Dependency Credentials</h3>
                    {cachedAt && (
                      <span className="tag" style={{ marginLeft: 'auto', fontSize: 11 }}>
                        Cached {new Date(cachedAt).toLocaleDateString()}
                      </span>
                    )}
                  </div>
                  <div className="card-body">
                    <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--warning-border, #a8860044)', fontSize: 13 }}>
                      <strong>⚠ Dev credentials detected.</strong> Your dependencies use default dev values (e.g. <code>devuser/devpass</code>).
                      Enter staging connection strings below or they'll be deployed with dev defaults.
                    </div>
                    {devCreds.filter(c => c.dep_type !== 'secret').map(cred => (
                      <div key={cred.env_var} className="form-group" style={{ marginTop: 12 }}>
                        <label className="form-label">
                          <span className="mono" style={{ fontWeight: 600 }}>{cred.env_var}</span>
                          <span className="text-dim" style={{ marginLeft: 8, fontSize: 12 }}>({cred.dep_type})</span>
                        </label>
                        <input
                          className="form-input"
                          type="password"
                          placeholder={cred.dev_value}
                          value={credValues[cred.env_var] || ''}
                          onChange={e => setCredValues(prev => ({ ...prev, [cred.env_var]: e.target.value }))}
                        />
                        <span className="form-hint">
                          Used by: {cred.services.join(', ')}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* User secrets (secretKeyRef) */}
              {showCredentials && devCreds.filter(c => c.dep_type === 'secret').length > 0 && (
                <div className="card" style={{ marginBottom: 16 }}>
                  <div className="card-header">
                    <span className="card-icon">🔑</span>
                    <h3>Secrets</h3>
                  </div>
                  <div className="card-body">
                    <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--info-border, #4488cc44)', fontSize: 13 }}>
                      These values are pre-filled from your dev cluster secrets.
                      Accept the defaults or enter staging-specific values.
                      Values like <code>PUBLIC_URL</code> can be updated after deploy once TLS is configured.
                    </div>
                    {devCreds.filter(c => c.dep_type === 'secret').map(cred => (
                      <div key={cred.env_var} className="form-group" style={{ marginTop: 12 }}>
                        <label className="form-label">
                          <span className="mono" style={{ fontWeight: 600 }}>{cred.env_var}</span>
                          {cred.env_var === 'PUBLIC_URL' && (
                            <span className="tag" style={{ marginLeft: 8, fontSize: 10 }}>update after TLS</span>
                          )}
                        </label>
                        <input
                          className="form-input"
                          placeholder={cred.dev_value}
                          value={credValues[cred.env_var] || ''}
                          onChange={e => setCredValues(prev => ({ ...prev, [cred.env_var]: e.target.value }))}
                        />
                        <span className="form-hint">
                          Used by: {cred.services.join(', ')}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                <button
                  className="btn btn-primary"
                  disabled={!registry || services.length === 0}
                  onClick={startDeploy}
                >
                  Deploy to Staging
                </button>
              </div>
            </>
          )}
        </>
      )}

      {/* Deploy progress */}
      {(step === 'deploying' || step === 'done') && (
        <div className="card">
          <div className="card-header">
            <span className="card-icon">{step === 'deploying' ? '◔' : logs.some(l => l.type === 'error') ? '✗' : '✓'}</span>
            <h3>{step === 'deploying' ? 'Deploying…' : logs.some(l => l.type === 'error') ? 'Deploy Failed' : 'Deploy Complete'}</h3>
          </div>
          <div className="card-body" style={{ padding: 0 }}>
            <div ref={logRef} className="deploy-log">
              {logs.map((log, i) => (
                <div key={i} className={`deploy-log-line deploy-log-${log.type}`}>
                  <span className="deploy-log-icon">
                    {log.type === 'step' ? '→' : log.type === 'error' ? '✗' : '✓'}
                  </span>
                  <span>{log.message}</span>
                </div>
              ))}
              {step === 'deploying' && (
                <div className="deploy-log-line deploy-log-step">
                  <span className="deploy-log-icon deploy-log-spinner">◌</span>
                  <span className="text-dim">Working…</span>
                </div>
              )}
            </div>
          </div>
          {step === 'done' && prodValuesYAML && (
            <details style={{ borderTop: '1px solid var(--border)' }}>
              <summary style={{ cursor: 'pointer', userSelect: 'none', padding: '10px 16px', fontSize: 13, fontWeight: 600 }}>
                values-prod.yaml
              </summary>
              <pre className="log-output" style={{ margin: '0 16px 12px', maxHeight: 300, overflow: 'auto', fontSize: 12 }}>{prodValuesYAML}</pre>
            </details>
          )}
          {step === 'done' && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              {prodValuesYAML && (
                <button className="btn btn-secondary" onClick={downloadProdValues}>
                  ⬇ Download values-prod.yaml
                </button>
              )}
              <button className="btn btn-secondary" onClick={reset}>
                ← Configure Another Deploy
              </button>
            </div>
          )}
        </div>
      )}

      {/* External access — the ingress controller's address for DNS/TLS setup */}
      {step === 'done' && !logs.some(l => l.type === 'error') && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-header">
            <span className="card-icon">🌐</span>
            <h3>External Access</h3>
          </div>
          <div className="card-body">
            {!ingressInfo?.external_ip && !ingressLookupFailed && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }} className="text-dim">
                <span className="deploy-log-icon deploy-log-spinner">◌</span>
                Waiting for the ingress controller's external IP (cloud load balancers can take a minute or two to provision)…
              </div>
            )}

            {!ingressInfo?.external_ip && ingressLookupFailed && (
              <div style={{ padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--error-border, #cc444444)', fontSize: 13 }}>
                ✗ Could not determine an external IP yet. Run <code>kubectl get svc -n traefik --context {status?.context}</code> from
                the CLI, or click Refresh below once the ingress controller has an address.
                <div style={{ marginTop: 8 }}>
                  <button className="btn btn-secondary" onClick={() => { setIngressLookupFailed(false); fetchStagingIngressController().then(setIngressInfo).catch(() => setIngressLookupFailed(true)); }}>
                    Refresh
                  </button>
                </div>
              </div>
            )}

            {ingressInfo?.external_ip && (
              <>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
                  <span className="mono" style={{ fontSize: 16, fontWeight: 600 }}>{ingressInfo.external_ip}</span>
                  <span className="tag" style={{ fontSize: 10 }}>load balancer</span>
                </div>
                <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--info-border, #4488cc44)', fontSize: 13 }}>
                  Create a DNS A record pointing your domain at this address, then configure TLS:
                  <pre className="staging-code-block" style={{ marginTop: 8 }}>
{`<your-domain>  →  ${ingressInfo.external_ip}

kindling staging tls --context ${status?.context} \\
  --domain <your-domain> --email <you@example.com>`}
                  </pre>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* Post-deploy secret update */}
      {step === 'done' && !logs.some(l => l.type === 'error') && devCreds.filter(c => c.dep_type === 'secret').length > 0 && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-header">
            <span className="card-icon">🔑</span>
            <h3>Update Secrets</h3>
          </div>
          <div className="card-body">
            <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--info-border, #4488cc44)', fontSize: 13 }}>
              Update secrets on the running staging cluster (e.g. set <code>PUBLIC_URL</code> after configuring TLS).
              Changed secrets will be patched in-place and affected deployments will be restarted automatically.
            </div>
            {devCreds.filter(c => c.dep_type === 'secret').map(cred => (
              <div key={cred.env_var} className="form-group" style={{ marginTop: 12 }}>
                <label className="form-label">
                  <span className="mono" style={{ fontWeight: 600 }}>{cred.env_var}</span>
                  {cred.env_var === 'PUBLIC_URL' && (
                    <span className="tag" style={{ marginLeft: 8, fontSize: 10 }}>set your staging URL</span>
                  )}
                </label>
                <input
                  className="form-input"
                  placeholder={credValues[cred.env_var] || cred.dev_value}
                  value={postDeploySecretValues[cred.env_var] || ''}
                  onChange={e => setPostDeploySecretValues(prev => ({ ...prev, [cred.env_var]: e.target.value }))}
                />
                <span className="form-hint">
                  Current: {credValues[cred.env_var] || cred.dev_value} · Used by: {cred.services.join(', ')}
                </span>
              </div>
            ))}
            {secretUpdateStatus === 'saved' && (
              <div style={{ marginTop: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--success-border, #44cc4444)', fontSize: 13 }}>
                ✓ {secretUpdateMsg}
              </div>
            )}
            {secretUpdateStatus === 'error' && (
              <div style={{ marginTop: 12, padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: 6, border: '1px solid var(--error-border, #cc444444)', fontSize: 13 }}>
                ✗ {secretUpdateMsg}
              </div>
            )}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 16 }}>
              <button
                className="btn btn-primary"
                disabled={secretUpdateStatus === 'saving' || Object.values(postDeploySecretValues).every(v => !v.trim())}
                onClick={async () => {
                  const creds: Record<string, string> = {};
                  for (const [k, v] of Object.entries(postDeploySecretValues)) {
                    if (v.trim()) creds[k] = v.trim();
                  }
                  if (Object.keys(creds).length === 0) return;
                  setSecretUpdateStatus('saving');
                  try {
                    const res = await updateStagingSecrets({ namespace, credentials: creds });
                    setSecretUpdateStatus('saved');
                    const restartedMsg = res.restarted?.length ? ` Restarted: ${res.restarted.join(', ')}` : '';
                    setSecretUpdateMsg(`Updated ${res.updated} secret(s).${restartedMsg}`);
                    // Update the credValues so the "Current" hints reflect the new values
                    setCredValues(prev => ({ ...prev, ...creds }));
                    setPostDeploySecretValues({});
                  } catch (err) {
                    setSecretUpdateStatus('error');
                    setSecretUpdateMsg(err instanceof Error ? err.message : 'Failed to update secrets');
                  }
                }}
              >
                {secretUpdateStatus === 'saving' ? 'Updating…' : 'Update Secrets & Restart'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

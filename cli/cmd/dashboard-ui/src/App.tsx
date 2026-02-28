import { useState, useEffect } from 'react';
import type { ReactNode } from 'react';
import { ToastProvider, ActionModal, ConfirmDialog, useToast, ResultOutput } from './pages/actions';
import { useApi, apiPost, apiDelete, fetchExposeStatus, streamInit, streamGenerate } from './api';
import type { ActionResult, GenerateResult } from './api';
import { OverviewPage } from './pages/OverviewPage';
import { DSEPage } from './pages/DSEPage';
import { RunnersPage } from './pages/RunnersPage';
import { DeploymentsPage } from './pages/DeploymentsPage';
import { PodsPage } from './pages/PodsPage';
import { ServicesPage } from './pages/ServicesPage';
import { IngressesPage } from './pages/IngressesPage';
import { SecretsPage } from './pages/SecretsPage';
import { EventsPage } from './pages/EventsPage';
import { RBACPage } from './pages/RBACPage';
import type { K8sList, K8sIngress } from './types';

type Page =
  | 'overview'
  | 'dses'
  | 'runners'
  | 'deployments'
  | 'pods'
  | 'services'
  | 'ingresses'
  | 'secrets'
  | 'events'
  | 'rbac';

interface NavGroup {
  label: string;
  items: { page: Page; icon: string; label: string }[];
}

const NAV_GROUPS: NavGroup[] = [
  {
    label: 'Cluster',
    items: [
      { page: 'overview', icon: '⬡', label: 'Overview' },
      { page: 'events', icon: '⚡', label: 'Events' },
    ],
  },
  {
    label: 'Kindling',
    items: [
      { page: 'dses', icon: '◆', label: 'Environments' },
      { page: 'runners', icon: '▶', label: 'Runners' },
    ],
  },
  {
    label: 'Workloads',
    items: [
      { page: 'deployments', icon: '□', label: 'Deployments' },
      { page: 'pods', icon: '○', label: 'Pods' },
    ],
  },
  {
    label: 'Network',
    items: [
      { page: 'services', icon: '◎', label: 'Services' },
      { page: 'ingresses', icon: '⊕', label: 'Ingresses' },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { page: 'secrets', icon: '◈', label: 'Secrets' },
    ],
  },
  {
    label: 'Access Control',
    items: [
      { page: 'rbac', icon: '⊘', label: 'RBAC' },
    ],
  },
];

const PAGES: Record<Page, () => ReactNode> = {
  overview: OverviewPage,
  dses: DSEPage,
  runners: RunnersPage,
  deployments: DeploymentsPage,
  pods: PodsPage,
  services: ServicesPage,
  ingresses: IngressesPage,
  secrets: SecretsPage,
  events: EventsPage,
  rbac: RBACPage,
};

function App() {
  const [activePage, setActivePage] = useState<Page>('overview');
  const ActiveComponent = PAGES[activePage];

  return (
    <ToastProvider>
      <div className="app">
        <AppSidebar activePage={activePage} setActivePage={setActivePage} />
        <main className="main-content">
          <ActiveComponent />
        </main>
      </div>
    </ToastProvider>
  );
}

// ── Command Menu (popover from sidebar) ─────────────────────────

function CommandMenu({ onClose, onAction }: {
  onClose: () => void;
  onAction: (action: string) => void;
}) {
  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [onClose]);

  return (
    <>
      <div className="cmd-menu-backdrop" onClick={onClose} />
      <div className="cmd-menu">
        <div className="cmd-menu-header">Commands</div>

        <div className="cmd-menu-group">
          <div className="cmd-menu-group-label">Lifecycle</div>
          <button className="cmd-item" onClick={() => onAction('init')}>
            <span className="cmd-item-icon i-green">⚡</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Init Cluster</div>
              <div className="cmd-item-desc">Bootstrap Kind + operator</div>
            </span>
          </button>
          <button className="cmd-item" onClick={() => onAction('destroy')}>
            <span className="cmd-item-icon i-red">✕</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Destroy Cluster</div>
              <div className="cmd-item-desc">Tear down everything</div>
            </span>
          </button>
        </div>

        <div className="cmd-menu-group">
          <div className="cmd-menu-group-label">Deploy</div>
          <button className="cmd-item" onClick={() => onAction('deploy')}>
            <span className="cmd-item-icon i-blue">▲</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Deploy Environment</div>
              <div className="cmd-item-desc">Apply a DevStagingEnvironment YAML</div>
            </span>
          </button>
          <button className="cmd-item" onClick={() => onAction('apply')}>
            <span className="cmd-item-icon i-purple">⎘</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Apply YAML</div>
              <div className="cmd-item-desc">Run kubectl apply with raw YAML</div>
            </span>
          </button>
          <button className="cmd-item" onClick={() => onAction('generate')}>
            <span className="cmd-item-icon i-cyan">✦</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Generate Workflow</div>
              <div className="cmd-item-desc">AI-generate a CI workflow from your repo</div>
            </span>
          </button>
        </div>

        <div className="cmd-menu-group">
          <div className="cmd-menu-group-label">Network</div>
          <button className="cmd-item" onClick={() => onAction('expose')}>
            <span className="cmd-item-icon i-cyan">↗</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Expose / Tunnel</div>
              <div className="cmd-item-desc">Public HTTPS tunnel for OAuth</div>
            </span>
          </button>
        </div>

        <div className="cmd-menu-group">
          <div className="cmd-menu-group-label">Manage</div>
          <button className="cmd-item" onClick={() => onAction('secret')}>
            <span className="cmd-item-icon i-orange">◈</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Create Secret</div>
              <div className="cmd-item-desc">Store an external secret</div>
            </span>
          </button>
          <button className="cmd-item" onClick={() => onAction('runner')}>
            <span className="cmd-item-icon i-green">▶</span>
            <span className="cmd-item-text">
              <div className="cmd-item-label">Create Runner</div>
              <div className="cmd-item-desc">Register a GitHub Actions runner</div>
            </span>
          </button>
        </div>

        <div className="cmd-menu-footer">
          <kbd>esc</kbd> to close
        </div>
      </div>
    </>
  );
}

// ── Expose Modal (ingress picker) ───────────────────────────────

function ExposeModal({ running, onStart, onStop, onClose }: {
  running: boolean;
  onStart: (service?: string) => void;
  onStop: () => void;
  onClose: () => void;
}) {
  const { data } = useApi<K8sList<K8sIngress>>('/api/ingresses');
  const [selected, setSelected] = useState('');

  const ingresses = (data?.items || []).filter(
    (i) => i.metadata.namespace !== 'kube-system' && i.metadata.namespace !== 'ingress-nginx'
  );

  if (running) {
    return (
      <ActionModal title="Stop Tunnel" submitLabel="Stop Tunnel" onSubmit={onStop} onClose={onClose}>
        <p style={{ color: 'var(--text-secondary)', fontSize: 13 }}>
          A tunnel is currently active. Stopping it will restore the original ingress hosts.
        </p>
      </ActionModal>
    );
  }

  return (
    <ActionModal
      title="Expose / Tunnel"
      submitLabel="Start Tunnel"
      onSubmit={() => onStart(selected || undefined)}
      onClose={onClose}
    >
      <p style={{ color: 'var(--text-secondary)', fontSize: 13, marginBottom: 12 }}>
        Creates a public HTTPS tunnel via Cloudflare and patches the selected ingress host to route through it.
      </p>
      <label className="form-label">Target Ingress</label>
      {ingresses.length === 0 ? (
        <p style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
          No ingresses found — the tunnel will start but no ingress will be patched.
        </p>
      ) : (
        <select
          className="form-input"
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
        >
          <option value="">All ingresses (first match)</option>
          {ingresses.map((ing) => (
            <option key={`${ing.metadata.namespace}/${ing.metadata.name}`} value={ing.metadata.name}>
              {ing.metadata.name}
              {ing.spec.rules?.[0]?.host ? ` (${ing.spec.rules[0].host})` : ''}
            </option>
          ))}
        </select>
      )}
    </ActionModal>
  );
}

// ── Sidebar ─────────────────────────────────────────────────────

function AppSidebar({ activePage, setActivePage }: { activePage: Page; setActivePage: (p: Page) => void }) {
  const { toast } = useToast();

  const [cmdOpen, setCmdOpen] = useState(false);

  // ── modal state ───────────────────────────────────
  const [showDeploy, setShowDeploy] = useState(false);
  const [showApply, setShowApply] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [showRunner, setShowRunner] = useState(false);
  const [showDestroy, setShowDestroy] = useState(false);
  const [showExpose, setShowExpose] = useState(false);

  const [deployYaml, setDeployYaml] = useState('');
  const [applyYaml, setApplyYaml] = useState('');
  const [deploying, setDeploying] = useState(false);
  const [applying, setApplying] = useState(false);

  const [secretForm, setSecretForm] = useState({ name: '', namespace: 'default', key: '', value: '' });
  const [creatingSec, setCreatingSec] = useState(false);

  const [runnerForm, setRunnerForm] = useState({ username: '', repo: '', token: '' });
  const [creatingRun, setCreatingRun] = useState(false);

  // ── init cluster ──────────────────────────────────
  const [initRunning, setInitRunning] = useState(false);
  const [showInit, setShowInit] = useState(false);
  const [initMessages, setInitMessages] = useState<string[]>([]);
  const [initResult, setInitResult] = useState<ActionResult | null>(null);

  // ── expose / tunnel ───────────────────────────────
  const [tunnelStatus, setTunnelStatus] = useState<{ running: boolean; url?: string; dns_ready?: boolean } | null>(null);

  // ── generate workflow ─────────────────────────────
  const [showGenerate, setShowGenerate] = useState(false);
  const [generateRunning, setGenerateRunning] = useState(false);
  const [generateMessages, setGenerateMessages] = useState<string[]>([]);
  const [generateResult, setGenerateResult] = useState<GenerateResult | null>(null);
  const [generateForm, setGenerateForm] = useState({ apiKey: '', provider: 'openai', model: '', ciProvider: 'github', branch: '' });
  useEffect(() => {
    fetchExposeStatus().then(setTunnelStatus).catch(() => {});
    const interval = setInterval(() => {
      fetchExposeStatus().then(setTunnelStatus).catch(() => {});
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  // ── keyboard shortcut ─────────────────────────────
  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setCmdOpen(prev => !prev);
      }
    }
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, []);

  function handleAction(action: string) {
    setCmdOpen(false);
    switch (action) {
      case 'init': handleInit(); break;
      case 'destroy': setShowDestroy(true); break;
      case 'deploy': setShowDeploy(true); break;
      case 'apply': setShowApply(true); break;
      case 'expose': setShowExpose(true); break;
      case 'secret': setShowSecret(true); break;
      case 'runner': setShowRunner(true); break;
      case 'generate': setShowGenerate(true); break;
    }
  }

  async function handleDeploy() {
    setDeploying(true);
    const result = await apiPost('/api/deploy', { yaml: deployYaml });
    setDeploying(false);
    if (result.ok) {
      toast(result.output || 'Deployed successfully', 'success');
      setShowDeploy(false);
      setDeployYaml('');
    } else {
      toast(result.error || 'Deploy failed', 'error');
    }
  }

  async function handleApply() {
    setApplying(true);
    const result = await apiPost('/api/apply', { yaml: applyYaml });
    setApplying(false);
    if (result.ok) {
      toast(result.output || 'Applied successfully', 'success');
      setShowApply(false);
      setApplyYaml('');
    } else {
      toast(result.error || 'Apply failed', 'error');
    }
  }

  async function handleCreateSecret() {
    setCreatingSec(true);
    const result = await apiPost('/api/secrets/create', secretForm);
    setCreatingSec(false);
    if (result.ok) {
      toast('Secret created', 'success');
      setShowSecret(false);
      setSecretForm({ name: '', namespace: 'default', key: '', value: '' });
    } else {
      toast(result.error || 'Failed to create secret', 'error');
    }
  }

  async function handleCreateRunner() {
    setCreatingRun(true);
    const result = await apiPost('/api/runners/create', runnerForm);
    setCreatingRun(false);
    if (result.ok) {
      toast('Runner pool created', 'success');
      setShowRunner(false);
      setRunnerForm({ username: '', repo: '', token: '' });
    } else {
      toast(result.error || 'Failed to create runner', 'error');
    }
  }

  async function toggleTunnel(service?: string) {
    if (tunnelStatus?.running) {
      const result = await apiDelete('/api/expose');
      if (result.ok) {
        toast('Tunnel stopped', 'success');
        setTunnelStatus({ running: false });
      } else {
        toast(result.error || 'Failed to stop tunnel', 'error');
      }
    } else {
      const result = await apiPost('/api/expose', service ? { service } : {});
      if (result.ok) {
        toast(result.output || 'Tunnel started', 'success');
        fetchExposeStatus().then(setTunnelStatus);
      } else {
        toast(result.error || 'Failed to start tunnel', 'error');
      }
    }
  }

  async function handleInit() {
    setShowInit(true);
    setInitRunning(true);
    setInitMessages([]);
    setInitResult(null);
    const result = await streamInit((msg) => setInitMessages((m) => [...m, msg]));
    setInitResult(result);
    setInitRunning(false);
    if (result.ok) toast('Cluster initialized', 'success');
    else toast(result.error || 'Init failed', 'error');
  }

  async function handleDestroy() {
    setShowDestroy(false);
    const result = await apiDelete('/api/cluster/destroy');
    if (result.ok) toast('Cluster destroyed', 'success');
    else toast(result.error || 'Destroy failed', 'error');
  }

  async function handleGenerate() {
    setGenerateRunning(true);
    setGenerateMessages([]);
    setGenerateResult(null);
    const result = await streamGenerate(
      {
        apiKey: generateForm.apiKey,
        provider: generateForm.provider || undefined,
        model: generateForm.model || undefined,
        ciProvider: generateForm.ciProvider || undefined,
        branch: generateForm.branch || undefined,
      },
      (msg) => setGenerateMessages((m) => [...m, msg]),
    );
    setGenerateResult(result);
    setGenerateRunning(false);
    if (result.ok) toast(result.output || 'Workflow generated', 'success');
    else toast(result.error || 'Generation failed', 'error');
  }

  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        <span className="brand-icon">🔥</span>
        <span className="brand-text">kindling</span>
      </div>
      <nav className="sidebar-nav">
        {NAV_GROUPS.map((group) => (
          <div key={group.label}>
            <div className="nav-group-label">{group.label}</div>
            {group.items.map((item) => (
              <button
                key={item.page}
                className={`nav-item ${activePage === item.page ? 'active' : ''}`}
                onClick={() => setActivePage(item.page)}
              >
                <span className="nav-icon">{item.icon}</span>
                <span className="nav-label">{item.label}</span>
              </button>
            ))}
          </div>
        ))}
      </nav>

      {/* ── Command trigger ──────────────── */}
      <div style={{ padding: '12px 0 0' }}>
        <button className="cmd-trigger" onClick={() => setCmdOpen(!cmdOpen)}>
          <span className="cmd-trigger-icon">⌘</span>
          <span className="cmd-trigger-label">Commands</span>
          <span className="cmd-trigger-hint">⌘K</span>
        </button>
      </div>

      {cmdOpen && (
        <CommandMenu onClose={() => setCmdOpen(false)} onAction={handleAction} />
      )}

      <div className="sidebar-footer">
        {tunnelStatus?.running && tunnelStatus.url && (
          <div className="tunnel-widget" style={!tunnelStatus.dns_ready ? { background: 'var(--amber-muted, rgba(255,193,7,0.08))', borderColor: 'color-mix(in srgb, var(--amber, #ffc107) 25%, transparent)' } : undefined}>
            <div className="tunnel-widget-header">
              <span className={tunnelStatus.dns_ready ? 'tunnel-pulse' : 'tunnel-pulse tunnel-pulse-amber'} />
              <span className="tunnel-widget-label" style={!tunnelStatus.dns_ready ? { color: 'var(--amber, #ffc107)' } : undefined}>
                {tunnelStatus.dns_ready ? 'Tunnel Active' : 'DNS Propagating…'}
              </span>
            </div>
            {tunnelStatus.dns_ready ? (
              <a href={tunnelStatus.url} target="_blank" rel="noopener" className="tunnel-widget-url">
                {tunnelStatus.url.replace('https://', '')}
              </a>
            ) : (
              <span className="tunnel-widget-url" style={{ opacity: 0.6, cursor: 'default' }}>
                {tunnelStatus.url.replace('https://', '')}
              </span>
            )}
            <div style={{ display: 'flex', gap: 6 }}>
              <button
                className="tunnel-copy-btn"
                onClick={() => {
                  navigator.clipboard.writeText(tunnelStatus.url!);
                  toast('URL copied', 'success');
                }}
              >
                Copy URL
              </button>
              <button
                className="tunnel-copy-btn"
                style={{ background: 'var(--danger)', color: '#fff' }}
                onClick={() => toggleTunnel()}
              >
                Stop
              </button>
            </div>
          </div>
        )}
        <span className="version">kindling dashboard v0.1</span>
      </div>

      {/* ── Deploy modal ─────────────────── */}
      {showExpose && (
        <ExposeModal
          running={tunnelStatus?.running ?? false}
          onStart={(service) => { setShowExpose(false); toggleTunnel(service); }}
          onStop={() => { setShowExpose(false); toggleTunnel(); }}
          onClose={() => setShowExpose(false)}
        />
      )}

      {/* ── Deploy Environment modal ─────── */}
      {showDeploy && (
        <ActionModal title="Deploy Environment" submitLabel="Deploy" loading={deploying} onSubmit={handleDeploy} onClose={() => setShowDeploy(false)}>
          <label className="form-label">DevStagingEnvironment YAML</label>
          <textarea className="form-textarea" rows={14} placeholder={"apiVersion: apps.kindling.dev/v1alpha1\nkind: DevStagingEnvironment\nmetadata:\n  name: my-app\nspec:\n  ..."} value={deployYaml} onChange={(e) => setDeployYaml(e.target.value)} />
        </ActionModal>
      )}

      {/* ── Apply YAML modal ─────────────── */}
      {showApply && (
        <ActionModal title="Apply YAML" submitLabel="Apply" loading={applying} onSubmit={handleApply} onClose={() => setShowApply(false)}>
          <label className="form-label">Raw YAML to apply via kubectl</label>
          <textarea className="form-textarea" rows={14} placeholder={"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-config\n  ..."} value={applyYaml} onChange={(e) => setApplyYaml(e.target.value)} />
        </ActionModal>
      )}

      {/* ── Create Secret modal ──────────── */}
      {showSecret && (
        <ActionModal title="Create Secret" submitLabel="Create" loading={creatingSec} onSubmit={handleCreateSecret} onClose={() => setShowSecret(false)}>
          <label className="form-label">Name</label>
          <input className="form-input" placeholder="my-secret" value={secretForm.name} onChange={(e) => setSecretForm({ ...secretForm, name: e.target.value })} />
          <label className="form-label">Namespace</label>
          <input className="form-input" placeholder="default" value={secretForm.namespace} onChange={(e) => setSecretForm({ ...secretForm, namespace: e.target.value })} />
          <label className="form-label">Key</label>
          <input className="form-input" placeholder="SECRET_KEY" value={secretForm.key} onChange={(e) => setSecretForm({ ...secretForm, key: e.target.value })} />
          <label className="form-label">Value</label>
          <input className="form-input" type="password" placeholder="secret value" value={secretForm.value} onChange={(e) => setSecretForm({ ...secretForm, value: e.target.value })} />
        </ActionModal>
      )}

      {/* ── Create Runner modal ──────────── */}
      {showRunner && (
        <ActionModal title="Create Runner Pool" submitLabel="Create" loading={creatingRun} onSubmit={handleCreateRunner} onClose={() => setShowRunner(false)}>
          <label className="form-label">GitHub Username</label>
          <input className="form-input" placeholder="your-username" value={runnerForm.username} onChange={(e) => setRunnerForm({ ...runnerForm, username: e.target.value })} />
          <label className="form-label">Repository (owner/repo)</label>
          <input className="form-input" placeholder="owner/repo" value={runnerForm.repo} onChange={(e) => setRunnerForm({ ...runnerForm, repo: e.target.value })} />
          <label className="form-label">GitHub PAT</label>
          <input className="form-input" type="password" placeholder="ghp_..." value={runnerForm.token} onChange={(e) => setRunnerForm({ ...runnerForm, token: e.target.value })} />
        </ActionModal>
      )}

      {/* ── Generate Workflow modal ────── */}
      {showGenerate && (
        <div className="modal-overlay" onClick={generateRunning ? undefined : () => setShowGenerate(false)}>
          <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Generate CI Workflow</h3>
              {!generateRunning && <button className="panel-close" onClick={() => setShowGenerate(false)}>✕</button>}
            </div>
            <div className="modal-body">
              {!generateRunning && !generateResult && (
                <>
                  <label className="form-label">API Key <span style={{ color: 'var(--danger)' }}>*</span></label>
                  <input className="form-input" type="password" placeholder="sk-..." value={generateForm.apiKey} onChange={(e) => setGenerateForm({ ...generateForm, apiKey: e.target.value })} />

                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 4 }}>
                    <div>
                      <label className="form-label">AI Provider</label>
                      <select className="form-input" value={generateForm.provider} onChange={(e) => setGenerateForm({ ...generateForm, provider: e.target.value })}>
                        <option value="openai">OpenAI</option>
                        <option value="anthropic">Anthropic</option>
                      </select>
                    </div>
                    <div>
                      <label className="form-label">Model <span style={{ color: 'var(--text-tertiary)', fontSize: 11 }}>(optional)</span></label>
                      <input className="form-input" placeholder={generateForm.provider === 'anthropic' ? 'claude-sonnet-4-20250514' : 'o3'} value={generateForm.model} onChange={(e) => setGenerateForm({ ...generateForm, model: e.target.value })} />
                    </div>
                  </div>

                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 4 }}>
                    <div>
                      <label className="form-label">CI Provider</label>
                      <select className="form-input" value={generateForm.ciProvider} onChange={(e) => setGenerateForm({ ...generateForm, ciProvider: e.target.value })}>
                        <option value="github">GitHub Actions</option>
                        <option value="gitlab">GitLab CI</option>
                      </select>
                    </div>
                    <div>
                      <label className="form-label">Branch <span style={{ color: 'var(--text-tertiary)', fontSize: 11 }}>(auto-detect)</span></label>
                      <input className="form-input" placeholder="main" value={generateForm.branch} onChange={(e) => setGenerateForm({ ...generateForm, branch: e.target.value })} />
                    </div>
                  </div>
                </>
              )}

              {(generateMessages.length > 0 || generateResult) && (
                <div style={{ marginTop: generateRunning || generateResult ? 0 : 12 }}>
                  <div className="log-viewer" style={{ marginBottom: 12 }}>
                    <pre className="log-output">{generateMessages.map((m, i) => <div key={i}>{m}</div>)}</pre>
                  </div>
                  <ResultOutput result={generateResult} />
                  {generateResult?.workflow && (
                    <details style={{ marginTop: 8 }}>
                      <summary style={{ cursor: 'pointer', color: 'var(--text-secondary)', fontSize: 13 }}>View generated workflow</summary>
                      <pre className="log-output" style={{ marginTop: 8, maxHeight: 300, overflow: 'auto', fontSize: 11 }}>{generateResult.workflow}</pre>
                    </details>
                  )}
                </div>
              )}

              {generateRunning && generateMessages.length === 0 && (
                <p style={{ color: 'var(--text-tertiary)' }}>Scanning repository…</p>
              )}
            </div>
            <div className="modal-footer">
              {!generateRunning && !generateResult && (
                <button className="btn btn-primary" disabled={!generateForm.apiKey} onClick={handleGenerate}>Generate</button>
              )}
              {!generateRunning && (
                <button className="btn" onClick={() => {
                  setShowGenerate(false);
                  setGenerateMessages([]);
                  setGenerateResult(null);
                }}>
                  {generateResult ? 'Close' : 'Cancel'}
                </button>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ── Init progress modal ──────────── */}
      {showInit && (
        <div className="modal-overlay" onClick={initRunning ? undefined : () => setShowInit(false)}>
          <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Initialize Cluster</h3>
              {!initRunning && <button className="panel-close" onClick={() => setShowInit(false)}>✕</button>}
            </div>
            <div className="modal-body">
              {initMessages.length > 0 && (
                <div className="log-viewer" style={{ marginBottom: 12 }}>
                  <pre className="log-output">{initMessages.map((m, i) => <div key={i}>{m}</div>)}</pre>
                </div>
              )}
              {initRunning && initMessages.length === 0 && (
                <p style={{ color: 'var(--text-tertiary)' }}>Starting cluster initialization…</p>
              )}
              <ResultOutput result={initResult} />
            </div>
            {!initRunning && (
              <div className="modal-footer">
                <button className="btn" onClick={() => setShowInit(false)}>Close</button>
              </div>
            )}
          </div>
        </div>
      )}

      {/* ── Destroy confirm ──────────────── */}
      {showDestroy && (
        <ConfirmDialog
          title="Destroy Cluster"
          message="This will permanently delete the Kind cluster and all resources. This cannot be undone."
          confirmLabel="Destroy"
          danger
          onConfirm={handleDestroy}
          onCancel={() => setShowDestroy(false)}
        />
      )}
    </aside>
  );
}

export default App;

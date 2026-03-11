import { useState } from 'react';
import { useApi, streamInit, activateIntel, deactivateIntel } from '../api';
import type { ActionResult } from '../api';
import type { ClusterInfo, K8sList, K8sNode, K8sPod, K8sDeployment, IntelStatus } from '../types';
import { StatusBadge, DeploymentStatus, TimeAgo } from './shared';
import { ActionButton, ResultOutput, useToast } from './actions';

/* ── Path tile definitions ─────────────────────────────────── */

interface PathTile {
  id: string;
  icon: string;
  title: string;
  desc: string;
  color: string;
  navigateTo: string;
  features: string[];
}

const PATHS: PathTile[] = [
  {
    id: 'setup',
    icon: '△',
    title: 'Setup',
    desc: 'Get your local cluster running, design your app, and register CI runners — everything to go from zero to deploying.',
    color: '#f97316',
    navigateTo: 'topology',
    features: ['Init cluster & operator', 'Visual app designer', 'Analyze repo readiness', 'Generate CI workflow'],
  },
  {
    id: 'develop',
    icon: '⬡',
    title: 'Develop',
    desc: 'Build, sync, debug, and test your services. Everything you need for your inner dev loop.',
    color: '#3b82f6',
    navigateTo: 'dses',
    features: ['Live sync & hot reload', 'API explorer & testing', 'Logs & debugging', 'Deployment management'],
  },
  {
    id: 'cicd',
    icon: '⊞',
    title: 'CI / CD',
    desc: 'Generate CI workflows with AI. Push code and watch your runners build, test, and deploy automatically.',
    color: '#8b5cf6',
    navigateTo: 'runners',
    features: ['AI workflow generation', 'Push to trigger CI', 'Build & test locally', 'Automated deploys'],
  },
  {
    id: 'production',
    icon: '◎',
    title: 'Production',
    desc: 'Snapshot your dev environment and deploy to a real cluster. Monitor, scale, and manage TLS.',
    color: '#10b981',
    navigateTo: 'prod-overview',
    features: ['One-click deploy', 'Metrics & monitoring', 'TLS certificates', 'Rollbacks & scaling'],
  },
];

/* ── Component ─────────────────────────────────────────────── */

export function OverviewPage() {
  const { data: cluster, loading: cl, refresh } = useApi<ClusterInfo>('/api/cluster');
  const { data: nodes } = useApi<K8sList<K8sNode>>('/api/nodes');
  const { data: ingressPods } = useApi<K8sList<K8sPod>>('/api/ingress-controller');
  const { data: deployments } = useApi<K8sList<K8sDeployment>>('/api/deployments');
  const { data: allPods } = useApi<K8sList<K8sPod>>('/api/pods');
  const { data: intel, refresh: refreshIntel } = useApi<IntelStatus>('/api/intel', 10000);
  const { toast } = useToast();

  const [initRunning, setInitRunning] = useState(false);
  const [initMessages, setInitMessages] = useState<string[]>([]);
  const [initResult, setInitResult] = useState<ActionResult | null>(null);
  const [intelLoading, setIntelLoading] = useState(false);

  async function handleInit() {
    setInitRunning(true);
    setInitMessages([]);
    setInitResult(null);
    const result = await streamInit((msg) => setInitMessages((m) => [...m, msg]));
    setInitResult(result);
    setInitRunning(false);
    if (result.ok) {
      toast('Cluster initialized', 'success');
      refresh();
    } else {
      toast(result.error || 'Init failed', 'error');
    }
  }

  async function handleIntelToggle() {
    setIntelLoading(true);
    if (intel?.status === 'active') {
      const result = await deactivateIntel();
      if (result.ok) toast('Intel deactivated', 'success');
      else toast(result.error || 'Failed to deactivate intel', 'error');
    } else {
      const result = await activateIntel();
      if (result.ok) toast('Intel activated', 'success');
      else toast(result.error || 'Failed to activate intel', 'error');
    }
    refreshIntel();
    setIntelLoading(false);
  }

  function navigateTo(page: string) {
    window.dispatchEvent(new CustomEvent('navigate', { detail: page }));
  }

  if (cl) return <div className="loading">Loading cluster info…</div>;
  if (!cluster) return <div className="loading">Failed to load cluster info</div>;

  // Compute metrics
  const totalPods = allPods?.items?.length ?? 0;
  const runningPods = allPods?.items?.filter(p => p.status?.phase === 'Running').length ?? 0;
  const totalDeps = deployments?.items?.length ?? 0;
  const readyDeps = deployments?.items?.filter(d => (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 1)).length ?? 0;
  const nodeCount = nodes?.items?.length ?? 0;
  const operatorReady = !!cluster.operator;
  const registryReady = !!cluster.registry;
  const ingressReady = (ingressPods?.items?.length ?? 0) > 0;

  return (
    <div className="page home-page">
      {/* ── Header ─────────────────────────── */}
      <div className="home-header">
        <div className="home-header-text">
          <h1 className="home-title">
            <svg className="home-logo" viewBox="0 0 64 64" width="28" height="28">
              <defs>
                <linearGradient id="hp-fo" x1="0%" y1="100%" x2="50%" y2="0%">
                  <stop offset="0%" stopColor="#FF6B35"/><stop offset="50%" stopColor="#F7931E"/><stop offset="100%" stopColor="#FFD23F"/>
                </linearGradient>
                <linearGradient id="hp-fi" x1="0%" y1="100%" x2="50%" y2="0%">
                  <stop offset="0%" stopColor="#FFD23F"/><stop offset="100%" stopColor="#FFF5CC"/>
                </linearGradient>
                <linearGradient id="hp-lg" x1="0%" y1="0%" x2="0%" y2="100%">
                  <stop offset="0%" stopColor="#8B5E3C"/><stop offset="100%" stopColor="#5C3A1E"/>
                </linearGradient>
              </defs>
              <rect x="14" y="50" width="36" height="5" rx="2.5" fill="url(#hp-lg)" transform="rotate(-8 32 52)"/>
              <rect x="14" y="50" width="36" height="5" rx="2.5" fill="url(#hp-lg)" transform="rotate(8 32 52)"/>
              <path d="M32 4C32 4 14 24 14 36C14 46 22 54 32 54C42 54 50 46 50 36C50 24 32 4 32 4Z" fill="url(#hp-fo)" opacity="0.95"/>
              <path d="M32 14C32 14 20 28 20 37C20 44 25 49 32 49C39 49 44 44 44 37C44 28 32 14 32 14Z" fill="#FFAD33" opacity="0.85"/>
              <path d="M32 24C32 24 25 33 25 38C25 42 28 46 32 46C36 46 39 42 39 38C39 33 32 24 32 24Z" fill="url(#hp-fi)" opacity="0.95"/>
            </svg>
            kindling
          </h1>
          <p className="home-subtitle">Your laptop is your staging environment. Where would you like to go?</p>
        </div>
        {!cluster.exists && (
          <ActionButton icon="▶" label="Init Cluster" onClick={handleInit} disabled={initRunning} primary />
        )}
      </div>

      {/* ── Init progress ──────────────────── */}
      {(initMessages.length > 0 || initResult) && (
        <div className="init-progress" style={{ marginBottom: 20 }}>
          <h3>Initialization Progress</h3>
          <div className="log-output">
            {initMessages.map((m, i) => <div key={i}>{m}</div>)}
          </div>
          <ResultOutput result={initResult} />
        </div>
      )}

      {/* ── Compact health strip ───────────── */}
      {cluster.exists && (
        <div className="health-strip">
          <HealthPill label="Cluster" ok={cluster.exists} value={cluster.exists ? 'Running' : 'Down'} />
          <HealthPill label="Nodes" ok={nodeCount > 0} value={String(nodeCount)} />
          <HealthPill label="Deployments" ok={readyDeps === totalDeps && totalDeps > 0} warn={readyDeps < totalDeps} value={`${readyDeps}/${totalDeps}`} />
          <HealthPill label="Pods" ok={runningPods === totalPods && totalPods > 0} warn={runningPods < totalPods && totalPods > 0} value={`${runningPods}/${totalPods}`} />
          <HealthPill label="Operator" ok={operatorReady} value={operatorReady ? '●' : '○'} />
          <HealthPill label="Registry" ok={registryReady} value={registryReady ? '●' : '○'} />
          <HealthPill label="Ingress" ok={ingressReady} value={ingressReady ? '●' : '○'} />
        </div>
      )}

      {/* ── Path tiles ─────────────────────── */}
      <div className="path-grid">
        {PATHS.map((path) => (
          <button
            key={path.id}
            className="path-tile"
            onClick={() => navigateTo(path.navigateTo)}
            style={{ '--path-color': path.color } as React.CSSProperties}
          >
            <div className="path-tile-header">
              <span className="path-tile-icon">{path.icon}</span>
              <span className="path-tile-title">{path.title}</span>
              <span className="path-tile-arrow">→</span>
            </div>
            <p className="path-tile-desc">{path.desc}</p>
            <ul className="path-tile-features">
              {path.features.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
          </button>
        ))}
      </div>

      {/* ── Infrastructure detail (collapsible) ── */}
      <details className="infra-details">
        <summary className="infra-summary">Infrastructure Details</summary>
        <div className="card-grid card-grid-3" style={{ marginTop: 16 }}>
          <div className="card">
            <div className="card-header">
              <span className="card-icon">⬡</span>
              <h3>Operator</h3>
            </div>
            <div className="card-body">
              {cluster.operator ? (
                <DeploymentStatus dep={cluster.operator} />
              ) : (
                <span className="text-dim" style={{ fontSize: 13 }}>Not installed</span>
              )}
            </div>
          </div>

          <div className="card">
            <div className="card-header">
              <span className="card-icon">◈</span>
              <h3>Registry</h3>
            </div>
            <div className="card-body">
              {cluster.registry ? (
                <DeploymentStatus dep={cluster.registry} />
              ) : (
                <span className="text-dim" style={{ fontSize: 13 }}>Not installed</span>
              )}
            </div>
          </div>

          <div className="card">
            <div className="card-header">
              <span className="card-icon">◎</span>
              <h3>Ingress Controller</h3>
            </div>
            <div className="card-body">
              {ingressPods?.items?.length ? (
                ingressPods.items.map((p) => (
                  <div key={p.metadata.name} className="stat-row" style={{ flexWrap: 'nowrap' }}>
                    <span className="label mono" style={{ fontSize: 11, overflow: 'auto', whiteSpace: 'nowrap', minWidth: 0, flex: '1 1 0' }}>{p.metadata.name}</span>
                    <StatusBadge ok={p.status.phase === 'Running'} label={p.status.phase || 'Unknown'} />
                  </div>
                ))
              ) : (
                <span className="text-dim" style={{ fontSize: 13 }}>No ingress controller</span>
              )}
            </div>
          </div>
        </div>

        {/* Agent Intel */}
        {intel && (
          <div className="card" style={{ marginBottom: 20 }}>
            <div className="card-header">
              <span className="card-icon">◇</span>
              <h3>Agent Intel</h3>
            </div>
            <div className="card-body">
              <div className="stat-row">
                <span className="label">Status</span>
                <StatusBadge
                  ok={intel.status === 'active'}
                  warn={intel.status === 'disabled'}
                  label={intel.status === 'active' ? 'Active' : intel.status === 'disabled' ? 'Disabled' : 'Inactive'}
                />
              </div>
              {intel.status === 'active' && intel.last_interaction && (
                <div className="stat-row">
                  <span className="label">Last interaction</span>
                  <TimeAgo timestamp={intel.last_interaction} />
                </div>
              )}
              {intel.status === 'active' && intel.files && intel.files.length > 0 && (
                <div style={{ marginTop: 8 }}>
                  <span className="label" style={{ display: 'block', marginBottom: 4 }}>Agent files</span>
                  <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                    {intel.files.map((f) => (
                      <span key={f} className="tag">{f}</span>
                    ))}
                  </div>
                </div>
              )}
              <div style={{ marginTop: 12 }}>
                <button
                  className={`btn btn-sm ${intel.status === 'active' ? 'btn-danger' : 'btn-primary'}`}
                  disabled={intelLoading}
                  onClick={handleIntelToggle}
                >
                  {intelLoading ? '…' : intel.status === 'active' ? 'Deactivate' : 'Activate'}
                </button>
              </div>
            </div>
          </div>
        )}

        {/* Nodes table */}
        {nodes?.items && nodes.items.length > 0 && (
          <>
            <h2 className="section-title">Nodes</h2>
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Status</th>
                    <th>Version</th>
                    <th>OS</th>
                    <th>Arch</th>
                    <th>Runtime</th>
                  </tr>
                </thead>
                <tbody>
                  {nodes.items.map((n) => {
                    const ready = n.status.conditions?.find((c) => c.type === 'Ready');
                    return (
                      <tr key={n.metadata.name}>
                        <td className="mono">{n.metadata.name}</td>
                        <td><StatusBadge ok={ready?.status === 'True'} label={ready?.status === 'True' ? 'Ready' : 'NotReady'} /></td>
                        <td className="mono">{n.status.nodeInfo?.kubeletVersion}</td>
                        <td>{n.status.nodeInfo?.osImage}</td>
                        <td>{n.status.nodeInfo?.architecture}</td>
                        <td className="mono">{n.status.nodeInfo?.containerRuntimeVersion}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        )}
      </details>
    </div>
  );
}

/* ── Health Pill (compact inline metric) ─────────────────── */

function HealthPill({ label, ok, warn, value }: {
  label: string;
  ok: boolean;
  warn?: boolean;
  value: string;
}) {
  const dotColor = ok ? 'var(--green)' : warn ? 'var(--yellow)' : 'var(--text-disabled)';
  return (
    <div className="health-pill">
      <span className="health-dot" style={{ background: dotColor }} />
      <span className="health-pill-label">{label}</span>
      <span className="health-pill-value">{value}</span>
    </div>
  );
}

import { useState, useCallback, useRef, useEffect, useMemo, type DragEvent } from 'react';
import {
  ReactFlow,
  Background,
  MiniMap,
  Panel,
  useReactFlow,
  addEdge,
  useNodesState,
  useEdgesState,
  Handle,
  Position,
  ConnectionMode,
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  type EdgeTypes,
  type OnConnect,
  type NodeProps,
  type EdgeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  DEPENDENCY_TYPES,
  DEP_META,
  type DependencyType,
  type TopologyNodeData,
  type TopologyGraph,
  type TopologyStatusMap,
  type TopologyNodeStatus,
  type TopologyNodeDetail,
  type TopologyLogEntry,
} from '../types';
import { fetchTopology, fetchTopologyStatus, fetchTopologyLogs, fetchTopologyNodeDetail, deployTopology, scaffoldService, checkPath, scaleDeployment, fetchWorkspaceInfo, cleanupService, saveCanvas, removeEdgeFromCluster, startDebugSession, stopDebugSession, fetchDebugStatus } from '../api';
import { ActionModal, useToast } from './actions';
import { DEP_ICONS, ServiceIcon, BrowserIcon } from '../icons';

// ── Status helpers ─────────────────────────────────────────────

function timeAgo(iso: string | undefined): string {
  if (!iso) return '';
  const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function phaseColor(phase: string | undefined): string {
  switch (phase) {
    case 'Running': return 'status-running';
    case 'Succeeded': return 'status-running';
    case 'Pending': return 'status-pending';
    case 'CrashLoopBackOff': return 'status-error';
    case 'ImagePullBackOff': return 'status-error';
    case 'Failed': return 'status-error';
    default: return 'status-unknown';
  }
}

function StatusDot({ status }: { status?: TopologyNodeStatus }) {
  if (!status) return <div className="topo-status-dot status-none" title="No cluster data" />;
  const cls = phaseColor(status.phase);
  const title = `${status.phase} — ${status.ready}/${status.total} pods ready`;
  return <div className={`topo-status-dot ${cls}`} title={title} />;
}

function RestartBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <span className={`topo-restart-badge ${count >= 5 ? 'high' : ''}`} title={`${count} restart${count !== 1 ? 's' : ''}`}>
      ↻{count}
    </span>
  );
}

// ── Custom Node: Service ────────────────────────────────────────

function ServiceNodeComponent({ data, selected }: NodeProps<Node<TopologyNodeData>>) {
  const status = data._status as TopologyNodeStatus | undefined;
  const isStaged = data.staged && !data.dseName;
  return (
    <div className={`topo-node topo-node-service ${selected ? 'selected' : ''} ${data.isNew ? 'is-new' : ''} ${isStaged ? 'is-staged' : ''}`}>
      <Handle type="target" position={Position.Left} id="target-left" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Left} id="source-left" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Top} id="target-top" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Top} id="source-top" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Bottom} id="target-bottom" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Bottom} id="source-bottom" className="topo-handle topo-handle-hidden" />
      {isStaged ? (
        <span className="topo-staged-badge">staged</span>
      ) : (
        <StatusDot status={status} />
      )}
      <div className="topo-node-icon"><ServiceIcon /></div>
      <div className="topo-node-body">
        <div className="topo-node-label">
          {data.label}
          <RestartBadge count={status?.restarts ?? 0} />
        </div>
        <div className="topo-node-detail">
          {isStaged ? (
            <>{data.language || 'node'} scaffold · :{data.servicePort || 3000}</>
          ) : (
            <>
              {data.image || 'custom service'}
              {data.servicePort ? ` :${data.servicePort}` : ''}
            </>
          )}
        </div>
        {isStaged && data.path && (
          <div className="topo-node-path" title={data.path}>
            {(data.path as string).split('/').slice(-2).join('/')}
          </div>
        )}
        {status && !isStaged && (
          <div className="topo-node-status-row">
            <span className={`topo-phase-label ${phaseColor(status.phase)}`}>
              {status.ready}/{status.total}
            </span>
            {status.lastDeploy && (
              <span className="topo-deploy-time">{timeAgo(status.lastDeploy)}</span>
            )}
          </div>
        )}
      </div>
      <Handle type="source" position={Position.Right} id="source-right" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Right} id="target-right" className="topo-handle topo-handle-hidden" />
    </div>
  );
}

// ── Custom Node: Dependency ─────────────────────────────────────

function DependencyNodeComponent({ data, selected }: NodeProps<Node<TopologyNodeData>>) {
  const meta = data.depType ? DEP_META[data.depType] : null;
  const color = meta?.color || '#666';
  const status = data._status as TopologyNodeStatus | undefined;
  return (
    <div
      className={`topo-node topo-node-dep ${selected ? 'selected' : ''} ${data.isNew ? 'is-new' : ''}`}
      style={{ borderColor: color }}
    >
      <Handle type="target" position={Position.Left} id="target-left" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Left} id="source-left" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Top} id="target-top" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Top} id="source-top" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Bottom} id="target-bottom" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Bottom} id="source-bottom" className="topo-handle topo-handle-hidden" />
      <StatusDot status={status} />
      <div className="topo-node-icon">{meta?.icon ? (DEP_ICONS[data.depType!]?.() || meta.icon) : '◆'}</div>
      <div className="topo-node-body">
        <div className="topo-node-label">
          {data.label}
          <RestartBadge count={status?.restarts ?? 0} />
        </div>
        <div className="topo-node-detail">
          {data.depType}{data.version ? `:${data.version}` : ''}
          {data.port ? ` :${data.port}` : ''}
        </div>
        {status && (
          <div className="topo-node-status-row">
            <span className={`topo-phase-label ${phaseColor(status.phase)}`}>
              {status.ready}/{status.total}
            </span>
            {status.lastDeploy && (
              <span className="topo-deploy-time">{timeAgo(status.lastDeploy)}</span>
            )}
          </div>
        )}
      </div>
      <Handle type="source" position={Position.Right} id="source-right" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Right} id="target-right" className="topo-handle topo-handle-hidden" />
    </div>
  );
}

// ── Custom Edge: connection label ───────────────────────────────

// ── Custom Node: External Client ────────────────────────────────

function ExternalNodeComponent({ data, selected }: NodeProps<Node<TopologyNodeData>>) {
  return (
    <div className={`topo-node topo-node-external ${selected ? 'selected' : ''}`}>
      <Handle type="target" position={Position.Left} id="target-left" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Left} id="source-left" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Top} id="target-top" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Top} id="source-top" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Bottom} id="target-bottom" className="topo-handle topo-handle-hidden" />
      <Handle type="source" position={Position.Bottom} id="source-bottom" className="topo-handle topo-handle-hidden" />
      <div className="topo-node-icon"><BrowserIcon /></div>
      <div className="topo-node-body">
        <div className="topo-node-label">{data.label || 'Browser'}</div>
        <div className="topo-node-detail">external client</div>
      </div>
      <Handle type="source" position={Position.Right} id="source-right" className="topo-handle topo-handle-hidden" />
      <Handle type="target" position={Position.Right} id="target-right" className="topo-handle topo-handle-hidden" />
    </div>
  );
}

// ── Smart Edge Routing ──────────────────────────────────────────
// Computes per-edge Y offsets so parallel edges (multiple edges from/to
// the same node) fan out instead of overlapping.

function useEdgeOffsets(edges: Edge[]): Map<string, number> {
  return useMemo(() => {
    const offsets = new Map<string, number>();
    // Group edges by their source node
    const bySource = new Map<string, Edge[]>();
    // Group edges by their target node
    const byTarget = new Map<string, Edge[]>();
    for (const e of edges) {
      bySource.set(e.source, [...(bySource.get(e.source) || []), e]);
      byTarget.set(e.target, [...(byTarget.get(e.target) || []), e]);
    }
    // For each source node, spread its outgoing edges
    const SPREAD = 20; // px between parallel edges
    for (const [, group] of bySource) {
      if (group.length <= 1) continue;
      const half = (group.length - 1) / 2;
      group.forEach((e, i) => {
        offsets.set(e.id, (offsets.get(e.id) || 0) + (i - half) * SPREAD);
      });
    }
    // For each target node with multiple incoming edges, spread them too
    for (const [, group] of byTarget) {
      if (group.length <= 1) continue;
      const half = (group.length - 1) / 2;
      group.forEach((e, i) => {
        offsets.set(e.id, (offsets.get(e.id) || 0) + (i - half) * SPREAD * 0.5);
      });
    }
    return offsets;
  }, [edges]);
}

const EDGE_BORDER_RADIUS = 16;

function ConnectionEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, style, markerEnd }: EdgeProps) {
  const edgeData = data as Record<string, unknown> | undefined;
  const offset = (edgeData?._offset as number) || 0;
  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX, sourceY: sourceY + offset, targetX, targetY: targetY + offset,
    sourcePosition, targetPosition, borderRadius: EDGE_BORDER_RADIUS,
  });

  const label = edgeData?._label as string | undefined;
  const envValue = edgeData?._envValue as string | undefined;

  return (
    <>
      <BaseEdge id={id} path={edgePath} style={style} markerEnd={markerEnd} />
      {label && (
        <EdgeLabelRenderer>
          <div
            className={`topo-edge-label ${envValue ? 'has-url' : ''}`}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)` }}
            title={envValue ? `${label}=${envValue}\nInjected into source container` : label}
          >
            {label}
            {envValue && <span className="topo-edge-url">{envValue}</span>}
            <button
              className="topo-edge-delete"
              onClick={(e) => { e.stopPropagation(); window.dispatchEvent(new CustomEvent('delete-edge', { detail: id })); }}
              title="Remove connection"
            >
              ✕
            </button>
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

function ServiceEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, style, markerEnd }: EdgeProps) {
  const edgeData = data as Record<string, unknown> | undefined;
  const offset = (edgeData?._offset as number) || 0;
  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX, sourceY: sourceY + offset, targetX, targetY: targetY + offset,
    sourcePosition, targetPosition, borderRadius: EDGE_BORDER_RADIUS,
  });

  const label = edgeData?._label as string | undefined;
  const envValue = edgeData?._envValue as string | undefined;

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={{ ...style, strokeDasharray: '6 3', stroke: '#06b6d4', strokeWidth: 2 }}
        markerEnd={markerEnd}
      />
      {label && (
        <EdgeLabelRenderer>
          <div
            className="topo-edge-label topo-edge-label--svc"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)` }}
            title={envValue ? `${label}=${envValue}\nService-to-service connection` : label}
          >
            {label}
            {envValue && <span className="topo-edge-url">{envValue}</span>}
            <button
              className="topo-edge-delete"
              onClick={(e) => { e.stopPropagation(); window.dispatchEvent(new CustomEvent('delete-edge', { detail: id })); }}
              title="Remove connection"
            >
              ✕
            </button>
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

const nodeTypes: NodeTypes = {
  service: ServiceNodeComponent,
  dependency: DependencyNodeComponent,
  external: ExternalNodeComponent,
};

const edgeTypes: EdgeTypes = {
  connection: ConnectionEdge,
  'service-edge': ServiceEdge,
};

// ── Terminal Log Viewer ─────────────────────────────────────────

function TerminalLog({ nodeId }: { nodeId: string }) {
  const [logs, setLogs] = useState<TopologyLogEntry[]>([]);
  const [pods, setPods] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [autoScroll, setAutoScroll] = useState(true);
  const [filter, setFilter] = useState('');
  const termRef = useRef<HTMLDivElement>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchLogs = useCallback(async () => {
    try {
      const result = await fetchTopologyLogs(nodeId);
      setLogs(result.lines || []);
      setPods(result.pods || []);
    } catch { /* ignore */ }
    setLoading(false);
  }, [nodeId]);

  useEffect(() => {
    setLoading(true);
    setLogs([]);
    fetchLogs();
    intervalRef.current = setInterval(fetchLogs, 4000);
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, [fetchLogs]);

  useEffect(() => {
    if (autoScroll && termRef.current) {
      termRef.current.scrollTop = termRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  const handleScroll = useCallback(() => {
    if (!termRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = termRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 40);
  }, []);

  const filteredLogs = filter
    ? logs.filter(l => l.line.toLowerCase().includes(filter.toLowerCase()) || l.pod.toLowerCase().includes(filter.toLowerCase()))
    : logs;

  const multiPod = pods.length > 1;
  // Assign stable colors to pod names
  const podColors = useRef(new Map<string, string>());
  const colorPalette = ['#60a5fa', '#34d399', '#fbbf24', '#f87171', '#a78bfa', '#fb923c', '#2dd4bf', '#e879f9'];
  pods.forEach((p, i) => {
    if (!podColors.current.has(p)) {
      podColors.current.set(p, colorPalette[i % colorPalette.length]);
    }
  });

  return (
    <div className="topo-terminal">
      <div className="topo-terminal-toolbar">
        <input
          className="topo-terminal-filter"
          placeholder="Filter logs…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
        <button
          className={`topo-terminal-btn ${autoScroll ? 'active' : ''}`}
          onClick={() => {
            setAutoScroll(true);
            if (termRef.current) termRef.current.scrollTop = termRef.current.scrollHeight;
          }}
          title="Auto-scroll"
        >
          ↓
        </button>
        <button className="topo-terminal-btn" onClick={fetchLogs} title="Refresh">⟳</button>
      </div>
      <div className="topo-terminal-body" ref={termRef} onScroll={handleScroll}>
        {loading && <div className="topo-terminal-loading">Loading logs…</div>}
        {!loading && filteredLogs.length === 0 && (
          <div className="topo-terminal-empty">No logs available</div>
        )}
        {filteredLogs.map((entry, i) => (
          <div key={i} className="topo-terminal-line">
            {multiPod && (
              <span className="topo-terminal-pod" style={{ color: podColors.current.get(entry.pod) }}>
                {entry.pod.slice(0, 24)}
              </span>
            )}
            <span className="topo-terminal-text">{entry.line}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── Detail Sidebar ──────────────────────────────────────────────

type DetailTab = 'logs' | 'status' | 'config';

function DetailSidebar({ node, onClose, onUpdate, onDelete, edges, allNodes }: {
  node: Node<TopologyNodeData>;
  onClose: () => void;
  onUpdate: (updates: Partial<TopologyNodeData>) => void;
  onDelete: () => void;
  edges: Edge[];
  allNodes: Node<TopologyNodeData>[];
}) {
  const data = node.data;
  const isDep = data.kind === 'dependency';
  const meta = isDep && data.depType ? DEP_META[data.depType] : null;
  const status = data._status as TopologyNodeStatus | undefined;
  const hasClusterData = !!status;
  const isStaged = data.staged && !data.dseName;
  const needsScaffold = isStaged && !data.scaffolded;

  const [tab, setTab] = useState<DetailTab>(hasClusterData ? 'logs' : 'config');
  const [detail, setDetail] = useState<TopologyNodeDetail | null>(null);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [scaling, setScaling] = useState(false);
  const [scaffoldingInSidebar, setScaffoldingInSidebar] = useState(false);

  // ── Debug state ───────────────────────────────────────────────
  const [debugActive, setDebugActive] = useState(false);
  const [debugPort, setDebugPort] = useState<number | null>(null);
  const [debugRuntime, setDebugRuntime] = useState('');
  const [debugLoading, setDebugLoading] = useState(false);
  const [debugLaunch, setDebugLaunch] = useState<Record<string, unknown> | null>(null);

  // Check debug status on mount
  useEffect(() => {
    if (!isDep && data.fromCluster && data.dseName) {
      fetchDebugStatus(data.dseName, 'default').then((res) => {
        if ('active' in res && res.active) {
          setDebugActive(true);
          setDebugPort(res.localPort ?? null);
          setDebugRuntime(res.runtime ?? '');
        }
      }).catch(() => {});
    }
  }, [data.dseName, data.fromCluster, isDep]);

  const handleDebugToggle = async () => {
    if (!data.dseName) return;
    setDebugLoading(true);
    try {
      if (debugActive) {
        await stopDebugSession(data.dseName, 'default');
        setDebugActive(false);
        setDebugPort(null);
        setDebugRuntime('');
        setDebugLaunch(null);
      } else {
        const res = await startDebugSession(data.dseName, 'default');
        if (res.status === 'started' || res.status === 'already_active') {
          setDebugActive(true);
          setDebugPort(res.localPort);
          setDebugRuntime(res.runtime);
          setDebugLaunch(res.launchConfig ?? null);
        }
      }
    } catch {
      // ignore
    } finally {
      setDebugLoading(false);
    }
  };

  // Collect connected env vars for this service from edges
  const connectedDeps = edges
    .filter((e) => e.source === node.id || e.target === node.id)
    .map((e) => {
      const otherId = e.source === node.id ? e.target : e.source;
      const otherNode = allNodes.find((n) => n.id === otherId);
      if (!otherNode) return null;
      if (otherNode.data.kind === 'dependency' && otherNode.data.depType) {
        const depMeta = DEP_META[otherNode.data.depType];
        return {
          envVar: otherNode.data.envVarName || depMeta?.envVar || '',
          value: `(auto-injected by operator)`,
          label: otherNode.data.label || otherNode.data.depType,
        };
      }
      if (otherNode.data.kind === 'service' && e.source === node.id) {
        // svc-to-svc: this service calls the other
        const tName = (otherNode.data.dseName || otherNode.data.label || 'service').toLowerCase().replace(/[^a-z0-9]+/g, '-');
        const port = otherNode.data.servicePort || 3000;
        const envVar = tName.toUpperCase().replace(/-/g, '_') + '_URL';
        return { envVar, value: `http://${tName}:${port}`, label: otherNode.data.label };
      }
      return null;
    })
    .filter(Boolean) as { envVar: string; value: string; label: string }[];

  const handleScaffoldFromSidebar = async () => {
    if (!data.language || !data.path) return;
    const safeName = (data.label || 'service').toLowerCase().replace(/\s+/g, '-');
    setScaffoldingInSidebar(true);
    const result = await scaffoldService({
      name: safeName,
      path: data.path,
      port: data.servicePort || 3000,
      language: data.language,
      deps: connectedDeps.map((d) => ({ envVar: d.envVar, value: d.value })),
    });
    setScaffoldingInSidebar(false);
    if (result.ok) {
      onUpdate({ scaffolded: true });
    }
  };

  const refreshDetail = useCallback(() => {
    setLoadingDetail(true);
    fetchTopologyNodeDetail(node.id)
      .then(setDetail)
      .catch(() => {})
      .finally(() => setLoadingDetail(false));
  }, [node.id]);

  // Fetch detail when Status tab is shown
  useEffect(() => {
    if (tab === 'status' && !detail && !loadingDetail) {
      refreshDetail();
    }
  }, [tab, detail, loadingDetail, refreshDetail]);

  // Reset detail when node changes
  useEffect(() => {
    setDetail(null);
  }, [node.id]);

  return (
    <div className="topo-detail-sidebar">
      {/* Header */}
      <div className="topo-detail-header">
        <span className="topo-detail-icon">{isDep ? (meta?.icon || '◆') : '⬡'}</span>
        <div className="topo-detail-title">
          <h3>{data.label}</h3>
          {status && (
            <span className={`topo-detail-phase ${phaseColor(status.phase)}`}>
              {status.phase} · {status.ready}/{status.total}
            </span>
          )}
          {!isDep && data.dseName && (
            <button
              className="topo-detail-dse-link"
              onClick={() => window.dispatchEvent(new CustomEvent('navigate', { detail: 'dses' }))}
              title="View in Environments"
            >
              ◆ View Environment
            </button>
          )}
        </div>
        <button className="btn btn-sm btn-ghost" onClick={onClose} title="Close (Esc)">✕</button>
      </div>

      {/* Tabs */}
      <div className="topo-detail-tabs">
        {hasClusterData && (
          <>
            <button className={`topo-detail-tab ${tab === 'logs' ? 'active' : ''}`} onClick={() => setTab('logs')}>
              Logs
            </button>
            <button className={`topo-detail-tab ${tab === 'status' ? 'active' : ''}`} onClick={() => setTab('status')}>
              Status
            </button>
          </>
        )}
        <button className={`topo-detail-tab ${tab === 'config' ? 'active' : ''}`} onClick={() => setTab('config')}>
          Config
        </button>
      </div>

      {/* Tab Content */}
      <div className="topo-detail-content">
        {/* ── Logs Tab ── */}
        {tab === 'logs' && <TerminalLog nodeId={node.id} />}

        {/* ── Status Tab ── */}
        {tab === 'status' && (
          <div className="topo-detail-status">
            {loadingDetail ? (
              <div className="topo-detail-loading">Loading…</div>
            ) : detail ? (
              <>
                {/* Scale Controls */}
                {detail.deployment && (
                  <div className="topo-detail-section">
                    <h4>Replicas</h4>
                    <div className="topo-scale-control">
                      <button
                        className="topo-scale-btn"
                        disabled={scaling || detail.deployment.replicas <= 0}
                        onClick={async () => {
                          if (!detail.deployment) return;
                          const n = Math.max(0, detail.deployment.replicas - 1);
                          setScaling(true);
                          await scaleDeployment(detail.deployment.namespace, detail.deployment.name, n);
                          setTimeout(refreshDetail, 1500);
                          setScaling(false);
                        }}
                      >
                        −
                      </button>
                      <div className="topo-scale-value">
                        <span className="topo-scale-current">{detail.deployment.replicas}</span>
                        <span className="topo-scale-available">{detail.deployment.available} available</span>
                      </div>
                      <button
                        className="topo-scale-btn"
                        disabled={scaling}
                        onClick={async () => {
                          if (!detail.deployment) return;
                          const n = detail.deployment.replicas + 1;
                          setScaling(true);
                          await scaleDeployment(detail.deployment.namespace, detail.deployment.name, n);
                          setTimeout(refreshDetail, 1500);
                          setScaling(false);
                        }}
                      >
                        +
                      </button>
                    </div>
                  </div>
                )}

                {/* Pods */}
                <div className="topo-detail-section">
                  <h4>Pods</h4>
                  {(detail.pods || []).length === 0 ? (
                    <div className="topo-detail-empty">No pods found</div>
                  ) : (
                    <div className="topo-detail-pods">
                      {detail.pods.map(p => (
                        <div key={p.name} className="topo-detail-pod-row">
                          <span className={`topo-detail-pod-phase ${p.phase === 'Running' ? 'running' : p.phase === 'Pending' ? 'pending' : 'error'}`}>●</span>
                          <span className="topo-detail-pod-name" title={p.name}>{p.name}</span>
                          <span className="topo-detail-pod-ready">{p.ready}</span>
                          {p.restarts > 0 && <span className="topo-restart-badge">{`↻${p.restarts}`}</span>}
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                {/* Events */}
                {(detail.events || []).length > 0 && (
                  <div className="topo-detail-section">
                    <h4>Recent Events</h4>
                    <div className="topo-detail-events">
                      {detail.events.map((e, i) => (
                        <div key={i} className={`topo-detail-event ${e.type === 'Warning' ? 'warning' : ''}`}>
                          <span className="topo-detail-event-reason">{e.reason}</span>
                          <span className="topo-detail-event-msg">{e.message}</span>
                          {e.count > 1 && <span className="topo-detail-event-count">×{e.count}</span>}
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Environment */}
                {(detail.env || []).length > 0 && (
                  <div className="topo-detail-section">
                    <h4>Environment</h4>
                    <div className="topo-detail-env">
                      {detail.env.map((e, i) => (
                        <div key={i} className="topo-detail-env-row">
                          <code className="topo-detail-env-name">{e.name}</code>
                          <span className="topo-detail-env-val">{e.value || '(empty)'}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </>
            ) : (
              <div className="topo-detail-empty">No cluster data yet — deploy first</div>
            )}
          </div>
        )}

        {/* ── Config Tab ── */}
        {tab === 'config' && (
          <div className="topo-detail-config">
            {isDep ? (
              <>
                <label className="form-label">Type</label>
                <div className="topo-config-value">{meta?.label || data.depType}</div>

                <label className="form-label">Version</label>
                <input
                  className="form-input"
                  placeholder="latest"
                  value={data.version || ''}
                  onChange={(e) => onUpdate({ version: e.target.value })}
                />

                <label className="form-label">Port</label>
                <input
                  className="form-input"
                  type="number"
                  value={data.port || meta?.defaultPort || ''}
                  onChange={(e) => onUpdate({ port: parseInt(e.target.value) || undefined })}
                />

                <label className="form-label">Env Var Name</label>
                <input
                  className="form-input"
                  placeholder={meta?.envVar || 'CONNECTION_URL'}
                  value={data.envVarName || ''}
                  onChange={(e) => onUpdate({ envVarName: e.target.value })}
                />

                {meta && (
                  <div className="topo-config-info">
                    <span className="text-dim">Auto-injected as </span>
                    <code>{data.envVarName || meta.envVar}</code>
                  </div>
                )}
              </>
            ) : (
              <>
                <label className="form-label">Service Name</label>
                <input
                  className="form-input"
                  value={data.label}
                  onChange={(e) => onUpdate({ label: e.target.value })}
                />

                <label className="form-label">Image</label>
                <input
                  className="form-input"
                  placeholder="localhost:5001/my-app:latest"
                  value={data.image || ''}
                  onChange={(e) => onUpdate({ image: e.target.value })}
                />

                <label className="form-label">Source Directory</label>
                <input
                  className="form-input"
                  placeholder="/path/to/source"
                  value={data.path || ''}
                  onChange={(e) => onUpdate({ path: e.target.value })}
                />

                <label className="form-label">Port</label>
                <input
                  className="form-input"
                  type="number"
                  value={data.servicePort || ''}
                  onChange={(e) => onUpdate({ servicePort: parseInt(e.target.value) || undefined })}
                />

                <label className="form-label">Replicas</label>
                <input
                  className="form-input"
                  type="number"
                  min="1"
                  value={data.replicas || 1}
                  onChange={(e) => onUpdate({ replicas: parseInt(e.target.value) || 1 })}
                />
              </>
            )}
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="topo-detail-footer">
        {/* Scaffold button for staged services that haven't been scaffolded yet */}
        {needsScaffold && (
          <div className="topo-scaffold-sidebar">
            {connectedDeps.length > 0 && (
              <div className="topo-scaffold-deps">
                <span className="topo-scaffold-deps-label">Connections detected:</span>
                {connectedDeps.map((d) => (
                  <div key={d.envVar} className="topo-scaffold-dep-item">
                    <code>{d.envVar}</code>
                    <span className="text-dim">{d.label}</span>
                  </div>
                ))}
              </div>
            )}
            <button
              className="btn btn-primary btn-full"
              onClick={handleScaffoldFromSidebar}
              disabled={scaffoldingInSidebar || !data.path}
            >
              {scaffoldingInSidebar ? 'Scaffolding…' : `⬡ Scaffold ${data.language || 'node'} service`}
            </button>
            {connectedDeps.length === 0 && (
              <div className="topo-scaffold-hint">Draw edges to deps/services first, or scaffold now for a basic service.</div>
            )}
          </div>
        )}
        {data.scaffolded && isStaged && (
          <div className="topo-scaffold-done">✓ Scaffolded — write your code, then deploy</div>
        )}
        {/* Test API button for deployed services */}
        {!isDep && data.fromCluster && data.dseName && (
          <button
            className="btn btn-sm btn-ghost"
            style={{ marginBottom: 8 }}
            onClick={() => {
              window.dispatchEvent(new CustomEvent('navigate', { detail: 'api-explorer' }));
              // Small delay so the page mounts before we send the prefill event
              setTimeout(() => {
                window.dispatchEvent(new CustomEvent('api-explorer-prefill', {
                  detail: { service: data.dseName, port: data.servicePort || 3000 },
                }));
              }, 100);
            }}
          >
            ⇆ Test API
          </button>
        )}
        {/* Debug button for deployed services */}
        {!isDep && data.fromCluster && data.dseName && (
          <>
            <button
              className={`btn btn-sm ${debugActive ? 'btn-warning' : 'btn-ghost'}`}
              style={{ marginBottom: 8 }}
              onClick={handleDebugToggle}
              disabled={debugLoading}
            >
              {debugLoading ? '⏳ …' : debugActive ? '🛑 Stop Debugger' : '🔧 Debug'}
            </button>
            {debugActive && debugPort && (
              <div className="topo-debug-status">
                <span className="topo-debug-badge">● {debugRuntime} on localhost:{debugPort}</span>
                {debugLaunch && (
                  <button
                    className="btn btn-xs btn-ghost"
                    title="Copy launch.json config"
                    onClick={() => {
                      navigator.clipboard.writeText(JSON.stringify(debugLaunch, null, 2));
                    }}
                  >
                    📋 Copy launch config
                  </button>
                )}
              </div>
            )}
          </>
        )}
        <button className="btn btn-danger btn-sm" onClick={onDelete}>
          Remove
        </button>
      </div>
    </div>
  );
}

// ── Palette Item ────────────────────────────────────────────────

function PaletteItem({ depType, label, icon, onDragStart }: {
  depType: string;
  label: string;
  icon: string;
  onDragStart: (e: DragEvent, data: { kind: string; depType?: string }) => void;
}) {
  const SvgIcon = DEP_ICONS[depType];
  return (
    <div
      className="topo-palette-item"
      draggable
      onDragStart={(e) => onDragStart(e, { kind: 'dependency', depType })}
    >
      <span className="topo-palette-icon">{SvgIcon ? SvgIcon() : icon}</span>
      <span className="topo-palette-label">{label}</span>
    </div>
  );
}

// ── Auto-Layout Utility ─────────────────────────────────────────
// ── Smart Layout Algorithm ──────────────────────────────────────
// Layered graph layout (Sugiyama-style):
//   1. Topological sort to assign layers (L→R)
//   2. External → upstream services → downstream services → dependencies
//   3. Barycenter heuristic to minimise edge crossings within each layer
//   4. Vertical centering of connected nodes

const LAYOUT = {
  layerGap: 340,        // horizontal gap between layers
  rowGap: 200,          // vertical gap between nodes in same layer
  rowStart: 80,         // top margin
  leftMargin: 60,       // left margin for first layer
};

// Shorthand positions for palette drops (when exact layer isn't known)
const DROP_X = {
  external: LAYOUT.leftMargin,
  service: LAYOUT.leftMargin + LAYOUT.layerGap,
  dependency: LAYOUT.leftMargin + LAYOUT.layerGap * 2,
};

function autoLayoutNodes(
  nodes: Node<TopologyNodeData>[],
  edges: Edge[],
): Node<TopologyNodeData>[] {
  if (nodes.length === 0) return nodes;

  const externals = nodes.filter((n) => n.data.kind === 'external');
  const services = nodes.filter((n) => n.data.kind === 'service');
  const deps = nodes.filter((n) => n.data.kind === 'dependency');

  // Build directed adjacency: source → targets (service→service edges)
  const svcOutgoing = new Map<string, Set<string>>();
  const svcIncoming = new Map<string, Set<string>>();
  const svcToDeps = new Map<string, string[]>();
  const depToSvcs = new Map<string, string[]>();
  const extToSvcs = new Map<string, string[]>();

  for (const s of services) {
    svcOutgoing.set(s.id, new Set());
    svcIncoming.set(s.id, new Set());
  }

  for (const e of edges) {
    const src = nodes.find((n) => n.id === e.source);
    const tgt = nodes.find((n) => n.id === e.target);
    if (!src || !tgt) continue;

    if (src.data.kind === 'service' && tgt.data.kind === 'service') {
      svcOutgoing.get(e.source)?.add(e.target);
      svcIncoming.get(e.target)?.add(e.source);
    } else if (src.data.kind === 'service' && tgt.data.kind === 'dependency') {
      svcToDeps.set(e.source, [...(svcToDeps.get(e.source) || []), e.target]);
      depToSvcs.set(e.target, [...(depToSvcs.get(e.target) || []), e.source]);
    } else if (src.data.kind === 'external') {
      extToSvcs.set(e.source, [...(extToSvcs.get(e.source) || []), e.target]);
    }
  }

  // ── Layer assignment via topological sort (Kahn's algorithm) ──
  // Services with no incoming service edges go to layer 0 (leftmost)
  const svcLayer = new Map<string, number>();
  const inDegree = new Map<string, number>();
  for (const s of services) {
    inDegree.set(s.id, svcIncoming.get(s.id)?.size || 0);
  }

  const queue: string[] = [];
  for (const s of services) {
    if ((inDegree.get(s.id) || 0) === 0) queue.push(s.id);
  }

  while (queue.length > 0) {
    const id = queue.shift()!;
    const layer = svcLayer.get(id) ?? 0;
    svcLayer.set(id, layer);
    for (const target of svcOutgoing.get(id) || []) {
      const currentLayer = svcLayer.get(target) ?? 0;
      svcLayer.set(target, Math.max(currentLayer, layer + 1));
      const deg = (inDegree.get(target) || 1) - 1;
      inDegree.set(target, deg);
      if (deg === 0) queue.push(target);
    }
  }

  // Handle cycles: any service not yet assigned gets layer 0
  for (const s of services) {
    if (!svcLayer.has(s.id)) svcLayer.set(s.id, 0);
  }

  // Group services by layer
  const maxSvcLayer = Math.max(0, ...Array.from(svcLayer.values()));
  const layers: Node<TopologyNodeData>[][] = [];
  for (let i = 0; i <= maxSvcLayer; i++) layers.push([]);
  for (const s of services) {
    layers[svcLayer.get(s.id) || 0].push(s);
  }

  // ── Barycenter ordering to reduce edge crossings ──
  // First pass: order layer 0 by dependency count (most connected first)
  layers[0].sort((a, b) => {
    const aDeps = (svcToDeps.get(a.id) || []).length + (svcOutgoing.get(a.id)?.size || 0);
    const bDeps = (svcToDeps.get(b.id) || []).length + (svcOutgoing.get(b.id)?.size || 0);
    return bDeps - aDeps;
  });

  // Assign initial Y positions to layer 0
  const nodeYPos = new Map<string, number>();
  let y = LAYOUT.rowStart;
  for (const n of layers[0]) {
    nodeYPos.set(n.id, y);
    y += LAYOUT.rowGap;
  }

  // Subsequent layers: order by barycenter of connected nodes in previous layer
  for (let l = 1; l <= maxSvcLayer; l++) {
    for (const n of layers[l]) {
      const incoming = svcIncoming.get(n.id) || new Set<string>();
      const positions = Array.from(incoming)
        .map((id) => nodeYPos.get(id))
        .filter((p): p is number => p !== undefined);
      if (positions.length > 0) {
        nodeYPos.set(n.id, positions.reduce((a, b) => a + b, 0) / positions.length);
      } else {
        nodeYPos.set(n.id, LAYOUT.rowStart);
      }
    }
    // Sort by barycenter Y, then space evenly to avoid overlap
    layers[l].sort((a, b) => (nodeYPos.get(a.id) || 0) - (nodeYPos.get(b.id) || 0));
    // Re-space to guarantee minimum gap
    let ly = LAYOUT.rowStart;
    for (const n of layers[l]) {
      const bary = nodeYPos.get(n.id) || 0;
      const finalY = Math.max(ly, bary);
      nodeYPos.set(n.id, finalY);
      ly = finalY + LAYOUT.rowGap;
    }
  }

  // ── Backwards pass: pull earlier layers towards their targets ──
  for (let l = maxSvcLayer - 1; l >= 0; l--) {
    for (const n of layers[l]) {
      const outgoing = svcOutgoing.get(n.id) || new Set<string>();
      const positions = Array.from(outgoing)
        .map((id) => nodeYPos.get(id))
        .filter((p): p is number => p !== undefined);
      if (positions.length > 0) {
        const avg = positions.reduce((a, b) => a + b, 0) / positions.length;
        const current = nodeYPos.get(n.id) || 0;
        // Nudge towards targets but don't overlap
        nodeYPos.set(n.id, current + (avg - current) * 0.3);
      }
    }
    // Re-sort and re-space
    layers[l].sort((a, b) => (nodeYPos.get(a.id) || 0) - (nodeYPos.get(b.id) || 0));
    let ly = LAYOUT.rowStart;
    for (const n of layers[l]) {
      const finalY = Math.max(ly, nodeYPos.get(n.id) || 0);
      nodeYPos.set(n.id, finalY);
      ly = finalY + LAYOUT.rowGap;
    }
  }

  // ── Compute X positions ──
  // External nodes get layer -1, deps get maxSvcLayer + 1
  const extLayerX = LAYOUT.leftMargin;
  const depLayerX = LAYOUT.leftMargin + (maxSvcLayer + 2) * LAYOUT.layerGap;

  const positions = new Map<string, { x: number; y: number }>();

  // Service positions
  for (const s of services) {
    const layer = svcLayer.get(s.id) || 0;
    // Offset by 1 layer to leave room for externals
    const x = LAYOUT.leftMargin + (layer + 1) * LAYOUT.layerGap;
    positions.set(s.id, { x, y: nodeYPos.get(s.id) || LAYOUT.rowStart });
  }

  // Dependency positions — vertically centered among connected services
  const sortedDeps = [...deps].sort((a, b) => {
    const aSvcs = depToSvcs.get(a.id) || [];
    const bSvcs = depToSvcs.get(b.id) || [];
    const aMinY = aSvcs.length > 0 ? Math.min(...aSvcs.map((s) => nodeYPos.get(s) ?? 9999)) : 9999;
    const bMinY = bSvcs.length > 0 ? Math.min(...bSvcs.map((s) => nodeYPos.get(s) ?? 9999)) : 9999;
    return aMinY - bMinY;
  });

  const usedDepYs: number[] = [];
  for (const dep of sortedDeps) {
    const connectedSvcs = depToSvcs.get(dep.id) || [];
    let targetY: number;
    if (connectedSvcs.length > 0) {
      const ys = connectedSvcs.map((s) => nodeYPos.get(s) ?? 0);
      targetY = ys.reduce((a, b) => a + b, 0) / ys.length;
    } else {
      targetY = (usedDepYs.length > 0 ? usedDepYs[usedDepYs.length - 1] + LAYOUT.rowGap : LAYOUT.rowStart);
    }
    // Avoid overlap with placed deps
    for (const usedY of usedDepYs) {
      if (Math.abs(targetY - usedY) < LAYOUT.rowGap * 0.8) {
        targetY = usedY + LAYOUT.rowGap * 0.8;
      }
    }
    positions.set(dep.id, { x: depLayerX, y: targetY });
    usedDepYs.push(targetY);
  }

  // External positions — aligned with their target service
  let extY = LAYOUT.rowStart;
  for (const ext of externals) {
    const targets = extToSvcs.get(ext.id) || [];
    if (targets.length > 0) {
      const ys = targets.map((t) => positions.get(t)?.y ?? LAYOUT.rowStart);
      extY = ys.reduce((a, b) => a + b, 0) / ys.length;
    }
    positions.set(ext.id, { x: extLayerX, y: extY });
    extY += LAYOUT.rowGap;
  }

  // Apply positions
  return nodes.map((n) => {
    const pos = positions.get(n.id);
    if (pos) return { ...n, position: pos };
    return n;
  });
}

// ── Pending Changes Tracker ─────────────────────────────────────

interface PendingChange {
  id: string;
  type: 'add-node' | 'remove-node' | 'add-edge' | 'remove-edge' | 'move-node';
  description: string;
}

// ── Zoom Controls ───────────────────────────────────────────────

function ZoomControls() {
  const { zoomIn, zoomOut, fitView } = useReactFlow();
  return (
    <Panel position="bottom-right" className="topo-zoom-panel">
      <div className="topo-zoom-controls">
        <button className="topo-zoom-btn" onClick={() => zoomIn({ duration: 200 })} title="Zoom in">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="10" y1="5" x2="10" y2="15" />
            <line x1="5" y1="10" x2="15" y2="10" />
          </svg>
        </button>
        <button className="topo-zoom-btn" onClick={() => zoomOut({ duration: 200 })} title="Zoom out">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="5" y1="10" x2="15" y2="10" />
          </svg>
        </button>
        <button className="topo-zoom-btn" onClick={() => fitView({ duration: 300, padding: 0.15 })} title="Fit view">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="3,7 3,3 7,3" />
            <polyline points="13,3 17,3 17,7" />
            <polyline points="17,13 17,17 13,17" />
            <polyline points="7,17 3,17 3,13" />
          </svg>
        </button>
      </div>
    </Panel>
  );
}

// ── Main Component ──────────────────────────────────────────────

export function TopologyPage() {
  const { toast } = useToast();
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<TopologyNodeData>>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [pendingChanges, setPendingChanges] = useState<PendingChange[]>([]);
  const [deploying, setDeploying] = useState(false);
  const [loading, setLoading] = useState(true);
  const [selectedNode, setSelectedNode] = useState<Node<TopologyNodeData> | null>(null);
  const [showCustomService, setShowCustomService] = useState(false);
  const [showDetailSidebar, setShowDetailSidebar] = useState(false);
  const [initialGraph, setInitialGraph] = useState<TopologyGraph | null>(null);

  // Track the react-flow instance for project/screenToFlowPosition
  const [reactFlowInstance, setReactFlowInstance] = useState<any>(null);

  // Live status map — keyed by topology node ID
  const [statusMap, setStatusMap] = useState<TopologyStatusMap>({});

  // ── Load initial topology from cluster ──────────────────────

  useEffect(() => {
    fetchTopology()
      .then((graph) => {
        setInitialGraph(graph);
        if (graph.nodes.length > 0) {
          const rawNodes = graph.nodes as Node<TopologyNodeData>[];
          // If backend applied saved positions from canvas.json, the
          // response includes hasPositions: true — use positions as-is.
          // Otherwise run auto-layout for a clean initial arrangement.
          if ((graph as any).hasPositions) {
            setNodes(rawNodes);
          } else {
            const layoutNodes = autoLayoutNodes(rawNodes, graph.edges);
            setNodes(layoutNodes);
          }
          setEdges(graph.edges);
        }
      })
      .catch(() => {
        // No topology yet — start empty
      })
      .finally(() => setLoading(false));
  }, []);

  // ── Poll live status every 5 seconds ────────────────────────

  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const status = await fetchTopologyStatus();
        if (!cancelled) setStatusMap(status);
      } catch { /* ignore — cluster may be down */ }
    };

    poll(); // initial fetch
    const interval = setInterval(poll, 5000);
    return () => { cancelled = true; clearInterval(interval); };
  }, []);

  // ── Merge status into node data ─────────────────────────────
  // When statusMap or nodes change, inject _status into each node's data
  // so the custom node components can render it.

  useEffect(() => {
    if (Object.keys(statusMap).length === 0) return;
    setNodes((nds) =>
      nds.map((n) => {
        const st = statusMap[n.id];
        if (st === n.data._status) return n; // skip if unchanged
        return { ...n, data: { ...n.data, _status: st } };
      }),
    );
  }, [statusMap]);

  // ── Compute edge offsets to fan out parallel edges ──────────
  const edgeOffsets = useEdgeOffsets(edges);
  const edgesWithOffsets = useMemo(() =>
    edges.map((e) => {
      const offset = edgeOffsets.get(e.id);
      if (offset === undefined || offset === 0) return e;
      return { ...e, data: { ...e.data, _offset: offset } };
    }),
    [edges, edgeOffsets],
  );

  // ── Auto-save canvas overlay on changes ─────────────────────
  // Debounce saves: extract non-cluster nodes/edges and persist.

  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (loading) return; // don't save during initial load

    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    saveTimerRef.current = setTimeout(() => {
      // Only save nodes that aren't from the cluster
      const overlayNodes = nodes
        .filter((n) => !n.data.fromCluster)
        .map((n) => ({
          id: n.id,
          type: n.type || n.data.kind,
          position: n.position,
          data: n.data,
        }));

      // Build set of cluster node IDs
      const clusterNodeIDs = new Set(
        nodes.filter((n) => n.data.fromCluster).map((n) => n.id)
      );
      // Save edges that connect at least one non-cluster node,
      // OR service-to-service edges (which the cluster doesn't reconstruct)
      const overlayEdges = edges.filter(
        (e) => !clusterNodeIDs.has(e.source) || !clusterNodeIDs.has(e.target) || e.type === 'service-edge'
      ).map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        type: e.type,
        data: e.data,
      }));

      // Save positions for ALL nodes (cluster + canvas) so layout persists
      const positions: Record<string, { x: number; y: number }> = {};
      for (const n of nodes) {
        positions[n.id] = n.position;
      }

      saveCanvas({ nodes: overlayNodes, edges: overlayEdges, positions }).catch(() => {});
    }, 500);

    return () => {
      if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    };
  }, [nodes, edges, loading]);

  // ── Pending change helpers ──────────────────────────────────

  const addChange = useCallback((change: Omit<PendingChange, 'id'>) => {
    setPendingChanges((prev) => [...prev, { ...change, id: `c-${Date.now()}-${Math.random().toString(36).slice(2, 6)}` }]);
  }, []);

  const clearChanges = useCallback(() => {
    setPendingChanges([]);
  }, []);

  const hasStagedNodes = nodes.some((n) => n.data.staged && !n.data.dseName);
  const hasChanges = pendingChanges.length > 0 || hasStagedNodes;

  // ── Node ID generator ──────────────────────────────────────

  const nextId = useCallback((prefix: string) => {
    return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
  }, []);

  // ── Connect handler (draw edge) ────────────────────────────

  const onConnect: OnConnect = useCallback((connection: Connection) => {
    // Derive a label for the edge — if target is a dep, show its injected env var
    const targetNode = nodes.find((n) => n.id === connection.target);
    const sourceNode = nodes.find((n) => n.id === connection.source);
    let edgeLabel = '';
    let edgeData: Record<string, unknown> = {};
    if (targetNode?.data?.kind === 'dependency' && targetNode.data.depType) {
      const meta = DEP_META[targetNode.data.depType];
      edgeLabel = targetNode.data.envVarName || meta?.envVar || '';
    } else if (sourceNode?.data?.kind === 'dependency' && sourceNode.data.depType) {
      const meta = DEP_META[sourceNode.data.depType];
      edgeLabel = sourceNode.data.envVarName || meta?.envVar || '';
    } else if (sourceNode?.data?.kind === 'service' && targetNode?.data?.kind === 'service') {
      // Service-to-service edge — generate env var with in-cluster URL
      const targetName = (targetNode.data.dseName || targetNode.data.label || 'service').toLowerCase().replace(/[^a-z0-9]+/g, '-');
      const port = targetNode.data.servicePort || 3000;
      const envVarName = targetName.toUpperCase().replace(/-/g, '_') + '_URL';
      const envVarValue = `http://${targetName}:${port}`;
      edgeLabel = envVarName;
      edgeData = { _label: envVarName, _envVar: envVarName, _envValue: envVarValue, _targetPort: port };
    } else if (sourceNode?.data?.kind === 'external') {
      // External client → service
      edgeLabel = 'ingress';
    }

    // Prevent duplicate dep types on the same service
    const svcNode = sourceNode?.data?.kind === 'service' ? sourceNode : (targetNode?.data?.kind === 'service' ? targetNode : null);
    const depNode = sourceNode?.data?.kind === 'dependency' ? sourceNode : (targetNode?.data?.kind === 'dependency' ? targetNode : null);
    if (svcNode && depNode && depNode.data.depType) {
      const connectedDepTypes = edges
        .filter((e) => e.source === svcNode.id || e.target === svcNode.id)
        .map((e) => {
          const otherId = e.source === svcNode.id ? e.target : e.source;
          return nodes.find((n) => n.id === otherId)?.data?.depType;
        })
        .filter(Boolean);
      if (connectedDepTypes.includes(depNode.data.depType)) {
        toast(`${svcNode.data.label} already has a ${depNode.data.label} dependency`, 'error');
        return;
      }
    }

    const isSvcToSvc = sourceNode?.data?.kind === 'service' && targetNode?.data?.kind === 'service';
    const newEdge: Edge = {
      ...connection,
      id: `e-${connection.source}-${connection.target}`,
      type: isSvcToSvc ? 'service-edge' : 'connection',
      data: Object.keys(edgeData).length > 0 ? edgeData : { _label: edgeLabel },
    } as Edge;
    setEdges((eds) => addEdge(newEdge, eds as any));

    addChange({
      type: 'add-edge',
      description: `Connect ${sourceNode?.data?.label || connection.source} → ${targetNode?.data?.label || connection.target}`,
    });
  }, [nodes, edges, setEdges, addChange, toast]);

  // ── Drag and drop from palette ─────────────────────────────

  const onDragOver = useCallback((event: DragEvent) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback((event: DragEvent) => {
    event.preventDefault();
    const rawData = event.dataTransfer.getData('application/kindling-topology');
    if (!rawData) return;

    const { kind, depType } = JSON.parse(rawData);

    if (!reactFlowInstance || !reactFlowWrapper.current) return;

    const bounds = reactFlowWrapper.current.getBoundingClientRect();
    const position = reactFlowInstance.screenToFlowPosition({
      x: event.clientX - bounds.left,
      y: event.clientY - bounds.top,
    });

    if (kind === 'dependency' && depType) {
      const meta = DEP_META[depType as DependencyType];
      const nodeId = nextId('dep');
      const newNode: Node<TopologyNodeData> = {
        id: nodeId,
        type: 'dependency',
        position,
        data: {
          kind: 'dependency',
          label: meta.label,
          depType: depType as DependencyType,
          port: meta.defaultPort,
          envVarName: meta.envVar,
          isNew: true,
        },
      };
      setNodes((nds) => [...nds, newNode]);

      // Auto-connect to nearest service node
      const serviceNodes = nodes.filter((n) => n.data.kind === 'service');
      if (serviceNodes.length > 0) {
        let nearest = serviceNodes[0];
        let minDist = Infinity;
        for (const sn of serviceNodes) {
          const dx = sn.position.x - position.x;
          const dy = sn.position.y - position.y;
          const dist = dx * dx + dy * dy;
          if (dist < minDist) {
            minDist = dist;
            nearest = sn;
          }
        }

        // Check if this service already has the same dep type connected
        const connectedDepTypes = new Set(
          edges
            .filter((e) => e.source === nearest.id || e.target === nearest.id)
            .map((e) => {
              const otherId = e.source === nearest.id ? e.target : e.source;
              const other = nodes.find((n) => n.id === otherId);
              return other?.data?.depType;
            })
            .filter(Boolean)
        );
        if (connectedDepTypes.has(depType)) {
          // Remove the node we just added and warn — no pending change recorded
          setNodes((nds) => nds.filter((n) => n.id !== nodeId));
          toast(`${nearest.data.label} already has a ${meta.label} dependency`, 'error');
          return;
        }

        // Only record changes after duplicate check passes
        addChange({ type: 'add-node', description: `Add ${meta.label} dependency` });

        // Reposition dep to the right of the nearest service, in the dependency column
        const existingDeps = nodes.filter((n) => n.data.kind === 'dependency');
        const snapPosition = {
          x: DROP_X.dependency,
          y: nearest.position.y + existingDeps.length * LAYOUT.rowGap * 0.8,
        };
        // Update the node position we just added
        setNodes((nds) =>
          nds.map((n) => n.id === nodeId ? { ...n, position: snapPosition } : n)
        );
        const edgeId = `e-${nearest.id}-${nodeId}`;
        const edgeLabel = meta.envVar || '';
        setEdges((eds) => [...eds, { id: edgeId, source: nearest.id, target: nodeId, type: 'connection', data: { _label: edgeLabel } }]);
        addChange({ type: 'add-edge', description: `Connect ${nearest.data.label} → ${meta.label}` });
      } else {
        // No service nodes to connect to — still record the node addition
        addChange({ type: 'add-node', description: `Add ${meta.label} dependency` });
      }
    } else if (kind === 'service') {
      // Open custom service dialog
      setShowCustomService(true);
    } else if (kind === 'external') {
      const nodeId = nextId('ext');
      const newNode: Node<TopologyNodeData> = {
        id: nodeId,
        type: 'external',
        position,
        data: {
          kind: 'external',
          label: 'Browser',
          isNew: true,
        },
      };
      setNodes((nds) => [...nds, newNode]);
      addChange({ type: 'add-node', description: 'Add Browser Client' });

      // Auto-connect to nearest service
      const serviceNodes = nodes.filter((n) => n.data.kind === 'service' && n.type === 'service');
      if (serviceNodes.length > 0) {
        let nearest = serviceNodes[0];
        let minDist = Infinity;
        for (const sn of serviceNodes) {
          const dx = sn.position.x - position.x;
          const dy = sn.position.y - position.y;
          const dist = dx * dx + dy * dy;
          if (dist < minDist) { minDist = dist; nearest = sn; }
        }
        // Position in the external column, aligned with the service
        setNodes((nds) =>
          nds.map((n) => n.id === nodeId ? { ...n, position: { x: DROP_X.external, y: nearest.position.y } } : n)
        );
        const edgeId = `e-${nodeId}-${nearest.id}`;
        setEdges((eds) => [...eds, { id: edgeId, source: nodeId, target: nearest.id, type: 'connection', data: { _label: 'ingress' } }]);
        addChange({ type: 'add-edge', description: `Browser → ${nearest.data.label}` });
      }
    }
  }, [reactFlowInstance, nextId, setNodes, setEdges, addChange, nodes]);

  const onDragStart = useCallback((event: DragEvent, data: { kind: string; depType?: string }) => {
    event.dataTransfer.setData('application/kindling-topology', JSON.stringify(data));
    event.dataTransfer.effectAllowed = 'move';
  }, []);

  // ── Add custom service from dialog ─────────────────────────

  const [customName, setCustomName] = useState('');
  const [customPath, setCustomPath] = useState('');
  const [customImage, setCustomImage] = useState('');
  const [customPort, setCustomPort] = useState('3000');
  const [customLanguage, setCustomLanguage] = useState<'node' | 'go' | 'python'>('node');
  const [hasExistingSource, setHasExistingSource] = useState(false);
  const [workspaceRoot, setWorkspaceRoot] = useState('');
  const [pathStatus, setPathStatus] = useState<{ exists: boolean; has_dockerfile: boolean; language: string } | null>(null);
  const [checkingPath, setCheckingPath] = useState(false);
  const pathManuallyEdited = useRef(false);

  // Fetch workspace root when dialog opens
  useEffect(() => {
    if (showCustomService && !workspaceRoot) {
      fetchWorkspaceInfo().then((info) => {
        setWorkspaceRoot(info.root);
      }).catch(() => {});
    }
  }, [showCustomService, workspaceRoot]);

  // Auto-suggest path when name changes (only for new scaffolds, and only if user hasn't manually edited)
  useEffect(() => {
    if (!hasExistingSource && customName.trim() && workspaceRoot && !pathManuallyEdited.current) {
      const safeName = customName.trim().toLowerCase().replace(/\s+/g, '-');
      setCustomPath(`${workspaceRoot}/services/${safeName}`);
    }
  }, [customName, workspaceRoot, hasExistingSource]);

  const handleCheckPath = useCallback(async (path: string) => {
    if (!path.trim()) return;
    setCheckingPath(true);
    try {
      const result = await checkPath(path);
      setPathStatus(result);
    } catch {
      setPathStatus({ exists: false, has_dockerfile: false, language: '' });
    }
    setCheckingPath(false);
  }, []);

  // Stage only — place node on canvas without scaffolding
  const handleStage = useCallback(() => {
    if (!customName.trim()) return;
    const port = parseInt(customPort) || 3000;
    const safeName = customName.trim().toLowerCase().replace(/\s+/g, '-');
    const path = customPath || `${workspaceRoot}/services/${safeName}`;

    // Add staged node to canvas
    const nodeId = nextId('svc');
    const svcCount = nodes.filter((n) => n.data.kind === 'service').length;
    const newNode: Node<TopologyNodeData> = {
      id: nodeId,
      type: 'service',
      position: { x: DROP_X.service, y: LAYOUT.rowStart + svcCount * LAYOUT.rowGap },
      data: {
        kind: 'service',
        label: customName.trim(),
        image: `localhost:5001/${safeName}:latest`,
        path,
        servicePort: port,
        replicas: 1,
        isNew: true,
        staged: true,
        language: customLanguage,
      },
    };
    setNodes((nds) => [...nds, newNode]);
    addChange({ type: 'add-node', description: `Stage "${customName.trim()}" (${customLanguage})` });
    toast(`${customName.trim()} staged → draw connections, then scaffold from the detail sidebar`, 'success');

    // Reset form
    setCustomName('');
    setCustomPath('');
    setCustomImage('');
    setCustomPort('3000');
    setCustomLanguage('node');
    setHasExistingSource(false);
    setPathStatus(null);
    pathManuallyEdited.current = false;
    setShowCustomService(false);
  }, [customName, customPath, customPort, customLanguage, workspaceRoot, nodes.length, nextId, setNodes, addChange, toast]);

  const handleAddCustomService = useCallback(() => {
    if (!customName.trim()) return;
    const nodeId = nextId('svc');
    const port = parseInt(customPort) || 3000;
    const image = customImage || `localhost:5001/${customName}:latest`;
    const svcCount = nodes.filter((n) => n.data.kind === 'service').length;

    const newNode: Node<TopologyNodeData> = {
      id: nodeId,
      type: 'service',
      position: { x: DROP_X.service, y: LAYOUT.rowStart + svcCount * LAYOUT.rowGap },
      data: {
        kind: 'service',
        label: customName,
        image,
        path: customPath || undefined,
        servicePort: port,
        replicas: 1,
        isNew: true,
      },
    };
    setNodes((nds) => [...nds, newNode]);
    addChange({ type: 'add-node', description: `Add service "${customName}"` });

    // Reset form
    setCustomName('');
    setCustomPath('');
    setCustomImage('');
    setCustomPort('3000');
    setCustomLanguage('node');
    setHasExistingSource(false);
    setPathStatus(null);
    setShowCustomService(false);
  }, [customName, customPath, customImage, customPort, nodes.length, nextId, setNodes, addChange]);

  // ── Node selection (for detail sidebar) ─────────────────────

  const onNodeClick = useCallback((_: any, node: Node<TopologyNodeData>) => {
    setSelectedNode(node);
    setShowDetailSidebar(true);
  }, []);

  // ── Edge click → open target node detail ──────────────────

  const onEdgeClick = useCallback((_: any, edge: Edge) => {
    // Find the target node (usually the dependency) and open its detail
    const targetNode = nodes.find((n) => n.id === edge.target);
    if (targetNode) {
      setSelectedNode(targetNode);
      setShowDetailSidebar(true);
    }
  }, [nodes]);

  // ── Delete edge ────────────────────────────────────────────

  const handleDeleteEdge = useCallback((edgeId: string) => {
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge) return;
    const sourceNode = nodes.find((n) => n.id === edge.source);
    const targetNode = nodes.find((n) => n.id === edge.target);
    setEdges((eds) => eds.filter((e) => e.id !== edgeId));
    addChange({
      type: 'remove-edge',
      description: `Disconnect ${sourceNode?.data?.label || edge.source} → ${targetNode?.data?.label || edge.target}`,
    });

    // Clean up deployed env var / dependency from the cluster DSE
    const edgeData = edge.data as Record<string, unknown> | undefined;
    const envVar = edgeData?._envVar as string | undefined;
    const sourceDSE = sourceNode?.data?.dseName;
    const targetDSE = targetNode?.data?.dseName;

    if (sourceNode?.data?.kind === 'service' && targetNode?.data?.kind === 'service' && envVar && sourceDSE) {
      // Svc-to-svc: remove the env var from the source service's DSE
      removeEdgeFromCluster({ dseName: sourceDSE, envVar }).catch(() => {});
    } else if (sourceNode?.data?.kind === 'service' && targetNode?.data?.kind === 'dependency' && targetNode.data.depType && sourceDSE) {
      // Svc-to-dep: remove the dependency from the service's DSE
      removeEdgeFromCluster({ dseName: sourceDSE, depType: targetNode.data.depType }).catch(() => {});
    } else if (targetNode?.data?.kind === 'service' && sourceNode?.data?.kind === 'dependency' && sourceNode.data.depType && targetDSE) {
      // Dep-to-svc (reversed direction): remove dep from the service's DSE
      removeEdgeFromCluster({ dseName: targetDSE, depType: sourceNode.data.depType }).catch(() => {});
    }
  }, [edges, nodes, setEdges, addChange]);

  // Listen for delete-edge events from edge label buttons
  useEffect(() => {
    const handler = (e: Event) => {
      const edgeId = (e as CustomEvent).detail;
      if (typeof edgeId === 'string') handleDeleteEdge(edgeId);
    };
    window.addEventListener('delete-edge', handler);
    return () => window.removeEventListener('delete-edge', handler);
  }, [handleDeleteEdge]);

  // ── Delete node ────────────────────────────────────────────

  const handleDeleteNode = useCallback(async (nodeId: string) => {
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;

    // For service nodes, clean up on-disk resources
    if (node.data.kind === 'service') {
      // Collect env var names that OTHER services use to reach this one
      // (edges where this service is the TARGET from another service)
      const referencedBy: string[] = [];
      for (const e of edges) {
        if (e.target === nodeId) {
          const sourceNode = nodes.find((n) => n.id === e.source);
          if (sourceNode?.data.kind === 'service' && e.data) {
            const envVar = (e.data as Record<string, unknown>)?._envVar as string | undefined;
            if (envVar) referencedBy.push(envVar);
          }
        }
      }

      try {
        const result = await cleanupService({
          name: node.data.label,
          path: node.data.path as string | undefined,
          dseName: node.data.dseName as string | undefined,
          referencedBy,
        });
        if (result.ok && result.output) {
          toast(result.output, 'success');
        }
      } catch {
        // Cleanup is best-effort — still remove from canvas
      }
    }

    // For deployed dependency nodes, remove dep from all connected service DSEs
    if (node.data.kind === 'dependency' && node.data.fromCluster && node.data.depType) {
      const connectedServiceDSEs = new Set<string>();
      for (const e of edges) {
        // Find edges where this dep is the target (service → dep)
        if (e.target === nodeId) {
          const sourceNode = nodes.find((n) => n.id === e.source);
          if (sourceNode?.data.kind === 'service' && sourceNode.data.dseName) {
            connectedServiceDSEs.add(sourceNode.data.dseName);
          }
        }
        // Also check edges where this dep is the source (dep → service)
        if (e.source === nodeId) {
          const targetNode = nodes.find((n) => n.id === e.target);
          if (targetNode?.data.kind === 'service' && targetNode.data.dseName) {
            connectedServiceDSEs.add(targetNode.data.dseName);
          }
        }
      }
      const removeResults = await Promise.allSettled(
        [...connectedServiceDSEs].map((dseName) =>
          removeEdgeFromCluster({ dseName, depType: node.data.depType! })
        ),
      );
      const successCount = removeResults.filter((r) => r.status === 'fulfilled').length;
      if (successCount > 0) {
        toast(`Removed ${node.data.depType} from ${successCount} service(s)`, 'success');
      }
    }

    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    addChange({ type: 'remove-node', description: `Remove ${node.data?.label || nodeId}` });
    setShowDetailSidebar(false);
    setSelectedNode(null);
  }, [nodes, edges, setNodes, setEdges, addChange, toast]);

  // ── Update node data ──────────────────────────────────────

  const updateNodeData = useCallback((nodeId: string, updates: Partial<TopologyNodeData>) => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === nodeId
          ? { ...n, data: { ...n.data, ...updates, isDirty: true } }
          : n,
      ),
    );
    addChange({ type: 'add-edge', description: `Update ${updates.label || 'node'} config` });
  }, [setNodes, addChange]);

  // ── Deploy ─────────────────────────────────────────────────

  const handleDeploy = useCallback(async () => {
    setDeploying(true);
    const graph: TopologyGraph = {
      nodes: nodes.map((n) => ({
        id: n.id,
        type: n.type || 'service',
        position: n.position,
        data: n.data,
      })),
      edges: edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        data: e.data as Record<string, unknown> | undefined,
      })),
    };

    const result = await deployTopology(graph);
    setDeploying(false);

    if (result.ok) {
      toast(result.output || 'Topology deployed successfully', 'success');
      clearChanges();

      // Build a map of old dep node IDs → canonical IDs so we can remap edges
      const depIdRemap: Record<string, string> = {};

      // Mark all nodes as not new / not dirty, and promote staged nodes to deployed
      setNodes((nds) =>
        nds.map((n) => {
          const updates: Partial<TopologyNodeData> = { isNew: false, isDirty: false };
          if (n.data.staged && n.data.kind === 'service') {
            const safeName = (n.data.label || '').toLowerCase().replace(/\s+/g, '-');
            updates.staged = false;
            updates.dseName = safeName;
            updates.fromCluster = true;
          }
          // Promote dependency nodes to canonical IDs and mark as deployed
          if (n.data.kind === 'dependency' && n.data.depType && !n.data.fromCluster) {
            const canonicalId = `dep-${n.data.depType}`;
            if (n.id !== canonicalId) {
              depIdRemap[n.id] = canonicalId;
            }
            updates.staged = false;
            updates.fromCluster = true;
            return { ...n, id: canonicalId, data: { ...n.data, ...updates } };
          }
          return { ...n, data: { ...n.data, ...updates } };
        }),
      );

      // Remap edges that reference old dep node IDs
      if (Object.keys(depIdRemap).length > 0) {
        setEdges((eds) =>
          eds.map((e) => {
            let updated = false;
            let { source, target } = e;
            if (depIdRemap[source]) { source = depIdRemap[source]; updated = true; }
            if (depIdRemap[target]) { target = depIdRemap[target]; updated = true; }
            if (!updated) return e;
            return { ...e, id: `e-${source}-${target}`, source, target };
          }),
        );
      }
    } else {
      toast(result.error || 'Deploy failed', 'error');
    }
  }, [nodes, edges, toast, clearChanges, setNodes]);

  // ── Discard changes ────────────────────────────────────────

  const handleDiscard = useCallback(() => {
    if (initialGraph) {
      setNodes(initialGraph.nodes as Node<TopologyNodeData>[]);
      setEdges(initialGraph.edges);
    } else {
      setNodes([]);
      setEdges([]);
    }
    clearChanges();
    setSelectedNode(null);
    setShowDetailSidebar(false);
  }, [initialGraph, setNodes, setEdges, clearChanges]);

  // ── Keyboard shortcuts ─────────────────────────────────────

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Delete' || e.key === 'Backspace') {
        if (selectedNode && !showCustomService && !showDetailSidebar) {
          handleDeleteNode(selectedNode.id);
        }
      }
      if (e.key === 'Escape') {
        setShowDetailSidebar(false);
        setShowCustomService(false);
        setSelectedNode(null);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [selectedNode, showCustomService, showDetailSidebar, handleDeleteNode]);

  // ── Minimap node color ─────────────────────────────────────

  const minimapNodeColor = useCallback((node: Node) => {
    if (node.type === 'dependency') {
      const depType = (node.data as unknown as TopologyNodeData)?.depType;
      return depType ? DEP_META[depType]?.color || '#666' : '#666';
    }
    if (node.type === 'external') return '#8b5cf6';
    return '#3b82f6';
  }, []);

  if (loading) return <div className="loading">Loading topology…</div>;

  return (
    <div className="topo-page">
      {/* ── Palette Sidebar ───────────────────────────────────── */}
      <div className="topo-palette">
        <div className="topo-palette-header">
          <h3>Services</h3>
        </div>

        <div className="topo-palette-section">
          <div className="topo-palette-section-label">Custom</div>
          <div
            className="topo-palette-item topo-palette-item-add"
            onClick={() => setShowCustomService(true)}
          >
            <span className="topo-palette-icon">＋</span>
            <span className="topo-palette-label">New Service</span>
          </div>
          <div
            className="topo-palette-item"
            draggable
            onDragStart={(e) => onDragStart(e, { kind: 'service' })}
          >
            <span className="topo-palette-icon"><ServiceIcon /></span>
            <span className="topo-palette-label">Drag Service</span>
          </div>
        </div>

        <div className="topo-palette-section">
          <div className="topo-palette-section-label">External</div>
          <div
            className="topo-palette-item"
            draggable
            onDragStart={(e) => onDragStart(e, { kind: 'external' })}
          >
            <span className="topo-palette-icon"><BrowserIcon /></span>
            <span className="topo-palette-label">Browser Client</span>
          </div>
        </div>

        <div className="topo-palette-section">
          <div className="topo-palette-section-label">Dependencies</div>
          {DEPENDENCY_TYPES.map((depType) => {
            const meta = DEP_META[depType];
            return (
              <PaletteItem
                key={depType}
                depType={depType}
                label={meta.label}
                icon={meta.icon}
                onDragStart={onDragStart}
              />
            );
          })}
        </div>
      </div>

      {/* ── Canvas ────────────────────────────────────────────── */}
      <div className="topo-canvas" ref={reactFlowWrapper}>
        <ReactFlow
          nodes={nodes}
          edges={edgesWithOffsets}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onDragOver={onDragOver}
          onDrop={onDrop}
          onInit={setReactFlowInstance}
          onNodeClick={onNodeClick}
          onEdgeClick={onEdgeClick}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          fitView
          snapToGrid
          snapGrid={[16, 16]}
          connectionMode={ConnectionMode.Loose}
          defaultEdgeOptions={{
            type: 'connection',
            animated: true,
            style: { stroke: '#3b82f6', strokeWidth: 2 },
          }}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} size={1} color="rgba(255,255,255,0.03)" />
          <ZoomControls />
          <MiniMap
            nodeColor={minimapNodeColor}
            maskColor="rgba(0,0,0,0.7)"
            style={{ background: 'var(--bg-surface)' }}
          />
        </ReactFlow>

        {/* Empty state */}
        {nodes.length === 0 && (
          <div className="topo-empty">
            <div className="topo-empty-icon">⬡</div>
            <h3>Design Your Architecture</h3>
            <p>Drag services and dependencies from the palette, draw connections between them, then deploy.</p>
          </div>
        )}
      </div>

      {/* ── Detail Sidebar ──────────────────────────────────────── */}
      {showDetailSidebar && selectedNode && (
        <DetailSidebar
          node={selectedNode}
          onClose={() => { setShowDetailSidebar(false); setSelectedNode(null); }}
          onUpdate={(updates) => updateNodeData(selectedNode.id, updates)}
          onDelete={() => handleDeleteNode(selectedNode.id)}
          edges={edges}
          allNodes={nodes}
        />
      )}

      {/* ── Custom Service Dialog ─────────────────────────────── */}
      {showCustomService && (
        <ActionModal
          title="Add Service"
          submitLabel={hasExistingSource ? 'Add to Canvas' : undefined}
          onSubmit={hasExistingSource ? handleAddCustomService : undefined}
          onClose={() => {
            setShowCustomService(false);
            setCustomName('');
            setCustomPath('');
            setCustomImage('');
            setCustomPort('3000');
            setCustomLanguage('node');
            setHasExistingSource(false);
            setPathStatus(null);
            pathManuallyEdited.current = false;
          }}
        >
          <label className="form-label">Service Name</label>
          <input
            className="form-input"
            placeholder="my-api"
            value={customName}
            onChange={(e) => setCustomName(e.target.value)}
            autoFocus
          />

          {/* ── Source toggle ──────────────────────────────── */}
          <div className="topo-source-toggle">
            <button
              className={`topo-toggle-btn ${!hasExistingSource ? 'active' : ''}`}
              onClick={() => { setHasExistingSource(false); setPathStatus(null); }}
            >
              New Scaffold
            </button>
            <button
              className={`topo-toggle-btn ${hasExistingSource ? 'active' : ''}`}
              onClick={() => { setHasExistingSource(true); setPathStatus(null); setCustomPath(''); pathManuallyEdited.current = false; }}
            >
              Existing Source
            </button>
          </div>

          {/* ── New scaffold mode ─────────────────────────── */}
          {!hasExistingSource && (
            <>
              <label className="form-label">Language</label>
              <div className="topo-lang-picker">
                {(['node', 'go', 'python'] as const).map((lang) => (
                  <button
                    key={lang}
                    className={`topo-lang-btn ${customLanguage === lang ? 'active' : ''}`}
                    onClick={() => setCustomLanguage(lang)}
                  >
                    {lang === 'node' ? 'Node.js' : lang === 'go' ? 'Go' : 'Python'}
                  </button>
                ))}
              </div>

              <label className="form-label">Port</label>
              <input
                className="form-input"
                type="number"
                placeholder="3000"
                value={customPort}
                onChange={(e) => setCustomPort(e.target.value)}
              />

              <label className="form-label">Scaffold Path</label>
              <input
                className="form-input form-input-dim"
                value={customPath}
                onChange={(e) => { pathManuallyEdited.current = true; setCustomPath(e.target.value); }}
                placeholder={workspaceRoot ? `${workspaceRoot}/services/...` : 'loading...'}
              />
              <div className="topo-scaffold-hint">
                Stages a {customLanguage === 'node' ? 'Node.js' : customLanguage === 'go' ? 'Go' : 'Python'} service on the canvas.
                Draw connections first, then scaffold from the detail sidebar to include the right env vars.
              </div>

              <button
                className="btn btn-primary btn-full"
                onClick={handleStage}
                disabled={!customName.trim()}
              >
                ⬡ Stage
              </button>
            </>
          )}

          {/* ── Existing source mode ──────────────────────── */}
          {hasExistingSource && (
            <>
              <label className="form-label">Source Directory</label>
              <div className="form-row">
                <input
                  className="form-input"
                  placeholder="/Users/you/project/my-api"
                  value={customPath}
                  onChange={(e) => {
                    setCustomPath(e.target.value);
                    setPathStatus(null);
                  }}
                />
                <button
                  className="btn btn-sm"
                  onClick={() => handleCheckPath(customPath)}
                  disabled={!customPath.trim() || checkingPath}
                >
                  {checkingPath ? '…' : 'Check'}
                </button>
              </div>
              {pathStatus && (
                <div className={`topo-path-status ${pathStatus.exists ? 'exists' : 'missing'}`}>
                  {pathStatus.exists ? (
                    <>
                      <span>✓ Directory exists</span>
                      {pathStatus.has_dockerfile && <span className="tag">Dockerfile</span>}
                      {pathStatus.language && <span className="tag">{pathStatus.language}</span>}
                    </>
                  ) : (
                    <span>⚠ Directory not found — switch to "New Scaffold" to create one</span>
                  )}
                </div>
              )}

              <label className="form-label">Container Image</label>
              <input
                className="form-input"
                placeholder={`localhost:5001/${customName || 'my-api'}:latest`}
                value={customImage}
                onChange={(e) => setCustomImage(e.target.value)}
              />

              <label className="form-label">Port</label>
              <input
                className="form-input"
                type="number"
                placeholder="3000"
                value={customPort}
                onChange={(e) => setCustomPort(e.target.value)}
              />
            </>
          )}
        </ActionModal>
      )}

      {/* ── Deploy Bar ────────────────────────────────────────── */}
      {hasChanges && (
        <div className="topo-deploy-bar">
          <div className="topo-deploy-changes">
            {pendingChanges.length > 0 ? (
              <>
                <span className="topo-deploy-count">{pendingChanges.length}</span>
                <span className="topo-deploy-label">
                  pending change{pendingChanges.length !== 1 ? 's' : ''}
                </span>
                <div className="topo-deploy-list">
                  {pendingChanges.slice(-5).map((c) => (
                    <span key={c.id} className="topo-deploy-change-item">{c.description}</span>
                  ))}
                  {pendingChanges.length > 5 && (
                    <span className="topo-deploy-change-item text-dim">
                      +{pendingChanges.length - 5} more
                    </span>
                  )}
                </div>
              </>
            ) : (
              <>
                <span className="topo-deploy-count">{nodes.filter((n) => n.data.staged && !n.data.dseName).length}</span>
                <span className="topo-deploy-label">staged service{nodes.filter((n) => n.data.staged && !n.data.dseName).length !== 1 ? 's' : ''} ready to deploy</span>
              </>
            )}
          </div>
          <div className="topo-deploy-actions">
            {pendingChanges.length > 0 && (
              <button className="btn" onClick={handleDiscard} disabled={deploying}>
                Discard
              </button>
            )}
            <button className="btn btn-primary" onClick={handleDeploy} disabled={deploying}>
              {deploying ? 'Deploying…' : '🚀 Deploy'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

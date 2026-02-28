import { useState, useCallback, useRef, useEffect, type DragEvent } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  addEdge,
  useNodesState,
  useEdgesState,
  Handle,
  Position,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  type OnConnect,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  DEPENDENCY_TYPES,
  DEP_META,
  type DependencyType,
  type TopologyNodeData,
  type TopologyGraph,
} from '../types';
import { fetchTopology, deployTopology, scaffoldService, checkPath } from '../api';
import { ActionModal, useToast } from './actions';

// ── Custom Node: Service ────────────────────────────────────────

function ServiceNodeComponent({ data, selected }: NodeProps<Node<TopologyNodeData>>) {
  return (
    <div className={`topo-node topo-node-service ${selected ? 'selected' : ''} ${data.isNew ? 'is-new' : ''}`}>
      <Handle type="target" position={Position.Left} className="topo-handle" />
      <div className="topo-node-icon">⬡</div>
      <div className="topo-node-body">
        <div className="topo-node-label">{data.label}</div>
        <div className="topo-node-detail">
          {data.image || 'custom service'}
          {data.servicePort ? ` :${data.servicePort}` : ''}
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="topo-handle" />
    </div>
  );
}

// ── Custom Node: Dependency ─────────────────────────────────────

function DependencyNodeComponent({ data, selected }: NodeProps<Node<TopologyNodeData>>) {
  const meta = data.depType ? DEP_META[data.depType] : null;
  const color = meta?.color || '#666';
  return (
    <div
      className={`topo-node topo-node-dep ${selected ? 'selected' : ''} ${data.isNew ? 'is-new' : ''}`}
      style={{ borderColor: color }}
    >
      <Handle type="target" position={Position.Left} className="topo-handle" />
      <div className="topo-node-icon">{meta?.icon || '◆'}</div>
      <div className="topo-node-body">
        <div className="topo-node-label">{data.label}</div>
        <div className="topo-node-detail">
          {data.depType}{data.version ? `:${data.version}` : ''}
          {data.port ? ` :${data.port}` : ''}
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="topo-handle" />
    </div>
  );
}

const nodeTypes: NodeTypes = {
  service: ServiceNodeComponent,
  dependency: DependencyNodeComponent,
};

// ── Palette Item ────────────────────────────────────────────────

function PaletteItem({ depType, label, icon, onDragStart }: {
  depType: string;
  label: string;
  icon: string;
  onDragStart: (e: DragEvent, data: { kind: string; depType?: string }) => void;
}) {
  return (
    <div
      className="topo-palette-item"
      draggable
      onDragStart={(e) => onDragStart(e, { kind: 'dependency', depType })}
    >
      <span className="topo-palette-icon">{icon}</span>
      <span className="topo-palette-label">{label}</span>
    </div>
  );
}

// ── Pending Changes Tracker ─────────────────────────────────────

interface PendingChange {
  id: string;
  type: 'add-node' | 'remove-node' | 'add-edge' | 'remove-edge' | 'move-node';
  description: string;
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
  const [showNodeConfig, setShowNodeConfig] = useState(false);
  const [initialGraph, setInitialGraph] = useState<TopologyGraph | null>(null);

  // Track the react-flow instance for project/screenToFlowPosition
  const [reactFlowInstance, setReactFlowInstance] = useState<any>(null);

  // ── Load initial topology from cluster ──────────────────────

  useEffect(() => {
    fetchTopology()
      .then((graph) => {
        setInitialGraph(graph);
        if (graph.nodes.length > 0) {
          setNodes(graph.nodes as Node<TopologyNodeData>[]);
          setEdges(graph.edges);
        }
      })
      .catch(() => {
        // No topology yet — start empty
      })
      .finally(() => setLoading(false));
  }, []);

  // ── Pending change helpers ──────────────────────────────────

  const addChange = useCallback((change: Omit<PendingChange, 'id'>) => {
    setPendingChanges((prev) => [...prev, { ...change, id: `c-${Date.now()}-${Math.random().toString(36).slice(2, 6)}` }]);
  }, []);

  const clearChanges = useCallback(() => {
    setPendingChanges([]);
  }, []);

  const hasChanges = pendingChanges.length > 0;

  // ── Node ID generator ──────────────────────────────────────

  const nextId = useCallback((prefix: string) => {
    return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
  }, []);

  // ── Connect handler (draw edge) ────────────────────────────

  const onConnect: OnConnect = useCallback((connection: Connection) => {
    const newEdge = {
      ...connection,
      id: `e-${connection.source}-${connection.target}`,
    };
    setEdges((eds) => addEdge(newEdge, eds as any));

    // Find source and target labels for description
    const sourceNode = nodes.find((n) => n.id === connection.source);
    const targetNode = nodes.find((n) => n.id === connection.target);
    addChange({
      type: 'add-edge',
      description: `Connect ${sourceNode?.data?.label || connection.source} → ${targetNode?.data?.label || connection.target}`,
    });
  }, [nodes, setEdges, addChange]);

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
      addChange({ type: 'add-node', description: `Add ${meta.label} dependency` });
    } else if (kind === 'service') {
      // Open custom service dialog
      setShowCustomService(true);
    }
  }, [reactFlowInstance, nextId, setNodes, addChange]);

  const onDragStart = useCallback((event: DragEvent, data: { kind: string; depType?: string }) => {
    event.dataTransfer.setData('application/kindling-topology', JSON.stringify(data));
    event.dataTransfer.effectAllowed = 'move';
  }, []);

  // ── Add custom service from dialog ─────────────────────────

  const [customName, setCustomName] = useState('');
  const [customPath, setCustomPath] = useState('');
  const [customImage, setCustomImage] = useState('');
  const [customPort, setCustomPort] = useState('3000');
  const [pathStatus, setPathStatus] = useState<{ exists: boolean; has_dockerfile: boolean; language: string } | null>(null);
  const [checkingPath, setCheckingPath] = useState(false);
  const [scaffolding, setScaffolding] = useState(false);

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

  const handleScaffold = useCallback(async () => {
    if (!customPath.trim() || !customName.trim()) return;
    setScaffolding(true);
    const result = await scaffoldService({
      name: customName,
      path: customPath,
      port: parseInt(customPort) || 3000,
    });
    setScaffolding(false);
    if (result.ok) {
      toast('Service scaffolded', 'success');
      setPathStatus({ exists: true, has_dockerfile: true, language: 'unknown' });
    } else {
      toast(result.error || 'Scaffold failed', 'error');
    }
  }, [customName, customPath, customPort, toast]);

  const handleAddCustomService = useCallback(() => {
    if (!customName.trim()) return;
    const nodeId = nextId('svc');
    const port = parseInt(customPort) || 3000;
    const image = customImage || `localhost:5001/${customName}:latest`;

    const newNode: Node<TopologyNodeData> = {
      id: nodeId,
      type: 'service',
      position: { x: 300, y: 100 + nodes.length * 120 },
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
    setPathStatus(null);
    setShowCustomService(false);
  }, [customName, customPath, customImage, customPort, nodes.length, nextId, setNodes, addChange]);

  // ── Node selection (for config panel) ──────────────────────

  const onNodeClick = useCallback((_: any, node: Node<TopologyNodeData>) => {
    setSelectedNode(node);
    setShowNodeConfig(true);
  }, []);

  // ── Delete node ────────────────────────────────────────────

  const handleDeleteNode = useCallback((nodeId: string) => {
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    addChange({ type: 'remove-node', description: `Remove ${node.data?.label || nodeId}` });
    setShowNodeConfig(false);
    setSelectedNode(null);
  }, [nodes, setNodes, setEdges, addChange]);

  // ── Update node data ──────────────────────────────────────

  const updateNodeData = useCallback((nodeId: string, updates: Partial<TopologyNodeData>) => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === nodeId
          ? { ...n, data: { ...n.data, ...updates } }
          : n,
      ),
    );
  }, [setNodes]);

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
      })),
    };

    const result = await deployTopology(graph);
    setDeploying(false);

    if (result.ok) {
      toast('Topology deployed successfully', 'success');
      clearChanges();
      // Mark all nodes as not new
      setNodes((nds) =>
        nds.map((n) => ({
          ...n,
          data: { ...n.data, isNew: false },
        })),
      );
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
    setShowNodeConfig(false);
  }, [initialGraph, setNodes, setEdges, clearChanges]);

  // ── Keyboard shortcuts ─────────────────────────────────────

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Delete' || e.key === 'Backspace') {
        if (selectedNode && !showCustomService && !showNodeConfig) {
          handleDeleteNode(selectedNode.id);
        }
      }
      if (e.key === 'Escape') {
        setShowNodeConfig(false);
        setShowCustomService(false);
        setSelectedNode(null);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [selectedNode, showCustomService, showNodeConfig, handleDeleteNode]);

  // ── Minimap node color ─────────────────────────────────────

  const minimapNodeColor = useCallback((node: Node) => {
    if (node.type === 'dependency') {
      const depType = (node.data as unknown as TopologyNodeData)?.depType;
      return depType ? DEP_META[depType]?.color || '#666' : '#666';
    }
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
            <span className="topo-palette-icon">⬡</span>
            <span className="topo-palette-label">Drag Service</span>
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
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onDragOver={onDragOver}
          onDrop={onDrop}
          onInit={setReactFlowInstance}
          onNodeClick={onNodeClick}
          nodeTypes={nodeTypes}
          fitView
          snapToGrid
          snapGrid={[16, 16]}
          defaultEdgeOptions={{
            type: 'smoothstep',
            animated: true,
            style: { stroke: '#3b82f6', strokeWidth: 2 },
          }}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} size={1} color="rgba(255,255,255,0.03)" />
          <Controls
            position="bottom-right"
            showInteractive={false}
          />
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

      {/* ── Node Config Panel ─────────────────────────────────── */}
      {showNodeConfig && selectedNode && (
        <NodeConfigPanel
          node={selectedNode}
          onClose={() => { setShowNodeConfig(false); setSelectedNode(null); }}
          onUpdate={(updates) => updateNodeData(selectedNode.id, updates)}
          onDelete={() => handleDeleteNode(selectedNode.id)}
        />
      )}

      {/* ── Custom Service Dialog ─────────────────────────────── */}
      {showCustomService && (
        <ActionModal
          title="Add Custom Service"
          submitLabel="Add to Canvas"
          onSubmit={handleAddCustomService}
          onClose={() => {
            setShowCustomService(false);
            setCustomName('');
            setCustomPath('');
            setCustomImage('');
            setCustomPort('3000');
            setPathStatus(null);
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

          <label className="form-label">Source Directory (optional)</label>
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
                <>
                  <span>Directory not found</span>
                  <button
                    className="btn btn-sm btn-primary"
                    onClick={handleScaffold}
                    disabled={scaffolding || !customName.trim()}
                  >
                    {scaffolding ? 'Creating…' : 'Create with scaffold'}
                  </button>
                </>
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
        </ActionModal>
      )}

      {/* ── Deploy Bar ────────────────────────────────────────── */}
      {hasChanges && (
        <div className="topo-deploy-bar">
          <div className="topo-deploy-changes">
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
          </div>
          <div className="topo-deploy-actions">
            <button className="btn" onClick={handleDiscard} disabled={deploying}>
              Discard
            </button>
            <button className="btn btn-primary" onClick={handleDeploy} disabled={deploying}>
              {deploying ? 'Deploying…' : '🚀 Deploy'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Node Config Panel ───────────────────────────────────────────

function NodeConfigPanel({ node, onClose, onUpdate, onDelete }: {
  node: Node<TopologyNodeData>;
  onClose: () => void;
  onUpdate: (updates: Partial<TopologyNodeData>) => void;
  onDelete: () => void;
}) {
  const data = node.data;
  const isDep = data.kind === 'dependency';
  const meta = isDep && data.depType ? DEP_META[data.depType] : null;

  return (
    <div className="topo-config-panel">
      <div className="topo-config-header">
        <span className="topo-config-icon">{isDep ? (meta?.icon || '◆') : '⬡'}</span>
        <h3>{data.label}</h3>
        <button className="btn btn-sm btn-ghost" onClick={onClose}>✕</button>
      </div>

      <div className="topo-config-body">
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

      <div className="topo-config-footer">
        <button className="btn btn-danger btn-sm" onClick={onDelete}>
          Remove
        </button>
      </div>
    </div>
  );
}

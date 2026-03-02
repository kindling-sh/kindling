import { useState, useEffect, useCallback, useRef } from 'react';
import { fetchProxyServices, proxyRequest, fetchServiceSpec } from '../api';
import type { ProxyService, ProxyResponse, ApiSpec, ApiEndpoint } from '../api';

// ── Types ───────────────────────────────────────────────────────

type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

interface HistoryEntry {
  id: string;
  timestamp: number;
  method: HttpMethod;
  service: string;
  port: number;
  path: string;
  status?: number;
  elapsed?: number;
  request: {
    headers: Record<string, string>;
    body: string;
  };
  response?: ProxyResponse;
}

const METHOD_COLORS: Record<HttpMethod, string> = {
  GET: '#61affe',
  POST: '#49cc90',
  PUT: '#fca130',
  PATCH: '#50e3c2',
  DELETE: '#f93e3e',
};

const METHODS: HttpMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];

// ── Main Component ──────────────────────────────────────────────

export function ApiExplorerPage() {
  // Services
  const [services, setServices] = useState<ProxyService[]>([]);
  const [loadingServices, setLoadingServices] = useState(true);

  // Request state
  const [method, setMethod] = useState<HttpMethod>('GET');
  const [selectedService, setSelectedService] = useState('');
  const [selectedPort, setSelectedPort] = useState(0);
  const [path, setPath] = useState('/');
  const [reqHeaders, setReqHeaders] = useState('');
  const [reqBody, setReqBody] = useState('');
  const [showHeaders, setShowHeaders] = useState(false);
  const [showBody, setShowBody] = useState(false);

  // Response state
  const [response, setResponse] = useState<ProxyResponse | null>(null);
  const [sending, setSending] = useState(false);

  // History
  const [history, setHistory] = useState<HistoryEntry[]>(() => {
    try {
      const saved = localStorage.getItem('kindling-api-history');
      return saved ? JSON.parse(saved) : [];
    } catch { return []; }
  });
  const [showHistory, setShowHistory] = useState(false);
  const [showResponseHeaders, setShowResponseHeaders] = useState(false);

  // API spec discovery
  const [apiSpec, setApiSpec] = useState<ApiSpec | null>(null);
  const [loadingSpec, setLoadingSpec] = useState(false);
  const [showEndpoints, setShowEndpoints] = useState(true);

  // Saved request bodies — persisted per service+method+path
  const [savedBodies, setSavedBodies] = useState<Record<string, string>>(() => {
    try {
      const saved = localStorage.getItem('kindling-api-saved-bodies');
      return saved ? JSON.parse(saved) : {};
    } catch { return {}; }
  });

  const bodyKey = (svc: string, m: string, p: string) => `${svc}:${m}:${p}`;

  const saveBody = useCallback((svc: string, m: string, p: string, body: string) => {
    if (!body.trim()) return;
    setSavedBodies(prev => {
      const next = { ...prev, [bodyKey(svc, m, p)]: body };
      localStorage.setItem('kindling-api-saved-bodies', JSON.stringify(next));
      return next;
    });
  }, []);

  const pathInputRef = useRef<HTMLInputElement>(null);

  // Persist history
  useEffect(() => {
    localStorage.setItem('kindling-api-history', JSON.stringify(history.slice(0, 50)));
  }, [history]);

  // Load services
  useEffect(() => {
    fetchProxyServices()
      .then((svcs) => {
        setServices(svcs);
        if (svcs.length > 0 && !selectedService) {
          setSelectedService(svcs[0].name);
          setSelectedPort(svcs[0].port);
        }
      })
      .catch(() => {})
      .finally(() => setLoadingServices(false));
  }, []);

  // Listen for pre-fill from topology
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail as { service: string; port: number } | undefined;
      if (detail) {
        setSelectedService(detail.service);
        setSelectedPort(detail.port);
        setPath('/');
        setMethod('GET');
        pathInputRef.current?.focus();
      }
    };
    window.addEventListener('api-explorer-prefill', handler);
    return () => window.removeEventListener('api-explorer-prefill', handler);
  }, []);

  // Update port when service changes
  const handleServiceChange = useCallback((name: string) => {
    setSelectedService(name);
    const svc = services.find(s => s.name === name);
    if (svc) setSelectedPort(svc.port);
  }, [services]);

  // Fetch API spec when service changes
  useEffect(() => {
    if (!selectedService || !selectedPort) {
      setApiSpec(null);
      return;
    }
    const svc = services.find(s => s.name === selectedService);
    const ns = svc?.namespace || 'default';
    setLoadingSpec(true);
    fetchServiceSpec(ns, selectedService, selectedPort)
      .then(setApiSpec)
      .catch(() => setApiSpec(null))
      .finally(() => setLoadingSpec(false));
  }, [selectedService, selectedPort, services]);

  // Send request
  const handleSend = useCallback(async () => {
    if (!selectedService || !selectedPort) return;
    setSending(true);
    setResponse(null);

    let headers: Record<string, string> = {};
    if (reqHeaders.trim()) {
      try { headers = JSON.parse(reqHeaders); } catch { /* ignore */ }
    }

    const result = await proxyRequest({
      service: selectedService,
      port: selectedPort,
      method,
      path,
      headers: Object.keys(headers).length > 0 ? headers : undefined,
      body: reqBody || undefined,
    });

    setResponse(result);
    setSending(false);

    // Persist the body for next time
    if (reqBody.trim()) {
      saveBody(selectedService, method, path, reqBody);
    }

    // Add to history
    const entry: HistoryEntry = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      timestamp: Date.now(),
      method,
      service: selectedService,
      port: selectedPort,
      path,
      status: result.status,
      elapsed: result.elapsed,
      request: { headers, body: reqBody },
      response: result,
    };
    setHistory(prev => [entry, ...prev].slice(0, 50));
  }, [selectedService, selectedPort, method, path, reqHeaders, reqBody, saveBody]);

  // Replay from history
  const handleReplay = useCallback((entry: HistoryEntry) => {
    setMethod(entry.method);
    setSelectedService(entry.service);
    setSelectedPort(entry.port);
    setPath(entry.path);
    setReqHeaders(Object.keys(entry.request.headers).length > 0 ? JSON.stringify(entry.request.headers, null, 2) : '');
    setReqBody(entry.request.body);
    setResponse(entry.response || null);
    setShowHistory(false);
    if (Object.keys(entry.request.headers).length > 0) setShowHeaders(true);
    if (entry.request.body) setShowBody(true);
  }, []);

  // Keyboard shortcut: Ctrl/Cmd+Enter to send
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
        e.preventDefault();
        handleSend();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [handleSend]);

  // Format response body
  const formatBody = (body: string | undefined) => {
    if (!body) return '';
    try {
      return JSON.stringify(JSON.parse(body), null, 2);
    } catch {
      return body;
    }
  };

  const statusClass = (status: number | undefined) => {
    if (!status) return '';
    if (status < 300) return 'status-ok';
    if (status < 400) return 'status-redirect';
    if (status < 500) return 'status-client-err';
    return 'status-server-err';
  };

  const currentService = services.find(s => s.name === selectedService);

  // Build a stub request body from discovered params
  const buildStubBody = useCallback((ep: ApiEndpoint): string => {
    if (!ep.requestBody) return '';
    // If we have parameter hints, generate a skeleton
    const params = ep.parameters?.filter(p => p.in === 'body') || [];
    if (params.length > 0) {
      const stub: Record<string, string> = {};
      params.forEach(p => { stub[p.name] = p.type === 'number' ? '0' as any : ''; });
      return JSON.stringify(stub, null, 2);
    }
    return JSON.stringify({ key: 'value' }, null, 2);
  }, []);

  // Click an endpoint → pre-fill AND immediately fire the request
  const handleEndpointClick = useCallback(async (ep: ApiEndpoint) => {
    const m = ep.method as HttpMethod;
    // Prefer a previously-saved body, fall back to stub
    const key = bodyKey(selectedService, m, ep.path);
    const saved = savedBodies[key];
    const body = saved || ((m === 'POST' || m === 'PUT' || m === 'PATCH') ? buildStubBody(ep) : '');

    setMethod(m);
    setPath(ep.path);
    setReqBody(body);
    if (body) setShowBody(true);

    // Fire immediately
    setSending(true);
    setResponse(null);

    const result = await proxyRequest({
      service: selectedService,
      port: selectedPort,
      method: m,
      path: ep.path,
      body: body || undefined,
    });

    setResponse(result);
    setSending(false);

    // Persist the body for next time
    if (body.trim()) {
      saveBody(selectedService, m, ep.path, body);
    }

    const entry: HistoryEntry = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      timestamp: Date.now(),
      method: m,
      service: selectedService,
      port: selectedPort,
      path: ep.path,
      status: result.status,
      elapsed: result.elapsed,
      request: { headers: {}, body },
      response: result,
    };
    setHistory(prev => [entry, ...prev].slice(0, 50));
  }, [selectedService, selectedPort, buildStubBody, savedBodies, saveBody]);

  return (
    <div className="api-explorer">
      {/* Header */}
      <div className="api-explorer-header">
        <div className="api-explorer-title">
          <h2>API Explorer</h2>
          <span className="text-dim">Send requests to in-cluster services</span>
        </div>
        <div style={{ display: 'flex', gap: 6 }}>
          <button
            className={`btn btn-sm ${showEndpoints ? 'btn-primary' : 'btn-ghost'}`}
            onClick={() => setShowEndpoints(!showEndpoints)}
          >
            ⚡ Endpoints {apiSpec ? `(${apiSpec.endpoints.length})` : ''}
          </button>
          <button
            className={`btn btn-sm ${showHistory ? 'btn-primary' : 'btn-ghost'}`}
            onClick={() => setShowHistory(!showHistory)}
          >
            ↻ History ({history.length})
          </button>
        </div>
      </div>

      {/* Service Picker */}
      <div className="api-service-picker">
        <label className="api-service-picker-label">Service</label>
        <select
          className="api-service-picker-select"
          value={selectedService}
          onChange={(e) => handleServiceChange(e.target.value)}
          disabled={loadingServices}
        >
          {loadingServices && <option>Loading…</option>}
          {services.map(s => (
            <option key={`${s.namespace}/${s.name}`} value={s.name}>
              {s.name}
            </option>
          ))}
          {!loadingServices && services.length === 0 && (
            <option>No services deployed</option>
          )}
        </select>
        {currentService && currentService.ports.length > 1 ? (
          <select
            className="api-service-picker-port"
            value={selectedPort}
            onChange={(e) => setSelectedPort(parseInt(e.target.value))}
          >
            {currentService.ports.map(p => (
              <option key={p.port} value={p.port}>:{p.port}{p.name ? ` (${p.name})` : ''}</option>
            ))}
          </select>
        ) : (
          <span className="api-service-picker-port-badge">:{selectedPort}</span>
        )}
        {apiSpec?.framework && (
          <span className="api-spec-framework">{apiSpec.framework}</span>
        )}
      </div>

      <div className="api-explorer-body">
        {/* Discovered Endpoints */}
        {showEndpoints && (
          <div className="api-endpoints-panel">
            {loadingSpec && (
              <div className="api-endpoints-loading">
                <div className="api-response-spinner" />
                <span>Discovering endpoints…</span>
              </div>
            )}
            {!loadingSpec && !apiSpec && selectedService && (
              <div className="api-endpoints-empty">No endpoints discovered</div>
            )}
            {!loadingSpec && apiSpec && (
              <>
                {apiSpec.endpoints.length > 0 && (
                  <div className="api-endpoints-meta">
                    <span className="api-spec-badge">
                      {apiSpec.endpoints.length} endpoint{apiSpec.endpoints.length !== 1 ? 's' : ''} detected
                    </span>
                    {apiSpec.host && (
                      <span className="api-spec-host">{apiSpec.host}</span>
                    )}
                  </div>
                )}
                <div className="api-endpoints-list">
                  {apiSpec.endpoints.map((ep, i) => {
                    const hasSaved = !!savedBodies[bodyKey(selectedService, ep.method, ep.path)];
                    return (
                      <button
                        key={`${ep.method}-${ep.path}-${i}`}
                        className={`api-endpoint-item${hasSaved ? ' has-saved-body' : ''}`}
                        onClick={() => handleEndpointClick(ep)}
                        title={`Send ${ep.method} ${ep.path}${hasSaved ? ' (saved body)' : ''}`}
                      >
                        <span className="api-endpoint-fire">▶</span>
                        <span
                          className="api-endpoint-method"
                          style={{ color: METHOD_COLORS[ep.method as HttpMethod] || '#999' }}
                        >
                          {ep.method}
                        </span>
                        <span className="api-endpoint-path">{ep.path}</span>
                        {hasSaved && <span className="api-endpoint-saved" title="Saved request body">●</span>}
                        {ep.summary && (
                          <span className="api-endpoint-summary">{ep.summary}</span>
                        )}
                      </button>
                    );
                  })}
                </div>
              </>
            )}
          </div>
        )}

        {/* Request Builder */}
        <div className="api-explorer-request">
          {/* URL Bar */}
          <div className="api-url-bar">
            {/* Method selector */}
            <select
              className="api-method-select"
              value={method}
              onChange={(e) => setMethod(e.target.value as HttpMethod)}
              style={{ color: METHOD_COLORS[method] }}
            >
              {METHODS.map(m => (
                <option key={m} value={m} style={{ color: METHOD_COLORS[m] }}>{m}</option>
              ))}
            </select>

            {/* Path input */}
            <input
              ref={pathInputRef}
              className="api-path-input"
              type="text"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/api/endpoint"
              spellCheck={false}
            />

            {/* Send button */}
            <button
              className="api-send-btn"
              onClick={handleSend}
              disabled={sending || !selectedService}
            >
              {sending ? '⏳' : '▶'} Send
            </button>
          </div>

          {/* Collapsible sections */}
          <div className="api-sections">
            <button className="api-section-toggle" onClick={() => setShowHeaders(!showHeaders)}>
              <span className="api-section-arrow">{showHeaders ? '▾' : '▸'}</span>
              Headers
              {reqHeaders.trim() && <span className="api-section-badge">●</span>}
            </button>
            {showHeaders && (
              <textarea
                className="api-editor"
                value={reqHeaders}
                onChange={(e) => setReqHeaders(e.target.value)}
                placeholder={'{\n  "Authorization": "Bearer ...",\n  "Content-Type": "application/json"\n}'}
                rows={5}
                spellCheck={false}
              />
            )}

            {(method === 'POST' || method === 'PUT' || method === 'PATCH') && (
              <>
                <button className="api-section-toggle" onClick={() => setShowBody(!showBody)}>
                  <span className="api-section-arrow">{showBody ? '▾' : '▸'}</span>
                  Body
                  {reqBody.trim() && <span className="api-section-badge">●</span>}
                </button>
                {showBody && (
                  <textarea
                    className="api-editor api-body-editor"
                    value={reqBody}
                    onChange={(e) => setReqBody(e.target.value)}
                    placeholder={'{\n  "key": "value"\n}'}
                    rows={8}
                    spellCheck={false}
                  />
                )}
              </>
            )}
          </div>

          <div className="api-shortcut-hint">
            <kbd>⌘</kbd> + <kbd>Enter</kbd> to send
          </div>
        </div>

        {/* Response Panel */}
        <div className="api-explorer-response">
          {!response && !sending && (
            <div className="api-response-empty">
              <div className="api-response-empty-icon">⇆</div>
              <div>Send a request to see the response</div>
            </div>
          )}

          {sending && (
            <div className="api-response-loading">
              <div className="api-response-spinner" />
              <div>Sending request…</div>
            </div>
          )}

          {response && !sending && (
            <>
              {/* Status bar */}
              <div className="api-response-status-bar">
                {response.status ? (
                  <>
                    <span className={`api-status-badge ${statusClass(response.status)}`}>
                      {response.status}
                    </span>
                    <span className="api-response-time">{response.elapsed}ms</span>
                    <span className="api-response-size">{formatSize(response.size || 0)}</span>
                  </>
                ) : (
                  <span className="api-status-badge status-error">
                    {response.error || 'Request failed'}
                  </span>
                )}

                <div className="api-response-actions">
                  <button
                    className={`btn btn-xs ${showResponseHeaders ? 'btn-primary' : 'btn-ghost'}`}
                    onClick={() => setShowResponseHeaders(!showResponseHeaders)}
                  >
                    Headers
                  </button>
                  <button
                    className="btn btn-xs btn-ghost"
                    onClick={() => {
                      if (response.body) navigator.clipboard.writeText(response.body);
                    }}
                    title="Copy response"
                  >
                    ⎘ Copy
                  </button>
                </div>
              </div>

              {/* Response headers */}
              {showResponseHeaders && response.headers && (
                <div className="api-response-headers">
                  {Object.entries(response.headers).map(([k, v]) => (
                    <div key={k} className="api-response-header-row">
                      <span className="api-header-key">{k}</span>
                      <span className="api-header-val">{v}</span>
                    </div>
                  ))}
                </div>
              )}

              {/* Response body */}
              <pre className="api-response-body">
                <code>{formatBody(response.body)}</code>
              </pre>
            </>
          )}
        </div>
      </div>

      {/* History Sidebar */}
      {showHistory && (
        <>
          <div className="api-history-backdrop" onClick={() => setShowHistory(false)} />
          <div className="api-history-sidebar">
            <div className="api-history-header">
              <h3>Request History</h3>
              {history.length > 0 && (
                <button
                  className="btn btn-xs btn-ghost"
                  onClick={() => { setHistory([]); localStorage.removeItem('kindling-api-history'); }}
                >
                  Clear
                </button>
              )}
            </div>
          <div className="api-history-list">
            {history.length === 0 && (
              <div className="api-history-empty">No requests yet</div>
            )}
            {history.map(entry => (
              <button
                key={entry.id}
                className="api-history-item"
                onClick={() => handleReplay(entry)}
              >
                <div className="api-history-item-top">
                  <span className="api-history-method" style={{ color: METHOD_COLORS[entry.method] }}>
                    {entry.method}
                  </span>
                  <span className="api-history-service">{entry.service}</span>
                  {entry.status && (
                    <span className={`api-history-status ${statusClass(entry.status)}`}>
                      {entry.status}
                    </span>
                  )}
                </div>
                <div className="api-history-path">{entry.path}</div>
                <div className="api-history-time">
                  {new Date(entry.timestamp).toLocaleTimeString()}
                  {entry.elapsed !== undefined && <span> · {entry.elapsed}ms</span>}
                </div>
              </button>
            ))}
          </div>
        </div>
        </>
      )}
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

import { useState, useEffect, useCallback } from 'react'

/* ── Types ─────────────────────────────────────────────────────── */

interface ServiceStatus {
  status: string
  error?: string
  version?: string
  sealed?: string
  brokers?: string
  note?: string
}

interface StatusResponse {
  app: string
  time: string
  postgres: ServiceStatus
  redis: ServiceStatus
  elasticsearch: ServiceStatus
  kafka: ServiceStatus
  vault: ServiceStatus
}

interface HistoryEntry {
  time: Date
  services: Record<string, boolean>
}

/* ── Service metadata ──────────────────────────────────────────── */

const SERVICES = [
  {
    key: 'postgres',
    label: 'PostgreSQL',
    icon: '🐘',
    color: '#336791',
    description: 'Primary datastore',
    envVar: 'DATABASE_URL',
  },
  {
    key: 'redis',
    label: 'Redis',
    icon: '⚡',
    color: '#dc382d',
    description: 'Cache & pub/sub',
    envVar: 'REDIS_URL',
  },
  {
    key: 'elasticsearch',
    label: 'Elasticsearch',
    icon: '🔍',
    color: '#00bfb3',
    description: 'Search & analytics',
    envVar: 'ELASTICSEARCH_URL',
  },
  {
    key: 'kafka',
    label: 'Kafka',
    icon: '📨',
    color: '#231f20',
    description: 'Event streaming',
    envVar: 'KAFKA_BROKER_URL',
  },
  {
    key: 'vault',
    label: 'Vault',
    icon: '🔐',
    color: '#ffec6e',
    description: 'Secrets management',
    envVar: 'VAULT_ADDR',
  },
] as const

/* ── Helpers ───────────────────────────────────────────────────── */

function isConnected(s?: ServiceStatus): boolean {
  return s?.status === 'connected'
}

function uptimeStr(history: HistoryEntry[], key: string): string {
  if (history.length === 0) return '—'
  const up = history.filter((h) => h.services[key]).length
  return `${Math.round((up / history.length) * 100)}%`
}

function timeAgo(date: Date): string {
  const s = Math.floor((Date.now() - date.getTime()) / 1000)
  if (s < 5) return 'just now'
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

/* ── App ───────────────────────────────────────────────────────── */

export default function App() {
  const [status, setStatus] = useState<StatusResponse | null>(null)
  const [connected, setConnected] = useState(true)
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [lastCheck, setLastCheck] = useState<Date | null>(null)
  const [eventLog, setEventLog] = useState<
    { id: number; msg: string; time: Date; ok: boolean }[]
  >([])

  let nextId = 0

  const fetchStatus = useCallback(async () => {
    try {
      const res = await fetch('/api/status')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data: StatusResponse = await res.json()
      setStatus(data)
      setConnected(true)
      setLastCheck(new Date())

      // Update history (keep last 60 entries = ~5 minutes at 5s interval)
      const entry: HistoryEntry = {
        time: new Date(),
        services: {},
      }
      for (const svc of SERVICES) {
        const svcStatus = data[svc.key as keyof StatusResponse] as ServiceStatus
        entry.services[svc.key] = isConnected(svcStatus)
      }
      setHistory((prev) => [...prev, entry].slice(-60))

      // Log state changes
      setStatus((prev) => {
        if (prev) {
          const newLogs: typeof eventLog = []
          for (const svc of SERVICES) {
            const was = isConnected(
              prev[svc.key as keyof StatusResponse] as ServiceStatus,
            )
            const now = isConnected(
              data[svc.key as keyof StatusResponse] as ServiceStatus,
            )
            if (was !== now) {
              newLogs.push({
                id: ++nextId,
                msg: `${svc.label} ${now ? 'connected' : 'disconnected'}`,
                time: new Date(),
                ok: now,
              })
            }
          }
          if (newLogs.length > 0) {
            setEventLog((prev) => [...newLogs, ...prev].slice(0, 50))
          }
        }
        return data
      })
    } catch {
      setConnected(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    fetchStatus()
    const id = setInterval(fetchStatus, 5000)
    return () => clearInterval(id)
  }, [fetchStatus])

  /* ── Derived ─────────────────────────────────────────────────── */

  const healthyCount = SERVICES.filter((svc) =>
    isConnected(status?.[svc.key as keyof StatusResponse] as ServiceStatus),
  ).length

  const allHealthy = healthyCount === SERVICES.length

  /* ── Render ──────────────────────────────────────────────────── */

  return (
    <div className="app">
      {/* Header */}
      <header className="header">
        <div className="header-left">
          <svg className="logo" viewBox="0 0 32 32" width="28" height="28">
            <defs>
              <linearGradient id="flame" x1="0" y1="1" x2="0" y2="0">
                <stop offset="0%" stopColor="#f97316" />
                <stop offset="100%" stopColor="#fbbf24" />
              </linearGradient>
            </defs>
            <path
              d="M16 2c0 0-6 8-6 14a6 6 0 0012 0c0-6-6-14-6-14z"
              fill="url(#flame)"
            />
            <path
              d="M16 12c0 0-3 4-3 7a3 3 0 006 0c0-3-3-7-3-7z"
              fill="rgba(255,255,255,0.2)"
            />
          </svg>
          <h1>
            Kindling <span className="subtle">Platform Dashboard</span>
          </h1>
        </div>
        <div className="header-right">
          <span className={`connection-dot ${connected ? 'ok' : 'err'}`} />
          <span className="connection-label">
            {connected ? 'Connected' : 'Disconnected'}
          </span>
        </div>
      </header>

      {/* Overview Bar */}
      <div className="overview-bar">
        <div className={`overall-status ${allHealthy ? 'ok' : 'warn'}`}>
          <span className="overall-icon">{allHealthy ? '✅' : '⚠️'}</span>
          <span>
            {allHealthy
              ? 'All systems operational'
              : `${healthyCount}/${SERVICES.length} services healthy`}
          </span>
        </div>
        <div className="overview-meta">
          {lastCheck && (
            <span className="last-check">
              Last check: {lastCheck.toLocaleTimeString()}
            </span>
          )}
          {status?.app && (
            <span className="api-badge">{status.app}</span>
          )}
        </div>
      </div>

      {/* Dashboard */}
      <main className="dashboard">
        {/* ── Service Cards ─────────────────────────────────────── */}
        <section className="services-section">
          <h2>🏗️ Infrastructure Services</h2>
          <div className="services-grid">
            {SERVICES.map((svc) => {
              const svcStatus = status?.[
                svc.key as keyof StatusResponse
              ] as ServiceStatus | undefined
              const up = isConnected(svcStatus)
              return (
                <div
                  key={svc.key}
                  className={`service-card ${up ? 'ok' : 'err'}`}
                >
                  <div className="service-header">
                    <span className="service-icon">{svc.icon}</span>
                    <div className="service-name-group">
                      <span className="service-name">{svc.label}</span>
                      <span className="service-desc">{svc.description}</span>
                    </div>
                    <span className={`service-badge ${up ? 'ok' : 'err'}`}>
                      {up ? 'Connected' : svcStatus?.status || 'Checking…'}
                    </span>
                  </div>

                  <div className="service-details">
                    <div className="detail-row">
                      <span className="detail-label">Env</span>
                      <code className="detail-value">{svc.envVar}</code>
                    </div>
                    {svcStatus?.version && (
                      <div className="detail-row">
                        <span className="detail-label">Version</span>
                        <span className="detail-value">
                          {svcStatus.version}
                        </span>
                      </div>
                    )}
                    {svcStatus?.brokers && (
                      <div className="detail-row">
                        <span className="detail-label">Brokers</span>
                        <span className="detail-value">
                          {svcStatus.brokers}
                        </span>
                      </div>
                    )}
                    {svcStatus?.sealed !== undefined && (
                      <div className="detail-row">
                        <span className="detail-label">Sealed</span>
                        <span className="detail-value">
                          {svcStatus.sealed === 'false' ? '🟢 No' : '🔴 Yes'}
                        </span>
                      </div>
                    )}
                    {svcStatus?.error && (
                      <div className="detail-row error-row">
                        <span className="detail-label">Error</span>
                        <span className="detail-value error-value">
                          {svcStatus.error}
                        </span>
                      </div>
                    )}
                    <div className="detail-row">
                      <span className="detail-label">Uptime</span>
                      <span className="detail-value">
                        {uptimeStr(history, svc.key)}
                      </span>
                    </div>
                  </div>

                  {/* Mini sparkline bar */}
                  <div className="sparkline">
                    {history.slice(-30).map((h, i) => (
                      <div
                        key={i}
                        className={`spark-bar ${h.services[svc.key] ? 'up' : 'down'}`}
                        title={h.time.toLocaleTimeString()}
                      />
                    ))}
                  </div>
                </div>
              )
            })}
          </div>
        </section>

        {/* ── Sidebar: Event Log + Info ─────────────────────────── */}
        <aside className="sidebar">
          <h2>⚡ Event Log</h2>
          <div className="event-log">
            {eventLog.length === 0 ? (
              <p className="empty">
                State changes will appear here as services connect and
                disconnect
              </p>
            ) : (
              eventLog.map((entry) => (
                <div
                  key={entry.id}
                  className={`event-entry ${entry.ok ? 'ok' : 'err'}`}
                >
                  <span className="event-icon">
                    {entry.ok ? '🟢' : '🔴'}
                  </span>
                  <span className="event-msg">{entry.msg}</span>
                  <span className="event-time">{timeAgo(entry.time)}</span>
                </div>
              ))
            )}
          </div>

          <h2>📋 API Endpoints</h2>
          <div className="endpoints-card">
            <div className="endpoint-row">
              <code>GET /</code>
              <span className="endpoint-desc">Hello message</span>
            </div>
            <div className="endpoint-row">
              <code>GET /healthz</code>
              <span className="endpoint-desc">Liveness probe</span>
            </div>
            <div className="endpoint-row">
              <code>GET /status</code>
              <span className="endpoint-desc">All service connectivity</span>
            </div>
          </div>

          <h2>🔗 Quick Links</h2>
          <div className="links-card">
            <a href="/api/" className="quick-link" target="_blank">
              API Root →
            </a>
            <a href="/api/healthz" className="quick-link" target="_blank">
              Health Check →
            </a>
            <a href="/api/status" className="quick-link" target="_blank">
              Raw Status JSON →
            </a>
          </div>
        </aside>
      </main>
    </div>
  )
}

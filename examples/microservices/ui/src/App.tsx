import { useState, useEffect, useCallback, useRef, FormEvent } from 'react'

/* ── Types ─────────────────────────────────────────────────────── */

interface ServiceHealth {
  status: string
}

interface Status {
  service: string
  time: string
  orders: ServiceHealth
  inventory: ServiceHealth
}

interface Order {
  id: number
  product: string
  quantity: number
  status: string
  created_at: string
}

interface InventoryItem {
  name: string
  stock: number
  updated_at: string
}

interface ActivityEntry {
  id: number
  message: string
  timestamp: Date
  type: 'order' | 'stock'
}

/* ── Helpers ───────────────────────────────────────────────────── */

let nextActivityId = 0

function timeAgo(date: Date): string {
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000)
  if (seconds < 5) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  return `${Math.floor(seconds / 3600)}h ago`
}

function isHealthy(s?: ServiceHealth): boolean {
  return !!s?.status?.includes('ok')
}

/* ── App ───────────────────────────────────────────────────────── */

export default function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [orders, setOrders] = useState<Order[]>([])
  const [inventory, setInventory] = useState<InventoryItem[]>([])
  const [activity, setActivity] = useState<ActivityEntry[]>([])
  const [product, setProduct] = useState('widget-a')
  const [quantity, setQuantity] = useState(1)
  const [submitting, setSubmitting] = useState(false)
  const [toast, setToast] = useState<{
    message: string
    type: 'success' | 'error'
  } | null>(null)
  const [connected, setConnected] = useState(true)
  const prevStock = useRef<Map<string, number>>(new Map())

  /* ── Toast helper ────────────────────────────────────────────── */

  const showToast = useCallback(
    (message: string, type: 'success' | 'error' = 'success') => {
      setToast({ message, type })
      setTimeout(() => setToast(null), 3000)
    },
    [],
  )

  /* ── Data fetching ───────────────────────────────────────────── */

  const fetchData = useCallback(async () => {
    try {
      const [statusRes, ordersRes, inventoryRes] = await Promise.all([
        fetch('/api/status'),
        fetch('/api/orders'),
        fetch('/api/inventory'),
      ])

      if (statusRes.ok) setStatus(await statusRes.json())
      if (ordersRes.ok) setOrders(await ordersRes.json())

      if (inventoryRes.ok) {
        const items: InventoryItem[] = await inventoryRes.json()
        setInventory(items)

        // Track stock deltas for the activity log
        if (prevStock.current.size > 0) {
          const newEntries: ActivityEntry[] = []
          for (const item of items) {
            const prev = prevStock.current.get(item.name)
            if (prev !== undefined && prev !== item.stock) {
              const delta = item.stock - prev
              newEntries.push({
                id: ++nextActivityId,
                message: `${item.name}: ${prev} → ${item.stock} (${delta > 0 ? '+' : ''}${delta})`,
                timestamp: new Date(),
                type: 'stock',
              })
            }
          }
          if (newEntries.length > 0) {
            setActivity((prev) => [...newEntries, ...prev].slice(0, 50))
          }
        }
        prevStock.current = new Map(items.map((i) => [i.name, i.stock]))
      }

      setConnected(true)
    } catch {
      setConnected(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const id = setInterval(fetchData, 3000)
    return () => clearInterval(id)
  }, [fetchData])

  /* ── Order creation ──────────────────────────────────────────── */

  const createOrder = async (e: FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    try {
      const res = await fetch('/api/orders', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ product, quantity }),
      })
      if (!res.ok) throw new Error('Failed to create order')
      const order: Order = await res.json()

      showToast(`Order #${order.id} placed — ${order.product} ×${order.quantity}`)
      setActivity((prev) =>
        [
          {
            id: ++nextActivityId,
            message: `Order #${order.id}: ${order.product} ×${order.quantity}`,
            timestamp: new Date(),
            type: 'order' as const,
          },
          ...prev,
        ].slice(0, 50),
      )

      // Re-fetch soon + again after queue processing
      setTimeout(fetchData, 500)
      setTimeout(fetchData, 3000)
    } catch {
      showToast('Failed to create order', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  /* ── Derived values ──────────────────────────────────────────── */

  const maxStock = Math.max(...inventory.map((i) => i.stock), 1)

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
            Kindling <span className="subtle">Microservices Demo</span>
          </h1>
        </div>
        <div className="header-right">
          <span className={`connection-dot ${connected ? 'ok' : 'err'}`} />
          <span className="connection-label">
            {connected ? 'Connected' : 'Disconnected'}
          </span>
        </div>
      </header>

      {/* Status Bar */}
      <div className="status-bar">
        {[
          { name: 'Gateway', ok: !!status },
          { name: 'Orders', ok: isHealthy(status?.orders) },
          { name: 'Inventory', ok: isHealthy(status?.inventory) },
        ].map((svc) => (
          <div
            key={svc.name}
            className={`status-chip ${svc.ok ? 'ok' : 'err'}`}
          >
            <span className="status-dot" />
            {svc.name}
          </div>
        ))}
        {status && (
          <span className="status-time">
            Last check: {new Date(status.time).toLocaleTimeString()}
          </span>
        )}
      </div>

      {/* Dashboard */}
      <main className="dashboard">
        {/* ── Left: Orders ──────────────────────────────────────── */}
        <section className="panel">
          <h2>📦 Place Order</h2>
          <form className="order-form" onSubmit={createOrder}>
            <div className="form-row">
              <label>
                Product
                <select
                  value={product}
                  onChange={(e) => setProduct(e.target.value)}
                >
                  {(inventory.length > 0
                    ? inventory.map((i) => i.name)
                    : ['widget-a', 'widget-b', 'gadget-x']
                  ).map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Qty
                <input
                  type="number"
                  min={1}
                  max={999}
                  value={quantity}
                  onChange={(e) => setQuantity(Number(e.target.value))}
                />
              </label>
              <button type="submit" disabled={submitting}>
                {submitting ? 'Placing…' : 'Place Order'}
              </button>
            </div>
          </form>

          <h2>📋 Recent Orders</h2>
          <div className="orders-list">
            {orders.length === 0 ? (
              <p className="empty">No orders yet — place one above!</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Product</th>
                    <th>Qty</th>
                    <th>Status</th>
                    <th>Time</th>
                  </tr>
                </thead>
                <tbody>
                  {orders.map((o) => (
                    <tr key={o.id}>
                      <td className="mono">{o.id}</td>
                      <td>{o.product}</td>
                      <td className="mono">{o.quantity}</td>
                      <td>
                        <span className={`badge ${o.status}`}>
                          {o.status}
                        </span>
                      </td>
                      <td className="subtle">
                        {new Date(o.created_at).toLocaleTimeString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </section>

        {/* ── Right: Inventory + Activity ───────────────────────── */}
        <section className="panel">
          <h2>📊 Inventory</h2>
          <div className="inventory-grid">
            {inventory.map((item) => (
              <div key={item.name} className="inventory-card">
                <div className="inventory-header">
                  <span className="inventory-name">{item.name}</span>
                  <span className="inventory-stock">{item.stock}</span>
                </div>
                <div className="stock-bar">
                  <div
                    className="stock-fill"
                    style={{
                      width: `${(item.stock / maxStock) * 100}%`,
                    }}
                  />
                </div>
              </div>
            ))}
          </div>

          <h2>⚡ Activity</h2>
          <div className="activity-log">
            {activity.length === 0 ? (
              <p className="empty">
                Activity will appear here as orders flow through the system
              </p>
            ) : (
              activity.map((entry) => (
                <div
                  key={entry.id}
                  className={`activity-entry ${entry.type}`}
                >
                  <span className="activity-icon">
                    {entry.type === 'order' ? '📦' : '📉'}
                  </span>
                  <span className="activity-message">{entry.message}</span>
                  <span className="activity-time">
                    {timeAgo(entry.timestamp)}
                  </span>
                </div>
              ))
            )}
          </div>
        </section>
      </main>

      {/* Toast Notification */}
      {toast && (
        <div className={`toast ${toast.type}`}>
          {toast.type === 'success' ? '✅' : '❌'} {toast.message}
        </div>
      )}
    </div>
  )
}

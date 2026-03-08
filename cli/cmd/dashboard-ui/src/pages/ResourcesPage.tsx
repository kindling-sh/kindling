import { useState } from 'react';
import { useApi, apiPost } from '../api';
import type {
  K8sList,
  K8sService,
  K8sIngress,
  K8sSecret,
  K8sEvent,
  K8sServiceAccount,
  K8sRole,
  K8sRoleBinding,
  K8sClusterRole,
  K8sClusterRoleBinding,
} from '../types';
import { StatusBadge, LabelBadges, EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

type ResourceTab = 'services' | 'ingresses' | 'secrets' | 'events' | 'rbac';

const TABS: { key: ResourceTab; icon: string; label: string }[] = [
  { key: 'services', icon: '◎', label: 'Services' },
  { key: 'ingresses', icon: '⊕', label: 'Ingresses' },
  { key: 'secrets', icon: '◈', label: 'Secrets' },
  { key: 'events', icon: '◇', label: 'Events' },
  { key: 'rbac', icon: '⊘', label: 'RBAC' },
];

export function ResourcesPage() {
  const [tab, setTab] = useState<ResourceTab>('services');

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Cluster Resources</h1>
          <p className="page-subtitle">Services, ingresses, secrets, events &amp; RBAC</p>
        </div>
      </div>

      <div className="tab-bar">
        {TABS.map((t) => (
          <button
            key={t.key}
            className={`tab-btn${tab === t.key ? ' tab-active' : ''}`}
            onClick={() => setTab(t.key)}
          >
            <span style={{ marginRight: 6 }}>{t.icon}</span>
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'services' && <ServicesTab />}
      {tab === 'ingresses' && <IngressesTab />}
      {tab === 'secrets' && <SecretsTab />}
      {tab === 'events' && <EventsTab />}
      {tab === 'rbac' && <RBACTab />}
    </div>
  );
}

// ── Services ────────────────────────────────────────────────────

function ServicesTab() {
  const { data, loading } = useApi<K8sList<K8sService>>('/api/services');

  if (loading) return <div className="loading">Loading services…</div>;

  const services = (data?.items || []).filter(
    (s) => s.metadata.namespace !== 'kube-system' && s.metadata.namespace !== 'local-path-storage'
  );

  if (services.length === 0) {
    return <EmptyState icon="○" message="No services found in workload namespaces." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Type</th>
            <th>Cluster IP</th>
            <th>Ports</th>
            <th>Selector</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {services.map((svc) => {
            const ports = svc.spec.ports?.map((p) => {
              let s = `${p.port}`;
              if (p.targetPort) s += `→${p.targetPort}`;
              if (p.protocol && p.protocol !== 'TCP') s += `/${p.protocol}`;
              if (p.nodePort) s += ` (node:${p.nodePort})`;
              return s;
            }).join(', ') || '—';

            return (
              <tr key={`${svc.metadata.namespace}/${svc.metadata.name}`}>
                <td className="mono">{svc.metadata.name}</td>
                <td><span className="tag">{svc.metadata.namespace}</span></td>
                <td>
                  <StatusBadge
                    ok={svc.spec.type === 'ClusterIP' || svc.spec.type === 'LoadBalancer'}
                    label={svc.spec.type || 'ClusterIP'}
                  />
                </td>
                <td className="mono">{svc.spec.clusterIP || '—'}</td>
                <td className="mono">{ports}</td>
                <td>
                  {svc.spec.selector ? (
                    <LabelBadges labels={svc.spec.selector} />
                  ) : (
                    <span className="text-dim">—</span>
                  )}
                </td>
                <td><TimeAgo timestamp={svc.metadata.creationTimestamp} /></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ── Ingresses ───────────────────────────────────────────────────

function IngressesTab() {
  const { data, loading } = useApi<K8sList<K8sIngress>>('/api/ingresses');

  if (loading) return <div className="loading">Loading ingresses…</div>;

  const ingresses = data?.items || [];

  if (ingresses.length === 0) {
    return <EmptyState icon="◎" message="No ingresses configured." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Class</th>
            <th>Hosts</th>
            <th>Paths</th>
            <th>Status</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {ingresses.map((ing) => {
            const rules = ing.spec.rules || [];
            const hosts = rules.map((r) => r.host || '*').join(', ') || '—';
            const paths = rules.flatMap((r) =>
              (r.http?.paths || []).map((p) => {
                const svc = p.backend?.service;
                const pathStr = p.path || '/';
                const svcStr = svc ? `${svc.name}:${svc.port?.number || svc.port?.name || '?'}` : '?';
                return `${pathStr} → ${svcStr}`;
              })
            );
            const hasIP = ing.status?.loadBalancer?.ingress?.some((i: any) => i.ip || i.hostname);

            return (
              <tr key={`${ing.metadata.namespace}/${ing.metadata.name}`}>
                <td className="mono">{ing.metadata.name}</td>
                <td><span className="tag">{ing.metadata.namespace}</span></td>
                <td className="mono">{ing.spec.ingressClassName || '—'}</td>
                <td className="mono">{hosts}</td>
                <td>
                  {paths.length > 0 ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                      {paths.map((p, i) => (
                        <span key={i} className="mono" style={{ fontSize: '0.8em' }}>{p}</span>
                      ))}
                    </div>
                  ) : (
                    <span className="text-dim">—</span>
                  )}
                </td>
                <td>
                  <StatusBadge ok={!!hasIP} label={hasIP ? 'Active' : 'Pending'} />
                </td>
                <td><TimeAgo timestamp={ing.metadata.creationTimestamp} /></td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ── Secrets ─────────────────────────────────────────────────────

function SecretsTab() {
  const { data, loading, refresh } = useApi<K8sList<K8sSecret>>('/api/secrets');
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<K8sSecret | null>(null);
  const [form, setForm] = useState({ name: '', namespace: 'default', key: '', value: '' });

  async function handleCreate() {
    setCreating(true);
    const result = await apiPost('/api/secrets/create', form);
    setCreating(false);
    if (result.ok) {
      toast('Secret created', 'success');
      setShowCreate(false);
      setForm({ name: '', namespace: 'default', key: '', value: '' });
      refresh();
    } else {
      toast(result.error || 'Failed to create secret', 'error');
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const result = await apiPost('/api/secrets/delete', {
      name: deleteTarget.metadata.name,
      namespace: deleteTarget.metadata.namespace,
    });
    setDeleteTarget(null);
    if (result.ok) {
      toast('Secret deleted', 'success');
      refresh();
    } else {
      toast(result.error || 'Failed to delete secret', 'error');
    }
  }

  if (loading) return <div className="loading">Loading secrets…</div>;

  const secrets = (data?.items || []).filter(
    (s) => s.metadata.namespace !== 'kube-system' && s.metadata.namespace !== 'local-path-storage'
  );

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <ActionButton icon="+" label="Create Secret" onClick={() => setShowCreate(true)} primary />
      </div>

      {showCreate && (
        <ActionModal
          title="Create Secret"
          submitLabel="Create"
          loading={creating}
          onSubmit={handleCreate}
          onClose={() => setShowCreate(false)}
        >
          <label className="form-label">Name</label>
          <input className="form-input" placeholder="my-secret" value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <label className="form-label">Namespace</label>
          <input className="form-input" placeholder="default" value={form.namespace}
            onChange={(e) => setForm({ ...form, namespace: e.target.value })} />
          <label className="form-label">Key</label>
          <input className="form-input" placeholder="SECRET_KEY" value={form.key}
            onChange={(e) => setForm({ ...form, key: e.target.value })} />
          <label className="form-label">Value</label>
          <input className="form-input" type="password" placeholder="secret value" value={form.value}
            onChange={(e) => setForm({ ...form, value: e.target.value })} />
        </ActionModal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete Secret"
          message={`Delete secret "${deleteTarget.metadata.name}" from namespace "${deleteTarget.metadata.namespace}"?`}
          confirmLabel="Delete"
          danger
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {secrets.length === 0 ? (
        <EmptyState icon="⊕" message="No secrets found. Create one to get started." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Type</th>
                <th>Keys</th>
                <th>Age</th>
                <th style={{ width: 50 }}></th>
              </tr>
            </thead>
            <tbody>
              {secrets.map((sec) => {
                const keys = sec.data ? Object.keys(sec.data) : [];
                return (
                  <tr key={`${sec.metadata.namespace}/${sec.metadata.name}`}>
                    <td className="mono">{sec.metadata.name}</td>
                    <td><span className="tag">{sec.metadata.namespace}</span></td>
                    <td className="mono" style={{ fontSize: '0.8em' }}>{sec.type || 'Opaque'}</td>
                    <td>
                      {keys.length > 0 ? (
                        <span>{keys.length} key{keys.length !== 1 ? 's' : ''}</span>
                      ) : (
                        <span className="text-dim">empty</span>
                      )}
                    </td>
                    <td><TimeAgo timestamp={sec.metadata.creationTimestamp} /></td>
                    <td>
                      <ActionButton icon="✕" label="" onClick={() => setDeleteTarget(sec)} danger ghost />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// ── Events ──────────────────────────────────────────────────────

function EventsTab() {
  const { data, loading } = useApi<K8sList<K8sEvent>>('/api/events');

  if (loading) return <div className="loading">Loading events…</div>;

  const events = (data?.items || []).filter(
    (e) => e.metadata.namespace !== 'kube-system' && e.metadata.namespace !== 'local-path-storage'
  );

  // Sort by last timestamp descending
  events.sort((a, b) => {
    const ta = a.lastTimestamp || a.metadata.creationTimestamp || '';
    const tb = b.lastTimestamp || b.metadata.creationTimestamp || '';
    return tb.localeCompare(ta);
  });

  if (events.length === 0) {
    return <EmptyState icon="◈" message="No events in workload namespaces." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th style={{ width: 72 }}>Type</th>
            <th>Reason</th>
            <th>Object</th>
            <th>Namespace</th>
            <th>Message</th>
            <th style={{ width: 60 }}>Count</th>
            <th>Last Seen</th>
          </tr>
        </thead>
        <tbody>
          {events.map((ev, i) => (
            <tr key={`${ev.metadata.namespace}/${ev.metadata.name}-${i}`}>
              <td>
                <span className={`event-badge event-${(ev.type || 'Normal').toLowerCase()}`}>
                  {ev.type || 'Normal'}
                </span>
              </td>
              <td className="mono">{ev.reason}</td>
              <td className="mono" style={{ fontSize: '0.8em' }}>
                {ev.involvedObject?.kind}/{ev.involvedObject?.name}
              </td>
              <td><span className="tag">{ev.metadata.namespace}</span></td>
              <td style={{ maxWidth: 400, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {ev.message}
              </td>
              <td style={{ textAlign: 'center' }}>{ev.count || 1}</td>
              <td><TimeAgo timestamp={ev.lastTimestamp || ev.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── RBAC ────────────────────────────────────────────────────────

type RBACSubTab = 'serviceaccounts' | 'roles' | 'rolebindings' | 'clusterroles' | 'clusterrolebindings';

const RBAC_TABS: { key: RBACSubTab; label: string }[] = [
  { key: 'serviceaccounts', label: 'Service Accounts' },
  { key: 'roles', label: 'Roles' },
  { key: 'rolebindings', label: 'Role Bindings' },
  { key: 'clusterroles', label: 'Cluster Roles' },
  { key: 'clusterrolebindings', label: 'Cluster Role Bindings' },
];

function RBACTab() {
  const [subTab, setSubTab] = useState<RBACSubTab>('serviceaccounts');

  return (
    <>
      <div className="tab-bar" style={{ marginTop: 12 }}>
        {RBAC_TABS.map((t) => (
          <button
            key={t.key}
            className={`tab-btn${subTab === t.key ? ' tab-active' : ''}`}
            onClick={() => setSubTab(t.key)}
            style={{ fontSize: '0.85em' }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {subTab === 'serviceaccounts' && <ServiceAccountsSubTab />}
      {subTab === 'roles' && <RolesSubTab />}
      {subTab === 'rolebindings' && <RoleBindingsSubTab />}
      {subTab === 'clusterroles' && <ClusterRolesSubTab />}
      {subTab === 'clusterrolebindings' && <ClusterRoleBindingsSubTab />}
    </>
  );
}

function ServiceAccountsSubTab() {
  const { data, loading } = useApi<K8sList<K8sServiceAccount>>('/api/serviceaccounts');

  if (loading) return <div className="loading">Loading service accounts…</div>;

  const items = (data?.items || []).filter(
    (sa) =>
      sa.metadata.namespace !== 'kube-system' &&
      sa.metadata.namespace !== 'local-path-storage'
  );

  if (items.length === 0) {
    return <EmptyState icon="⊞" message="No service accounts found in workload namespaces." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Secrets</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((sa) => (
            <tr key={`${sa.metadata.namespace}/${sa.metadata.name}`}>
              <td className="mono">{sa.metadata.name}</td>
              <td><span className="tag">{sa.metadata.namespace}</span></td>
              <td className="mono">{sa.secrets?.length ?? 0}</td>
              <td><TimeAgo timestamp={sa.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RolesSubTab() {
  const { data, loading } = useApi<K8sList<K8sRole>>('/api/roles');

  if (loading) return <div className="loading">Loading roles…</div>;

  const items = (data?.items || []).filter(
    (r) =>
      r.metadata.namespace !== 'kube-system' &&
      r.metadata.namespace !== 'local-path-storage'
  );

  if (items.length === 0) {
    return <EmptyState icon="⊘" message="No roles found in workload namespaces." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Rules</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((role) => (
            <tr key={`${role.metadata.namespace}/${role.metadata.name}`}>
              <td className="mono">{role.metadata.name}</td>
              <td><span className="tag">{role.metadata.namespace}</span></td>
              <td>{role.rules?.length ?? 0}</td>
              <td><TimeAgo timestamp={role.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RoleBindingsSubTab() {
  const { data, loading } = useApi<K8sList<K8sRoleBinding>>('/api/rolebindings');

  if (loading) return <div className="loading">Loading role bindings…</div>;

  const items = (data?.items || []).filter(
    (rb) =>
      rb.metadata.namespace !== 'kube-system' &&
      rb.metadata.namespace !== 'local-path-storage'
  );

  if (items.length === 0) {
    return <EmptyState icon="⊘" message="No role bindings found in workload namespaces." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Namespace</th>
            <th>Role</th>
            <th>Subjects</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((rb) => (
            <tr key={`${rb.metadata.namespace}/${rb.metadata.name}`}>
              <td className="mono">{rb.metadata.name}</td>
              <td><span className="tag">{rb.metadata.namespace}</span></td>
              <td className="mono">{rb.roleRef.kind}/{rb.roleRef.name}</td>
              <td>
                {rb.subjects?.map((s, i) => (
                  <span key={i} className="tag" style={{ marginRight: 4 }}>
                    {s.kind}:{s.name}
                  </span>
                )) || <span className="text-dim">—</span>}
              </td>
              <td><TimeAgo timestamp={rb.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ClusterRolesSubTab() {
  const { data, loading } = useApi<K8sList<K8sClusterRole>>('/api/clusterroles');

  if (loading) return <div className="loading">Loading cluster roles…</div>;

  const items = (data?.items || []).filter(
    (cr) => !cr.metadata.name.startsWith('system:')
  );

  if (items.length === 0) {
    return <EmptyState icon="⊘" message="No custom cluster roles found." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Rules</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((cr) => (
            <tr key={cr.metadata.name}>
              <td className="mono">{cr.metadata.name}</td>
              <td>{cr.rules?.length ?? 0}</td>
              <td><TimeAgo timestamp={cr.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ClusterRoleBindingsSubTab() {
  const { data, loading } = useApi<K8sList<K8sClusterRoleBinding>>('/api/clusterrolebindings');

  if (loading) return <div className="loading">Loading cluster role bindings…</div>;

  const items = (data?.items || []).filter(
    (crb) => !crb.metadata.name.startsWith('system:')
  );

  if (items.length === 0) {
    return <EmptyState icon="⊘" message="No custom cluster role bindings found." />;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Role</th>
            <th>Subjects</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((crb) => (
            <tr key={crb.metadata.name}>
              <td className="mono">{crb.metadata.name}</td>
              <td className="mono">{crb.roleRef.kind}/{crb.roleRef.name}</td>
              <td>
                {crb.subjects?.map((s, i) => (
                  <span key={i} className="tag" style={{ marginRight: 4 }}>
                    {s.kind}:{s.name}
                  </span>
                )) || <span className="text-dim">—</span>}
              </td>
              <td><TimeAgo timestamp={crb.metadata.creationTimestamp} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

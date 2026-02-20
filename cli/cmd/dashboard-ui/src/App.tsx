import { useState } from 'react';
import type { ReactNode } from 'react';
import { OverviewPage } from './pages/OverviewPage';
import { DSEPage } from './pages/DSEPage';
import { RunnersPage } from './pages/RunnersPage';
import { DeploymentsPage } from './pages/DeploymentsPage';
import { PodsPage } from './pages/PodsPage';
import { ServicesPage } from './pages/ServicesPage';
import { IngressesPage } from './pages/IngressesPage';
import { SecretsPage } from './pages/SecretsPage';
import { EventsPage } from './pages/EventsPage';

type Page =
  | 'overview'
  | 'dses'
  | 'runners'
  | 'deployments'
  | 'pods'
  | 'services'
  | 'ingresses'
  | 'secrets'
  | 'events';

const NAV: { page: Page; icon: string; label: string }[] = [
  { page: 'overview', icon: '📊', label: 'Overview' },
  { page: 'dses', icon: '🚀', label: 'Environments' },
  { page: 'runners', icon: '🏃', label: 'Runners' },
  { page: 'deployments', icon: '📦', label: 'Deployments' },
  { page: 'pods', icon: '🫛', label: 'Pods' },
  { page: 'services', icon: '🔌', label: 'Services' },
  { page: 'ingresses', icon: '🌐', label: 'Ingresses' },
  { page: 'secrets', icon: '🔑', label: 'Secrets' },
  { page: 'events', icon: '📋', label: 'Events' },
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
};

function App() {
  const [activePage, setActivePage] = useState<Page>('overview');
  const ActiveComponent = PAGES[activePage];

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <span className="brand-icon">🔥</span>
          <span className="brand-text">kindling</span>
        </div>
        <nav className="sidebar-nav">
          {NAV.map((item) => (
            <button
              key={item.page}
              className={`nav-item ${activePage === item.page ? 'active' : ''}`}
              onClick={() => setActivePage(item.page)}
            >
              <span className="nav-icon">{item.icon}</span>
              <span className="nav-label">{item.label}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="version">kindling dashboard</span>
        </div>
      </aside>
      <main className="main-content">
        <ActiveComponent />
      </main>
    </div>
  );
}

export default App;

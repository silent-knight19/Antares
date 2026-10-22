import { NavLink, Outlet } from 'react-router-dom';
import { StatusBar } from './StatusBar';

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center justify-between rounded-md px-3 py-2 text-sm transition-colors duration-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent ${
    isActive ? 'bg-accent-soft text-text' : 'text-text-muted hover:bg-surface-2 hover:text-text'
  }`;

function NavItem({ to, label }: { to: string; label: string }) {
  return (
    <NavLink to={to} className={linkClass}>
      <span>{label}</span>
    </NavLink>
  );
}

function LaterItem({ label, milestone }: { label: string; milestone: string }) {
  return (
    <div className="flex items-center justify-between rounded-md px-3 py-2 text-sm text-text-faint" aria-disabled="true">
      <span>{label}</span>
      <span className="font-mono text-xs">{milestone}</span>
    </div>
  );
}

export function Layout() {
  return (
    <div className="flex h-full min-h-screen flex-col bg-bg">
      <StatusBar />
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <aside className="border-b border-border bg-surface/40 md:flex md:w-[220px] md:shrink-0 md:flex-col md:border-b-0 md:border-r">
          <div className="hidden px-4 pb-2 pt-4 text-xs uppercase tracking-wider text-text-faint md:block">Showcase</div>
          <nav className="flex gap-1 overflow-x-auto p-2 md:flex-1 md:flex-col md:space-y-1 md:overflow-visible">
            <NavItem to="/overview" label="Overview" />
            <NavItem to="/metrics" label="Metrics" />
            <NavItem to="/benchmarks" label="Benchmarks" />
            <NavItem to="/durability" label="Durability Lab" />
            <div className="hidden md:my-3 md:block md:border-t md:border-border" />
            <div className="hidden px-2 pb-1 text-[11px] uppercase tracking-wider text-text-faint md:block">Console</div>
            <NavItem to="/playground" label="Playground" />
            <NavItem to="/keyspace" label="Keyspace" />
            <div className="hidden md:my-3 md:block md:border-t md:border-border" />
            <LaterItem label="Replication" milestone="M3" />
          </nav>
          <div className="hidden border-t border-border px-4 py-3 text-xs text-text-faint md:block">M2 · showcase</div>
        </aside>
        <main className="min-w-0 flex-1 overflow-auto">
          <div className="mx-auto max-w-[1440px] p-4 md:p-6">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}

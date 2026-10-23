import { NavLink, Outlet } from 'react-router-dom';
import { StatusBar } from './StatusBar';

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center justify-between rounded-md px-3 py-2 text-sm transition-colors duration-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent ${
    isActive ? 'bg-accent-soft text-text' : 'text-text-muted hover:bg-surface-2 hover:text-text'
  }`;

function NavItem({ to, label, id }: { to: string; label: string; id?: string }) {
  return (
    <NavLink id={id} to={to} className={linkClass}>
      <span>{label}</span>
    </NavLink>
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
            <NavItem id="nav-overview" to="/overview" label="Overview" />
            <NavItem id="nav-metrics" to="/metrics" label="Metrics" />
            <NavItem id="nav-benchmarks" to="/benchmarks" label="Benchmarks" />
            <NavItem id="nav-durability" to="/durability" label="Durability Lab" />
            <NavItem id="nav-replication" to="/replication" label="Replication Lab" />
            <NavItem id="nav-interview" to="/interview" label="Interview Mode" />
            <div className="hidden md:my-3 md:block md:border-t md:border-border" />
            <div className="hidden px-2 pb-1 text-[11px] uppercase tracking-wider text-text-faint md:block">Console</div>
            <NavItem id="nav-playground" to="/playground" label="Playground" />
            <NavItem id="nav-keyspace" to="/keyspace" label="Keyspace" />
          </nav>
          <div className="hidden border-t border-border px-4 py-3 text-xs text-text-faint md:block">M3 · interview ready</div>
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

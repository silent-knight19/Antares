import { Link } from 'react-router-dom';
import { EmptyState, Panel } from '../components/ui';

export function NotFound() {
  return (
    <Panel title="Not found">
      <EmptyState title="That route does not exist." hint="Try /playground or /keyspace." />
      <div className="text-center">
        <Link className="text-sm text-info hover:text-accent" to="/playground">
          Go to Playground
        </Link>
      </div>
    </Panel>
  );
}

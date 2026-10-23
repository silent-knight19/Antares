import { Navigate, Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { Benchmarks } from './pages/Benchmarks';
import { DurabilityLab } from './pages/DurabilityLab';
import { Keyspace } from './pages/Keyspace';
import { Metrics } from './pages/Metrics';
import { NotFound } from './pages/NotFound';
import { Overview } from './pages/Overview';
import { Playground } from './pages/Playground';
import { ReplicationLab } from './pages/ReplicationLab';
import { InterviewMode } from './pages/InterviewMode';

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={<Overview />} />
        <Route path="metrics" element={<Metrics />} />
        <Route path="benchmarks" element={<Benchmarks />} />
        <Route path="durability" element={<DurabilityLab />} />
        <Route path="replication" element={<ReplicationLab />} />
        <Route path="interview" element={<InterviewMode />} />
        <Route path="playground" element={<Playground />} />
        <Route path="keyspace" element={<Keyspace />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

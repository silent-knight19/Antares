import { Navigate, Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { Keyspace } from './pages/Keyspace';
import { NotFound } from './pages/NotFound';
import { Playground } from './pages/Playground';

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Navigate to="/playground" replace />} />
        <Route path="playground" element={<Playground />} />
        <Route path="keyspace" element={<Keyspace />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

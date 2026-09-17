import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './Layout';
import Dashboard from './pages/Dashboard';
import Mailbox from './pages/Mailbox';
import Templates from './pages/Templates';
import Campaigns from './pages/Campaigns';

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="mailbox" element={<Mailbox />} />
          <Route path="templates" element={<Templates />} />
          <Route path="campaigns" element={<Campaigns />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;

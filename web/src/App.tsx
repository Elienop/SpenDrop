import { useState, useEffect } from 'react';
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './components/Sidebar';
import { ProtectedRoute } from './components/ProtectedRoute';
import { Login } from './pages/Login';
import { Register } from './pages/Register';
import { Dashboard } from './pages/Dashboard';
import { Transactions } from './pages/Transactions';
import { Categories } from './pages/Categories';
import { Settings } from './pages/Settings';
import { Reports } from './pages/Reports';
import layoutStyles from './styles/AppLayout.module.css';

function AppLayout() {
  const [sidebarExpanded, setSidebarExpanded] = useState(() => {
    return localStorage.getItem('spendrop-sidebar') === 'true';
  });

  // Listen for sidebar toggle events (custom event dispatched by Sidebar component)
  useEffect(() => {
    const handler = () => {
      setSidebarExpanded(localStorage.getItem('spendrop-sidebar') === 'true');
    };
    window.addEventListener('sidebar-toggle', handler);
    return () => {
      window.removeEventListener('sidebar-toggle', handler);
    };
  }, []);

  return (
    <div className={layoutStyles.layout}>
      <Sidebar />
      <main className={`${layoutStyles.main}${sidebarExpanded ? ` ${layoutStyles.mainExpanded}` : ''}`}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <AppLayout />
          </ProtectedRoute>
        }
      />
    </Routes>
  );
}

export default App;

import { Routes, Route } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { ProtectedRoute } from './components/ProtectedRoute';
import { Login } from './pages/Login';
import { Register } from './pages/Register';
import { QuickAdd } from './pages/QuickAdd';
import { useOfflineSync } from './hooks/useOfflineSync';

function App() {
  // Replay any offline-queued expenses app-wide (on launch + reconnect), so a
  // capture made offline on /quick syncs even after navigating into the app.
  useOfflineSync();

  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        path="/quick"
        element={
          <ProtectedRoute>
            <QuickAdd />
          </ProtectedRoute>
        }
      />
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <AppShell />
          </ProtectedRoute>
        }
      />
    </Routes>
  );
}

export default App;

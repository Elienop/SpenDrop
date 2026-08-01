import { Routes, Route } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { ProtectedRoute } from './components/ProtectedRoute';
import { Login } from './pages/Login';
import { Register } from './pages/Register';
import { QuickAdd } from './pages/QuickAdd';
import { useOfflineSync } from './hooks/useOfflineSync';
import { useLiveUpdates } from './hooks/useLiveUpdates';

function App() {
  // Replay any offline-queued expenses app-wide (on launch + reconnect), so a
  // capture made offline on /quick syncs even after navigating into the app.
  useOfflineSync();

  // Subscribe (once) to server-sent invalidation hints so this device reflects
  // changes made on other household devices within ~1s, no manual refresh.
  // Auth-gated and StrictMode-safe inside the hook (one EventSource, closed on
  // unmount / logout via its effect cleanup).
  useLiveUpdates();

  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      {/* The installed PWA's start_url. `allowUnverified` is what makes
          offline capture reachable with no signal: the app cannot confirm the
          session, so it falls back to the identity this device last had
          confirmed rather than bouncing to /login — the one screen that
          cannot work offline. Capture writes locally only; the queue is not
          replayed until the server confirms who is signed in. */}
      <Route
        path="/quick"
        element={
          <ProtectedRoute allowUnverified>
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

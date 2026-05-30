import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { AuthProvider } from './hooks/useAuth';
import { ThemeProvider } from './components/theme-provider';
import App from './App';
import './globals.css';

// One-shot reload when a NEW service worker takes control of this tab. The SW
// calls skipWaiting()+clients.claim() on activate, which fires
// `controllerchange`. We reload so an open tab adopts the new precached shell
// (and, later, push-capable SW logic) instead of running stale assets until the
// next manual navigation. Guards:
//  - `refreshing` makes it single-shot (clients.claim() can fire the event more
//    than once during activation; without the latch the reload would loop).
//  - We only attach the listener when this page already HAS a controller, so the
//    first-ever SW claiming a previously-uncontrolled tab (initial install) does
//    not trigger a spurious reload.
if ('serviceWorker' in navigator && navigator.serviceWorker.controller) {
  let refreshing = false;
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (refreshing) return;
    refreshing = true;
    window.location.reload();
  });
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <ThemeProvider defaultTheme="dark" storageKey="spendrop-theme">
        <AuthProvider>
          <App />
        </AuthProvider>
      </ThemeProvider>
    </BrowserRouter>
  </StrictMode>,
);

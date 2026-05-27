// Pre-paint theme initialization. Loaded as an EXTERNAL same-origin script
// (not inline) so it executes under the server's strict CSP
// (`script-src 'self'`, no 'unsafe-inline'). It runs synchronously in
// <head>, before React mounts, to set the light/dark class on <html> and
// avoid a flash of the wrong theme. Keep in sync with ThemeProvider's
// storageKey ('spendrop-theme').
(function () {
  var stored = localStorage.getItem('spendrop-theme');
  var theme;
  if (stored === 'light' || stored === 'dark') {
    theme = stored;
  } else if (stored === 'system' || !stored) {
    theme = window.matchMedia('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'light';
  }
  document.documentElement.classList.remove('light', 'dark');
  document.documentElement.classList.add(theme || 'dark');
})();

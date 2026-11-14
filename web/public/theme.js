// Applies a saved light or dark choice before the first paint. Without a
// choice the page follows the system setting through CSS color-scheme.
try {
  var theme = localStorage.getItem('cph-theme')
  if (theme === 'light' || theme === 'dark')
    document.documentElement.dataset.theme = theme
} catch (e) {}

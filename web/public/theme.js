// Applies a saved light or dark choice before the first paint. Without a
// choice the page follows the system setting through CSS color-scheme.
try {
  var theme = localStorage.getItem('cph-theme')
  if (theme === 'light' || theme === 'dark') {
    document.documentElement.dataset.theme = theme
    // A forced theme also colors the browser chrome (Safari's toolbar).
    var color = theme === 'dark' ? '#111113' : '#f5f5f7'
    var metas = document.querySelectorAll('meta[name="theme-color"]')
    for (var i = 0; i < metas.length; i++) metas[i].setAttribute('content', color)
  }
} catch (e) {}

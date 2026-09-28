'use strict';
// Applies the saved theme before the page renders (no flash). "auto" follows
// the operating system; see the theme switch in app.js.
(() => {
  try {
    const t = localStorage.getItem('filedeck-theme');
    if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t;
  } catch (e) { /* storage unavailable: follow the system */ }
})();

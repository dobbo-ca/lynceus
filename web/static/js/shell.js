// Shell interactivity (ly-ae6.2): theme-toggle glyph + poll ticker. Depends on
// window.Lynceus from the head bootstrap / theme.js. Self-contained, no external
// references (privacy backbone).
//
// COSMETIC-ONLY: the "UPDATED Ns AGO" ticker below is a visual placeholder — it
// counts 0..PollSecs on a local 1s interval and is NOT wired to any real data
// refresh (this chrome-only bead renders no live data). It becomes real when the
// data screens (ly-ae6.4 fleet dashboard onward) add HTMX polling on their body;
// at that point the ticker should reset on each successful poll (e.g. an
// htmx:afterSwap listener) rather than free-run. See the plan's backend-gaps note.
(function () {
  var doc = document.documentElement;
  function glyph() { return doc.dataset.theme === 'light' ? '☀' : '☾'; } // ☀ / ☾

  var toggle = document.getElementById('theme-toggle');
  if (toggle) {
    toggle.textContent = glyph();
    toggle.addEventListener('click', function () {
      if (window.Lynceus && window.Lynceus.cycleTheme) window.Lynceus.cycleTheme();
      toggle.textContent = glyph();
    });
  }

  var ago = document.querySelector('[data-updated-ago]');
  var host = document.querySelector('[data-poll-secs]');
  if (ago && host) {
    var span = parseInt(host.getAttribute('data-poll-secs'), 10) || 3;
    var n = 0;
    setInterval(function () {
      n = (n + 1) % (span + 1);
      ago.textContent = 'UPDATED ' + n + 'S AGO';
    }, 1000);
  }
})();

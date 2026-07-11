// Accent-picker enhancement for Settings › Appearance. Marks the active swatch
// from the persisted preference and, on click, calls the F1 setter
// window.Lynceus.setAccent (theme.js), which persists to localStorage and
// re-applies the per-theme accent variant. No external references.
(function () {
  var L = window.Lynceus || {};
  var PRESETS = ['#2dd4bf', '#22d3ee', '#818cf8'];
  function current() {
    try {
      var v = localStorage.getItem('lynceus.accent');
      return PRESETS.indexOf(v) >= 0 ? v : '#2dd4bf';
    } catch (e) {
      return '#2dd4bf';
    }
  }
  function init() {
    var cur = current();
    var btns = document.querySelectorAll('[data-accent]');
    function mark(active) {
      btns.forEach(function (x) {
        var on = x === active;
        x.classList.toggle('is-active', on);
        x.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    }
    var initial = null;
    btns.forEach(function (b) {
      if (b.getAttribute('data-accent') === cur) initial = b;
      b.addEventListener('click', function () {
        if (L.setAccent) L.setAccent(b.getAttribute('data-accent'));
        mark(b);
      });
    });
    if (initial) mark(initial);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();

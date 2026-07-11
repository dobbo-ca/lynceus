// Interactive theme + accent API. The synchronous no-flash bootstrap in the
// document <head> already created window.Lynceus (ACCENTS/resolveTheme/
// applyAccent) and applied the persisted preference; this deferred file adds
// the setters that the top-bar toggle (ly-ae6.2) and Settings picker
// (ly-ae6.14) call.
(function () {
  var L = (window.Lynceus = window.Lynceus || {});
  function persist(k, v) {
    try {
      localStorage.setItem(k, v);
    } catch (e) {}
  }

  // setTheme('dark'|'light'|'system'): resolve, apply, persist, re-apply accent
  // (the accent variant tracks the resolved theme).
  L.setTheme = function (pref) {
    L.pref = pref;
    persist('lynceus.theme', pref);
    document.documentElement.dataset.theme = L.resolveTheme(pref);
    if (L.applyAccent) L.applyAccent();
  };

  // cycleTheme(): dark -> light -> system -> dark. Returns the new preference.
  L.cycleTheme = function () {
    var order = ['dark', 'light', 'system'];
    var next = order[(order.indexOf(L.pref) + 1) % order.length];
    L.setTheme(next);
    return next;
  };

  // setAccent('#hex'): persist + apply. No-op for unknown presets.
  L.setAccent = function (hex) {
    if (!L.ACCENTS || !L.ACCENTS[hex]) return;
    L.accent = hex;
    persist('lynceus.accent', hex);
    if (L.applyAccent) L.applyAccent();
  };

  // Keep a 'system' preference live if the OS theme flips while the app is open.
  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', function () {
      if (L.pref === 'system') {
        document.documentElement.dataset.theme = L.resolveTheme('system');
        if (L.applyAccent) L.applyAccent();
      }
    });
  }
})();

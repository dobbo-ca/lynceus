// Copy-to-clipboard for onboarding wizard + provider guides.
// Event-delegated so it also covers HTMX-swapped content. No external hosts.
(function () {
  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-copy]');
    if (!btn) return;
    var src = document.getElementById(btn.getAttribute('data-copy'));
    if (!src) return;
    var text = src.textContent;
    var done = function () {
      var prev = btn.textContent;
      btn.textContent = 'COPIED';
      setTimeout(function () { btn.textContent = prev; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () {});
    } else {
      var ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); done(); } catch (err) {}
      document.body.removeChild(ta);
    }
  });
})();

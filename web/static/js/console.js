// SQL Console progressive enhancement. Self-hosted (privacy backbone).
document.addEventListener('keydown', function (e) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    var run = document.querySelector('[data-console-run]');
    if (run) { e.preventDefault(); run.click(); }
  }
});
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-console-copy]');
  if (!btn) return;
  var src = document.getElementById('console-copy-src');
  var note = document.getElementById('console-copy-note');
  var setNote = function (t) { if (note) note.textContent = t; };
  if (!src || src.getAttribute('data-too-large') === '1') {
    setNote('⚠ RESULT TOO LARGE FOR CLIPBOARD — USE CSV');
    return;
  }
  navigator.clipboard.writeText(src.textContent).then(
    function () { setNote('✓ COPIED'); },
    function () { setNote('⚠ CLIPBOARD BLOCKED — USE CSV'); }
  );
});

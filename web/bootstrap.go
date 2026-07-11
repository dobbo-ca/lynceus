package web

// themeBootstrapJS is the synchronous no-flash theme bootstrap. It is rendered
// inline in the document <head> (before the stylesheet) via templ.Raw so it
// runs before first paint: it creates the window.Lynceus namespace, resolves
// the persisted theme preference (dark | light | system), sets
// documentElement.dataset.theme, and applies the persisted accent. It must be
// inline (an external script would load async and cause a flash) and
// self-contained (no external references). theme.js later extends the same
// namespace with the interactive setters.
const themeBootstrapJS = `
(function(){
  var L = window.Lynceus = window.Lynceus || {};
  // per-theme accent variants: [acc, acc2, accdim, accbg]; bright on dark, deeper on light.
  L.ACCENTS = {
    '#2dd4bf':{dark:['#2dd4bf','#5eead4','rgba(45,212,191,.14)','#131c29'],light:['#0d9488','#0f766e','rgba(13,148,136,.12)','#e3f4f1']},
    '#22d3ee':{dark:['#22d3ee','#67e8f9','rgba(34,211,238,.14)','#131c29'],light:['#0891b2','#0e7490','rgba(8,145,178,.12)','#e0f4f9']},
    '#818cf8':{dark:['#818cf8','#a5b4fc','rgba(129,140,248,.14)','#131c29'],light:['#4f46e5','#4338ca','rgba(79,70,229,.12)','#e9eafc']}
  };
  L.resolveTheme = function(pref){
    if(pref==='system'||!pref){ return (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark'; }
    return pref==='light' ? 'light' : 'dark';
  };
  L.applyAccent = function(){
    var a = L.accent || '#2dd4bf';
    var t = document.documentElement.dataset.theme || 'dark';
    var v = (L.ACCENTS[a] || L.ACCENTS['#2dd4bf'])[t==='light'?'light':'dark'];
    var st = document.documentElement.style;
    st.setProperty('--acc',v[0]); st.setProperty('--acc2',v[1]);
    st.setProperty('--accdim',v[2]); st.setProperty('--accbg',v[3]); st.setProperty('--ok',v[0]);
    document.documentElement.dataset.accent = a;
  };
  try {
    var acc = localStorage.getItem('lynceus.accent');
    if(['#2dd4bf','#22d3ee','#818cf8'].indexOf(acc) >= 0) L.accent = acc;
    L.pref = localStorage.getItem('lynceus.theme') || 'system';
    document.documentElement.dataset.theme = L.resolveTheme(L.pref);
    L.applyAccent();
  } catch(e){ /* localStorage blocked -> keep SSR data-theme="dark" */ }
})();
`

// themeBootstrapTag wraps themeBootstrapJS in a <script> element for inline
// rendering via templ.Raw. templ treats the contents of a *literal* <script>
// element as opaque text and will not evaluate a templ expression inside one,
// so the entire tag is emitted as a single raw node instead.
func themeBootstrapTag() string {
	return "<script>" + themeBootstrapJS + "</script>"
}

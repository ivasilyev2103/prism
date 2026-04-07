/* Prism Documentation — Mermaid.js Diagram Integration */

document.addEventListener('DOMContentLoaded', function () {
  // Detect language from URL path.
  var lang = 'ru';
  if (location.pathname.indexOf('/en/') !== -1) lang = 'en';
  else if (location.pathname.indexOf('/ru/') !== -1) lang = 'ru';

  // Base path for module links (relative to current page).
  var base = '';
  if (location.pathname.indexOf('/index.html') !== -1 || location.pathname.endsWith('/html/')) {
    base = lang + '/';
  } else {
    base = '';
  }

  // Initialize Mermaid with dark theme.
  if (typeof mermaid !== 'undefined') {
    mermaid.initialize({
      startOnLoad: true,
      theme: 'dark',
      themeVariables: {
        primaryColor: '#1f6feb',
        primaryTextColor: '#e6edf3',
        primaryBorderColor: '#30363d',
        lineColor: '#58a6ff',
        secondaryColor: '#161b22',
        tertiaryColor: '#0d1117',
        fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif',
        fontSize: '14px',
        nodeTextColor: '#e6edf3'
      },
      securityLevel: 'loose',
      flowchart: {
        htmlLabels: true,
        curve: 'basis',
        nodeSpacing: 50,
        rankSpacing: 60
      }
    });

    // After render, bind click handlers to nodes.
    mermaid.run().then(function () {
      bindNodeClicks(base);
    }).catch(function () {
      // Fallback for older mermaid versions.
      setTimeout(function () { bindNodeClicks(base); }, 500);
    });
  }

  // Highlight active sidebar link.
  var links = document.querySelectorAll('.sidebar nav a');
  var current = location.pathname.split('/').pop().replace('.html', '');
  links.forEach(function (a) {
    var href = a.getAttribute('href') || '';
    var target = href.split('/').pop().replace('.html', '');
    if (target === current) {
      a.classList.add('active');
    }
  });
});

/**
 * Bind click handlers to Mermaid diagram nodes.
 * Node IDs map to module pages.
 */
function bindNodeClicks(base) {
  var nodeMap = {
    'ingress':  base + 'ingress.html',
    'privacy':  base + 'privacy.html',
    'policy':   base + 'policy.html',
    'vault':    base + 'vault.html',
    'provider': base + 'provider.html',
    'cost':     base + 'cost.html',
    'audit':    base + 'audit.html',
    'cache':    base + 'cache.html'
  };

  var nodes = document.querySelectorAll('.mermaid .node');
  nodes.forEach(function (node) {
    var id = node.id || '';
    // Mermaid generates IDs like "flowchart-ingress-0".
    var key = '';
    Object.keys(nodeMap).forEach(function (k) {
      if (id.indexOf(k) !== -1) key = k;
    });

    if (key) {
      node.style.cursor = 'pointer';
      node.addEventListener('click', function () {
        window.location.href = nodeMap[key];
      });
    }
  });
}

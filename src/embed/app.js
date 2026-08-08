/* LeanKG ui-lite — vis-network graph + node detail panel */
(function () {
  'use strict';

  var SNIPPET_PAD = 3;
  var DEFAULT_LIMIT = 500;

  var nodesDS = null;
  var edgesDS = null;
  var network = null;
  var selectedId = null;

  function $(id) {
    return document.getElementById(id);
  }

  function setStatus(msg) {
    $('status').textContent = msg || '';
  }

  function qsPath() {
    var p = new URLSearchParams(window.location.search).get('path');
    return (p && p.trim()) || 'src';
  }

  function setPathInUrl(path) {
    var u = new URL(window.location.href);
    u.searchParams.set('path', path);
    history.replaceState(null, '', u.toString());
  }

  function unwrap(json, fallback) {
    if (!json) throw new Error(fallback || 'empty response');
    if (json.success === false) throw new Error(json.error || fallback || 'request failed');
    if (json.data !== undefined) return json.data;
    return json;
  }

  async function fetchJson(path) {
    var res = await fetch(path, { headers: { Accept: 'application/json' } });
    var body = null;
    try {
      body = await res.json();
    } catch (_) {
      body = null;
    }
    if (!res.ok) {
      var err = (body && (body.error || body.message)) || ('HTTP ' + res.status);
      throw new Error(err);
    }
    return unwrap(body, 'Failed ' + path);
  }

  /** Mirror of Rust clip_snippet_range (1-based inclusive). */
  function clipSnippetRange(lineStart, lineEnd, pad, totalLines) {
    if (!totalLines) return [0, 0];
    var start = Math.max(1, (lineStart || 1) - (pad || 0));
    var end = Math.min(totalLines, (lineEnd || lineStart || 1) + (pad || 0));
    if (end < start) end = start;
    return [start, end];
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function nodeFromApi(n) {
    var props = n.properties || {};
    var id = n.id || props.qualified_name || props.name;
    var et = String(
      props.element_type || props.elementType || n.label || 'unknown',
    ).toLowerCase();
    var name = props.name || String(id).split('::').pop();
    var file = props.file_path || props.filePath || '';
    return {
      id: id,
      label: name,
      group: et,
      title: id + '\n' + et + (file ? '\n' + file : ''),
      file: file,
      element_type: et,
    };
  }

  function edgeFromApi(e, idx) {
    var from = e.sourceId || e.source || e.from;
    var to = e.targetId || e.target || e.to;
    if (!from || !to) return null;
    return {
      id: e.id || from + '->' + to + '#' + idx,
      from: from,
      to: to,
      label: e.type || e.rel_type || '',
      arrows: 'to',
      title: (e.confidenceLabel || e.confidence_label || '') + '',
    };
  }

  function graphOptions() {
    return {
      autoResize: true,
      nodes: {
        shape: 'dot',
        size: 18,
        font: { color: '#e8eaed', size: 14, face: 'Helvetica', strokeWidth: 0 },
        borderWidth: 2,
        color: { background: '#5b9fd4', border: '#3d7eb0', highlight: { background: '#7eb6e0', border: '#5b9fd4' } },
      },
      edges: {
        width: 1.5,
        color: { color: '#8899aa', highlight: '#5b9fd4' },
        arrows: { to: { enabled: true, scaleFactor: 0.6 } },
        font: { color: '#9aa3b2', size: 11, align: 'middle', strokeWidth: 0 },
        smooth: { type: 'continuous' },
      },
      physics: {
        enabled: true,
        forceAtlas2Based: {
          gravitationalConstant: -45,
          centralGravity: 0.01,
          springLength: 140,
          springConstant: 0.08,
          damping: 0.4,
        },
        solver: 'forceAtlas2Based',
        stabilization: { enabled: true, iterations: 120, fit: true },
      },
      interaction: { hover: true, tooltipDelay: 180, zoomView: true, dragView: true },
      groups: {
        function: { color: { background: '#4ecdc4', border: '#45b7aa' } },
        struct: { color: { background: '#ff6b6b', border: '#ee5a5a' } },
        class: { color: { background: '#ffd93d', border: '#f0c929' } },
        module: { color: { background: '#6bcb77', border: '#5ab868' } },
        file: { color: { background: '#4d96ff', border: '#3d86ef' } },
        folder: { color: { background: '#8899aa', border: '#778899' } },
        directory: { color: { background: '#8899aa', border: '#778899' } },
        service: { color: { background: '#c9b1ff', border: '#b8a1ff' } },
        default: { color: { background: '#888', border: '#666' } },
      },
    };
  }

  function resizeNetwork() {
    if (!network) return;
    var box = $('mynetwork');
    if (!box) return;
    var w = box.clientWidth;
    var h = box.clientHeight;
    if (w < 10 || h < 10) return false;
    network.setSize(w + 'px', h + 'px');
    network.redraw();
    return true;
  }

  function fitNetwork() {
    if (!network) return;
    resizeNetwork();
    try {
      network.fit({ animation: false, padding: 40 });
    } catch (_) {
      /* ignore */
    }
  }

  function ensureNetwork() {
    if (network) {
      resizeNetwork();
      return;
    }
    var V = window.vis;
    if (!V || typeof V.DataSet !== 'function') {
      setStatus('vis-network failed to load');
      return;
    }
    var container = $('mynetwork');
    if (!container) return;
    nodesDS = new V.DataSet([]);
    edgesDS = new V.DataSet([]);
    network = new V.Network(container, { nodes: nodesDS, edges: edgesDS }, graphOptions());
    network.on('stabilizationIterationsDone', function () {
      network.setOptions({ physics: { enabled: false } });
      fitNetwork();
    });
    network.on('click', function (params) {
      if (params.nodes && params.nodes.length) {
        selectNode(params.nodes[0]);
      }
    });
    network.on('doubleClick', function (params) {
      if (params.nodes && params.nodes.length) {
        expandNode(params.nodes[0]);
      }
    });
    if (typeof ResizeObserver !== 'undefined') {
      var ro = new ResizeObserver(function () {
        fitNetwork();
      });
      ro.observe(container);
    }
    window.addEventListener('resize', function () {
      fitNetwork();
    });
    resizeNetwork();
  }

  function seedPositions(mappedNodes) {
    var n = mappedNodes.length;
    if (!n) return;
    // Give vis-network real coordinates so fit() is not a no-op on a blank canvas.
    var cols = Math.max(1, Math.ceil(Math.sqrt(n)));
    for (var i = 0; i < n; i++) {
      mappedNodes[i].x = (i % cols) * 160;
      mappedNodes[i].y = Math.floor(i / cols) * 120;
    }
  }

  function replaceGraph(payload) {
    ensureNetwork();
    if (!network || !nodesDS) {
      setStatus('Graph canvas not ready');
      return;
    }
    var rawNodes = payload.nodes || [];
    var rawEdges = payload.relationships || payload.edges || [];
    var mappedNodes = rawNodes.map(nodeFromApi).filter(function (n) { return n.id; });
    var mappedEdges = [];
    for (var i = 0; i < rawEdges.length; i++) {
      var e = edgeFromApi(rawEdges[i], i);
      if (e) mappedEdges.push(e);
    }
    seedPositions(mappedNodes);
    nodesDS.clear();
    edgesDS.clear();
    nodesDS.add(mappedNodes);
    edgesDS.add(mappedEdges);
    $('node-count').textContent = String(mappedNodes.length);
    $('edge-count').textContent = String(mappedEdges.length);
    network.setOptions({ physics: { enabled: true } });
    // Defer fit until layout has non-zero size (fixes empty canvas after first paint).
    requestAnimationFrame(function () {
      fitNetwork();
      setTimeout(fitNetwork, 50);
      setTimeout(fitNetwork, 250);
    });
  }

  function mergeGraph(payload) {
    ensureNetwork();
    var rawNodes = payload.nodes || [];
    var rawEdges = payload.relationships || payload.edges || [];
    var existing = {};
    nodesDS.getIds().forEach(function (id) { existing[id] = true; });
    var addNodes = [];
    rawNodes.map(nodeFromApi).forEach(function (n) {
      if (n.id && !existing[n.id]) {
        addNodes.push(n);
        existing[n.id] = true;
      }
    });
    var edgeIds = {};
    edgesDS.getIds().forEach(function (id) { edgeIds[id] = true; });
    var addEdges = [];
    for (var i = 0; i < rawEdges.length; i++) {
      var e = edgeFromApi(rawEdges[i], i);
      if (e && !edgeIds[e.id] && existing[e.from] && existing[e.to]) {
        addEdges.push(e);
        edgeIds[e.id] = true;
      }
    }
    if (addNodes.length) nodesDS.add(addNodes);
    if (addEdges.length) edgesDS.add(addEdges);
    $('node-count').textContent = String(nodesDS.length);
    $('edge-count').textContent = String(edgesDS.length);
  }

  async function loadPath(path) {
    setStatus('Loading ' + path + '…');
    setPathInUrl(path);
    $('path-input').value = path;
    try {
      var q = new URLSearchParams({
        path: path,
        all: 'true',
        limit: String(DEFAULT_LIMIT),
        offset: '0',
      });
      var data = await fetchJson('/api/graph/expand-service?' + q.toString());
      replaceGraph(data);
      var n = (data.nodes && data.nodes.length) || 0;
      setStatus(
        n
          ? 'Loaded ' + path + ' (' + n + ' nodes)'
          : 'Loaded ' + path + ' — no nodes (index empty or path miss)',
      );
    } catch (err) {
      setStatus(String(err.message || err));
    }
  }

  async function expandNode(nodeId) {
    setStatus('Expanding…');
    try {
      var node = nodesDS.get(nodeId);
      var et = (node && node.element_type) || '';
      var isDir = et === 'folder' || et === 'directory' || String(nodeId).indexOf('folder:') === 0;
      if (isDir) {
        var path = String(nodeId).replace(/^folder:/, '').replace(/\/$/, '') || (node && node.file) || nodeId;
        await loadPath(path);
        return;
      }
      var q = new URLSearchParams({
        node_id: nodeId,
        node_type: et || 'function',
        limit: '100',
      });
      var data = await fetchJson('/api/graph/expand-node?' + q.toString());
      mergeGraph(data);
      setStatus('Expanded ' + nodeId);
    } catch (err) {
      setStatus(String(err.message || err));
    }
  }

  function showDetailPanel(show) {
    var aside = $('detail');
    var layout = $('layout');
    if (show) {
      aside.hidden = false;
      layout.classList.remove('no-detail');
    } else {
      aside.hidden = true;
      layout.classList.add('no-detail');
      selectedId = null;
    }
  }

  function renderDetail(detail, snippetText, snippetRange) {
    var el = detail.element || {};
    var sig = (el.metadata && el.metadata.signature) || '';
    $('detail-name').textContent = el.name || el.qualified_name || '';
    $('detail-meta').innerHTML =
      '<span class="type-pill">' +
      escapeHtml(el.element_type || '') +
      '</span> · ' +
      escapeHtml(el.language || '');

    var html = '';
    html += '<div class="row"><span class="label">File</span> <code>' + escapeHtml(el.file_path || '') + '</code></div>';
    html +=
      '<div class="row"><span class="label">Lines</span> ' +
      escapeHtml(String(el.line_start || '?')) +
      '–' +
      escapeHtml(String(el.line_end || '?')) +
      '</div>';
    if (el.env) {
      html += '<div class="row"><span class="label">Env</span> ' + escapeHtml(el.env) + '</div>';
    }
    if (el.cluster_label) {
      html +=
        '<div class="row"><span class="label">Cluster</span> ' +
        escapeHtml(el.cluster_label) +
        '</div>';
    }
    html +=
      '<h3>Qualified name</h3><div class="mono">' +
      escapeHtml(el.qualified_name || '') +
      '</div>';
    if (sig) {
      html += '<h3>Signature</h3><div class="mono">' + escapeHtml(sig) + '</div>';
    }

    var ann = detail.annotation;
    if (ann && ann.description) {
      html += '<h3>Annotation</h3><div class="row">' + escapeHtml(ann.description) + '</div>';
      if (ann.user_story_id) {
        html += '<div class="row"><span class="label">US</span> ' + escapeHtml(ann.user_story_id) + '</div>';
      }
      if (ann.feature_id) {
        html += '<div class="row"><span class="label">FR</span> ' + escapeHtml(ann.feature_id) + '</div>';
      }
    }

    var neighbors = detail.neighbors || [];
    html += '<h3>Relations (' + neighbors.length + ')</h3>';
    if (!neighbors.length) {
      html += '<div class="row" style="color:var(--muted)">No neighbors in cap</div>';
    } else {
      neighbors.forEach(function (n) {
        html +=
          '<div class="neighbor"><span class="dir">' +
          escapeHtml(n.direction) +
          '</span> <strong>' +
          escapeHtml(n.rel_type) +
          '</strong> → <span class="mono">' +
          escapeHtml(n.peer) +
          '</span> <span style="color:var(--muted)">(' +
          escapeHtml(n.confidence_label || '') +
          ')</span></div>';
      });
    }

    if (snippetText) {
      html +=
        '<h3>Snippet' +
        (snippetRange ? ' L' + snippetRange[0] + '–' + snippetRange[1] : '') +
        '</h3><pre class="snippet">' +
        escapeHtml(snippetText) +
        '</pre>';
    }

    $('detail-body').innerHTML = html;
    showDetailPanel(true);
  }

  async function selectNode(nodeId) {
    selectedId = nodeId;
    setStatus('Loading detail…');
    try {
      var detail = await fetchJson('/api/element?qn=' + encodeURIComponent(nodeId));
      var el = detail.element || {};
      var snippetText = '';
      var range = null;
      if (el.file_path && el.line_start) {
        try {
          var file = await fetchJson('/api/file?path=' + encodeURIComponent(el.file_path));
          var content = typeof file === 'string' ? file : file.content || '';
          var lines = content.split('\n');
          range = clipSnippetRange(el.line_start, el.line_end || el.line_start, SNIPPET_PAD, lines.length);
          if (range[0] > 0) {
            snippetText = lines.slice(range[0] - 1, range[1]).join('\n');
          }
        } catch (_) {
          /* file may be unavailable in container mounts */
        }
      }
      renderDetail(detail, snippetText, range);
      setStatus('Selected ' + (el.name || nodeId));
    } catch (err) {
      $('detail-body').innerHTML = '<p class="err">' + escapeHtml(err.message || err) + '</p>';
      $('detail-name').textContent = nodeId;
      $('detail-meta').textContent = '';
      showDetailPanel(true);
      setStatus(String(err.message || err));
    }
  }

  async function runSearch() {
    var q = $('search-input').value.trim();
    if (!q) return;
    setStatus('Searching…');
    try {
      var data = await fetchJson('/api/search?q=' + encodeURIComponent(q) + '&limit=30');
      var results = Array.isArray(data) ? data : data.results || [];
      if (!results.length) {
        setStatus('No matches');
        return;
      }
      var first = results[0];
      var qn = first.qualified_name || first.id || first.name;
      if (qn && nodesDS && nodesDS.get(qn)) {
        network.focus(qn, { scale: 1.2, animation: true });
        selectNode(qn);
      } else if (qn) {
        selectNode(qn);
      }
      setStatus('Search: ' + results.length + ' hit(s)');
    } catch (err) {
      setStatus(String(err.message || err));
    }
  }

  function init() {
    $('layout').classList.add('no-detail');
    $('path-input').value = qsPath();
    $('path-form').addEventListener('submit', function (ev) {
      ev.preventDefault();
      loadPath(($('path-input').value || 'src').trim());
    });
    $('search-btn').addEventListener('click', runSearch);
    $('search-input').addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        runSearch();
      }
    });
    $('detail-close').addEventListener('click', function () {
      showDetailPanel(false);
    });
    ensureNetwork();
    loadPath(qsPath());

    // Expose for tests / console
    window.LeanKGLite = {
      clipSnippetRange: clipSnippetRange,
      loadPath: loadPath,
      selectNode: selectNode,
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

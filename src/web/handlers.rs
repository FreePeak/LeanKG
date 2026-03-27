use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    response::IntoResponse,
    Json,
};
use serde::{Deserialize, Serialize};

use crate::db;

use super::{ApiResponse, AppState};

#[derive(Deserialize, Serialize)]
pub struct QueryRequest {
    pub query: String,
}

#[derive(Serialize)]
pub struct QueryResponse {
    pub result: Vec<serde_json::Value>,
}

#[derive(Deserialize)]
pub struct SearchParams {
    pub q: Option<String>,
    pub element_type: Option<String>,
    pub file_path: Option<String>,
}

#[derive(Deserialize, Serialize)]
pub struct AnnotationRequest {
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
}

#[derive(Serialize, Clone)]
pub struct GraphData {
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
    pub filtered: Option<GraphFilterInfo>,
}

#[derive(Serialize, Clone)]
pub struct GraphFilterInfo {
    pub tests_filtered: usize,
    pub message: String,
}

#[derive(Serialize, Clone)]
pub struct GraphNode {
    pub id: String,
    pub label: String,
    pub element_type: String,
    pub file_path: String,
}

#[derive(Serialize, Clone)]
pub struct GraphEdge {
    pub source: String,
    pub target: String,
    pub rel_type: String,
}

fn is_test_element(element: &crate::db::models::CodeElement) -> bool {
    let qn = &element.qualified_name;
    let fp = &element.file_path;
    qn.contains("test_") || qn.contains("_test.") || qn.ends_with("_test") 
        || fp.contains("_test.") || fp.contains("/test/") || fp.contains("\\test\\")
}

fn build_nav_html() -> String {
    r#"
        <nav style="margin-bottom: 20px; padding: 10px; background: #f5f5f5; border-radius: 8px;">
            <a href="/" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Dashboard</a>
            <a href="/graph" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Graph</a>
            <a href="/browse" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Browse</a>
            <a href="/docs" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Docs</a>
            <a href="/annotate" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Annotate</a>
            <a href="/quality" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Quality</a>
            <a href="/export" style="margin-right: 15px; text-decoration: none; color: #333; font-weight: 500;">Export</a>
            <a href="/settings" style="text-decoration: none; color: #333; font-weight: 500;">Settings</a>
        </nav>
    "#.to_string()
}

fn base_html(title: &str, content: &str) -> String {
    format!(
        r#"<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>LeanKG - {}</title>
    <style>
        * {{ box-sizing: border-box; margin: 0; padding: 0; }}
        body {{ font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 1400px; margin: 0 auto; padding: 20px; background: #fafafa; }}
        nav {{ margin-bottom: 20px; padding: 12px 20px; background: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }}
        nav a {{ margin-right: 20px; text-decoration: none; color: #444; font-weight: 500; transition: color 0.2s; }}
        nav a:hover {{ color: #0066cc; }}
        h1 {{ color: #333; margin-bottom: 20px; font-size: 1.8rem; }}
        h2 {{ color: #444; margin: 15px 0; font-size: 1.3rem; }}
        .card {{ background: #fff; padding: 20px; margin-bottom: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }}
        .stats {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin-bottom: 20px; }}
        .stat-box {{ background: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); text-align: center; }}
        .stat-box .value {{ font-size: 2rem; font-weight: 700; color: #0066cc; }}
        .stat-box .label {{ color: #666; font-size: 0.9rem; margin-top: 5px; }}
        input, select, textarea {{ width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 6px; font-size: 14px; margin-bottom: 10px; }}
        button {{ background: #0066cc; color: #fff; padding: 10px 20px; border: none; border-radius: 6px; cursor: pointer; font-weight: 500; transition: background 0.2s; }}
        button:hover {{ background: #0052a3; }}
        table {{ width: 100%; border-collapse: collapse; }}
        th, td {{ padding: 12px; text-align: left; border-bottom: 1px solid #eee; }}
        th {{ background: #f8f9fa; font-weight: 600; color: #333; }}
        tr:hover {{ background: #f8f9fa; }}
        .badge {{ display: inline-block; padding: 4px 8px; border-radius: 4px; font-size: 12px; font-weight: 500; }}
        .badge-function {{ background: #e3f2fd; color: #1565c0; }}
        .badge-class {{ background: #f3e5f5; color: #7b1fa2; }}
        .badge-file {{ background: #e8f5e9; color: #2e7d32; }}
        .badge-module {{ background: #fff3e0; color: #e65100; }}
        #graph-container {{ width: 100%; height: 600px; border: 1px solid #ddd; border-radius: 8px; background: #fff; position: relative; }}
        .loading {{ text-align: center; padding: 40px; color: #666; }}
        .error {{ background: #ffebee; color: #c62828; padding: 15px; border-radius: 6px; margin-bottom: 15px; }}
        .success {{ background: #e8f5e9; color: #2e7d32; padding: 15px; border-radius: 6px; margin-bottom: 15px; }}
        .form-group {{ margin-bottom: 15px; }}
        .form-group label {{ display: block; margin-bottom: 5px; font-weight: 500; color: #333; }}
    </style>
</head>
<body>
    {}
    <h1>{}</h1>
    {}
</body>
</html>"#,
        title,
        build_nav_html(),
        title,
        content
    )
}

#[allow(dead_code)]
pub async fn index(State(state): State<AppState>) -> axum::response::Html<String> {
    let mut element_count = 0usize;
    let mut relationship_count = 0usize;
    let mut annotation_count = 0usize;
    let mut files_count = 0usize;
    let mut functions_count = 0usize;
    let mut classes_count = 0usize;

    if let Ok(graph) = state.get_graph_engine().await {
        if let Ok(elements) = graph.all_elements() {
            element_count = elements.len();
            let unique_files: std::collections::HashSet<_> = elements.iter().map(|e| e.file_path.clone()).collect();
            files_count = unique_files.len();
            functions_count = elements
                .iter()
                .filter(|x| x.element_type == "function")
                .count();
            classes_count = elements
                .iter()
                .filter(|x| x.element_type == "class")
                .count();
        }
        if let Ok(relns) = graph.all_relationships() {
            relationship_count = relns.len();
        }
        if let Ok(anns) = graph.all_annotations() {
            annotation_count = anns.len();
        }
    }

    let content = format!(
        r#"
        <div class="stats">
            <div class="stat-box"><div class="value">{}</div><div class="label">Total Elements</div></div>
            <div class="stat-box"><div class="value">{}</div><div class="label">Relationships</div></div>
            <div class="stat-box"><div class="value">{}</div><div class="label">Annotations</div></div>
        </div>
        <div class="stats">
            <div class="stat-box"><div class="value">{}</div><div class="label">Files</div></div>
            <div class="stat-box"><div class="value">{}</div><div class="label">Functions</div></div>
            <div class="stat-box"><div class="value">{}</div><div class="label">Classes</div></div>
        </div>
        <div class="card">
            <h2>Getting Started</h2>
            <p style="color: #666; margin-bottom: 10px;">Use the CLI to index your codebase:</p>
            <code style="display: block; background: #f5f5f5; padding: 15px; border-radius: 6px; margin-bottom: 15px;">leankg init && leankg index ./src</code>
            <p style="color: #666;">Then start the server with:</p>
            <code style="display: block; background: #f5f5f5; padding: 15px; border-radius: 6px;">leankg serve</code>
        </div>
        <div class="card">
            <h2>Quick Actions</h2>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                <a href="/graph"><button>View Graph</button></a>
                <a href="/browse"><button>Browse Code</button></a>
                <a href="/annotate"><button>Add Annotation</button></a>
                <a href="/export"><button>Export Graph</button></a>
            </div>
        </div>"#,
        element_count,
        relationship_count,
        annotation_count,
        files_count,
        functions_count,
        classes_count
    );

    axum::response::Html(base_html("Dashboard", &content))
}

#[allow(dead_code)]
pub async fn graph() -> axum::response::Html<String> {
    let content = r#"
        <script src="https://cdnjs.cloudflare.com/ajax/libs/sigma.js/1.2.1/sigma.min.js"></script>
        <script src="https://cdnjs.cloudflare.com/ajax/libs/sigma.js/1.2.1/plugins/sigma.parsers.json.min.js"></script>
        <div class="card">
            <h2>Code Dependency Graph</h2>
            <p style="color: #666; margin-bottom: 15px;">Interactive visualization of code elements and their relationships.</p>
            <div id="graph-filter" style="margin-bottom: 15px; display: flex; gap: 8px; flex-wrap: wrap;">
                <button class="filter-btn active" data-filter="all" onclick="setFilter('all')">All</button>
                <button class="filter-btn" data-filter="document" onclick="setFilter('document')">Document</button>
                <button class="filter-btn" data-filter="function" onclick="setFilter('function')">Function</button>
            </div>
            <div id="graph-container"><div class="loading">Loading graph data...</div></div>
        </div>
        <div class="card">
            <h3>Graph Controls</h3>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                <button onclick="zoomIn()">Zoom In</button>
                <button onclick="zoomOut()">Zoom Out</button>
                <button onclick="resetZoom()">Reset</button>
                <button onclick="startLayout()">Run Layout</button>
            </div>
        </div>
        <style>
            .filter-btn { background: #e0e0e0; color: #333; padding: 8px 16px; border: none; border-radius: 6px; cursor: pointer; font-weight: 500; transition: all 0.2s; }
            .filter-btn:hover { background: #d0d0d0; }
            .filter-btn.active { background: #0066cc; color: #fff; }
            #graph-container canvas { width: 100% !important; height: 100% !important; }
        </style>
        <script>
            let sig = null;
            let graphDataCache = null;
            let currentFilter = 'all';
            const docTypes = ['document', 'doc_section'];
            const funcTypes = ['function', 'class', 'struct'];
            
            function isTestElement(node) {
                const qn = node.id.toLowerCase();
                const fp = node.file_path ? node.file_path.toLowerCase() : '';
                return qn.includes('test_') || qn.includes('_test.') || qn.endsWith('_test') 
                    || fp.includes('_test.') || fp.includes('/test/') || fp.includes('\\test\\')
                    || fp.includes('benchmark');
            }
            
            function filterTestElements(data) {
                const nodeIds = new Set();
                data.nodes.forEach(n => { if (!isTestElement(n)) nodeIds.add(n.id); });
                const filteredEdges = data.edges.filter(e => nodeIds.has(e.source) && nodeIds.has(e.target));
                const filteredNodes = data.nodes.filter(n => nodeIds.has(n.id));
                return { nodes: filteredNodes, edges: filteredEdges };
            }
            
            async function loadGraph() {
                try {
                    const response = await fetch('/api/graph/data');
                    if (!response.ok) {
                        throw new Error('HTTP ' + response.status + ': ' + response.statusText);
                    }
                    const data = await response.json();
                    if (data.success && data.data) { 
                        graphDataCache = filterTestElements(data.data); 
                        initGraph(graphDataCache); 
                    } else {
                        document.getElementById('graph-container').innerHTML = '<div class="error">No graph data available. Index your codebase first.</div>';
                    }
                } catch (e) {
                    console.error('Graph load error:', e);
                    document.getElementById('graph-container').innerHTML = '<div class="error">Failed to load graph: ' + (e.message || String(e)) + '</div>';
                }
            }
            
            function setFilter(filter) {
                currentFilter = filter;
                document.querySelectorAll('.filter-btn').forEach(btn => btn.classList.remove('active'));
                document.querySelector('.filter-btn[data-filter="' + filter + '"]').classList.add('active');
                if (graphDataCache) initGraph(graphDataCache);
            }
            
            function getFilteredData(data) {
                const filteredNodes = [];
                const filteredEdges = [];
                const nodeIds = new Set();
                const edgeNodeIds = new Set();
                
                if (currentFilter === 'all') {
                    data.nodes.forEach(n => { filteredNodes.push(n); nodeIds.add(n.id); });
                    data.edges.forEach(e => { if (nodeIds.has(e.source) && nodeIds.has(e.target)) { filteredEdges.push(e); edgeNodeIds.add(e.source); edgeNodeIds.add(e.target); } });
                    const finalNodes = filteredNodes.filter(n => edgeNodeIds.has(n.id));
                    return { nodes: finalNodes, edges: filteredEdges };
                } else if (currentFilter === 'document') {
                    data.nodes.forEach(n => { if (docTypes.includes(n.element_type)) { filteredNodes.push(n); nodeIds.add(n.id); } });
                    data.edges.forEach(e => { if (nodeIds.has(e.target)) { filteredEdges.push(e); edgeNodeIds.add(e.source); edgeNodeIds.add(e.target); } });
                    const relatedNodes = data.nodes.filter(n => edgeNodeIds.has(n.id));
                    return { nodes: relatedNodes, edges: filteredEdges };
                } else if (currentFilter === 'function') {
                    data.nodes.forEach(n => { if (funcTypes.includes(n.element_type)) { filteredNodes.push(n); nodeIds.add(n.id); } });
                    data.edges.forEach(e => { if (nodeIds.has(e.source) || nodeIds.has(e.target)) { filteredEdges.push(e); edgeNodeIds.add(e.source); edgeNodeIds.add(e.target); } });
                    const relatedNodes = data.nodes.filter(n => edgeNodeIds.has(n.id));
                    return { nodes: relatedNodes, edges: filteredEdges };
                }
                return { nodes: filteredNodes, edges: filteredEdges };
            }
            
            function applyLimitAndOrphan(data) {
                if (data.nodes.length <= 500 && data.edges.length <= 1000) {
                    return data;
                }
                const nodeConnectCount = {};
                data.edges.forEach(e => { 
                    nodeConnectCount[e.source] = (nodeConnectCount[e.source] || 0) + 1; 
                    nodeConnectCount[e.target] = (nodeConnectCount[e.target] || 0) + 1; 
                });
                const sortedNodes = [...data.nodes].sort((a, b) => 
                    (nodeConnectCount[b.id] || 0) - (nodeConnectCount[a.id] || 0)
                );
                const topNodeIds = new Set(sortedNodes.slice(0, 500).map(n => n.id));
                const filteredNodes = data.nodes.filter(n => topNodeIds.has(n.id));
                const filteredEdges = data.edges.filter(e => topNodeIds.has(e.source) && topNodeIds.has(e.target)).slice(0, 1000);
                return { nodes: filteredNodes, edges: filteredEdges };
            }
            
            function initGraph(fullData) {
                const filtered = getFilteredData(fullData);
                const data = applyLimitAndOrphan(filtered);
                const container = document.getElementById('graph-container');
                container.innerHTML = '';
                
                if (data.nodes.length === 0) {
                    container.innerHTML = '<div class="error">No graph data available.</div>';
                    return;
                }
                
                if (sig) {
                    sig.kill();
                }
                
                const colors = {
                    'file': '#2e7d32', 
                    'function': '#1565c0', 
                    'class': '#7b1fa2', 
                    'module': '#e65100', 
                    'document': '#e65100', 
                    'doc_section': '#ff9800', 
                    'struct': '#1565c0'
                };
                
                const idCounts = {};
                data.nodes.forEach(n => { idCounts[n.id] = (idCounts[n.id] || 0) + 1; });
                
                const nodeCounter = {};
                const graphNodes = data.nodes.map((n, idx) => {
                    if (!idCounts[n.id]) idCounts[n.id] = 0;
                    if (idCounts[n.id] === 1) {
                        return { id: n.id, originalId: n.id, idx };
                    }
                    if (!nodeCounter[n.id]) nodeCounter[n.id] = 0;
                    nodeCounter[n.id]++;
                    return { id: n.id + '_' + nodeCounter[n.id], originalId: n.id, idx };
                });
                
                const edgeCounter = {};
                const edgeMap = new Map();
                data.edges.forEach((e, edgeIdx) => {
                    if (!edgeCounter[e.source]) edgeCounter[e.source] = 0;
                    if (!edgeCounter[e.target]) edgeCounter[e.target] = 0;
                    edgeCounter[e.source]++;
                    edgeCounter[e.target]++;
                    const key = e.source + '|' + e.target;
                    if (!edgeMap.has(key)) edgeMap.set(key, []);
                    edgeMap.get(key).push(edgeIdx);
                });
                
                const graphEdges = [];
                const usedEdgeInstances = {};
                data.edges.forEach((e, edgeIdx) => {
                    let srcNodeId, tgtNodeId;
                    
                    const srcCount = idCounts[e.source] || 0;
                    const tgtCount = idCounts[e.target] || 0;
                    
                    if (srcCount <= 1) {
                        srcNodeId = e.source;
                    } else {
                        const srcInstances = graphNodes.filter(n => n.originalId === e.source);
                        const srcUsed = usedEdgeInstances[e.source] || 0;
                        srcNodeId = srcInstances[Math.min(srcUsed, srcInstances.length - 1)]?.id || e.source;
                    }
                    
                    if (tgtCount <= 1) {
                        tgtNodeId = e.target;
                    } else {
                        const tgtInstances = graphNodes.filter(n => n.originalId === e.target);
                        const tgtUsed = usedEdgeInstances[e.target + '_tgt'] || 0;
                        tgtNodeId = tgtInstances[Math.min(tgtUsed, tgtInstances.length - 1)]?.id || e.target;
                        usedEdgeInstances[e.target + '_tgt'] = (usedEdgeInstances[e.target + '_tgt'] || 0) + 1;
                    }
                    
                    usedEdgeInstances[e.source] = (usedEdgeInstances[e.source] || 0) + 1;
                    
                    if (srcNodeId && tgtNodeId) {
                        graphEdges.push({
                            id: 'edge_' + edgeIdx,
                            source: srcNodeId,
                            target: tgtNodeId,
                            size: 1
                        });
                    }
                });
                
                const validNodeIds = new Set(graphNodes.map(n => n.id));
                const finalEdges = graphEdges.filter(e => 
                    validNodeIds.has(e.source) && validNodeIds.has(e.target)
                );
                
                const graphData = {
                    nodes: graphNodes.map(n => ({
                        id: n.id,
                        label: data.nodes[n.idx].label,
                        x: Math.random() * 100,
                        y: Math.random() * 100,
                        size: 5,
                        color: colors[data.nodes[n.idx].element_type] || '#666',
                        type: data.nodes[n.idx].element_type
                    })),
                    edges: finalEdges
                };
                
                sig = new sigma({
                    graph: graphData,
                    container: container,
                    settings: {
                        drawLabels: true,
                        drawEdgeLabels: false,
                        defaultNodeColor: '#666',
                        defaultEdgeColor: '#999',
                        font: 'Arial',
                        labelSize: 10,
                        labelColor: '#333333',
                        labelHover: true,
                        labelSizeRatio: 1.5
                    }
                });
                
                sig.refresh();
                
                sig.bind('clickNode', function(e) {
                    const node = e.data.node;
                    alert('Element: ' + node.label + '\nType: ' + node.type + '\nID: ' + node.id);
                });
            }
            
            function startLayout() {
                if (sig) {
                    if (sigma.layouts && sigma.layouts.config) {
                        sigma.layouts.startForceAtlas2({ worker: false, iterations: 100 });
                    } else {
                        alert('ForceAtlas2 layout not available. Using default positioning.');
                    }
                }
            }
            
            function zoomIn() { if (sig && sig.camera) sigma.camera.goTo({ ratio: sigma.camera.ratio * 0.7 }); }
            function zoomOut() { if (sig && sig.camera) sigma.camera.goTo({ ratio: sigma.camera.ratio * 1.3 }); }
            function resetZoom() { if (sig && sig.camera) sigma.camera.goTo({ x: 0, y: 0, ratio: 1 }); }
            
            loadGraph();
        </script>"#;

    axum::response::Html(base_html("Graph Visualization", content))
}

#[allow(dead_code)]
pub async fn browse(State(state): State<AppState>) -> axum::response::Html<String> {
    let elements: Vec<_> = if let Ok(g) = state.get_graph_engine().await {
        g.all_elements().unwrap_or_default()
    } else {
        vec![]
    };

    let mut functions: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "function")
        .collect();
    let mut classes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "class")
        .collect();

    let mut file_paths: std::collections::HashSet<_> = elements.iter().map(|e| e.file_path.clone()).collect();
    let mut files: Vec<_> = file_paths.drain().map(|fp| {
        let count = elements.iter().filter(|e| e.file_path == fp).count();
        (fp, count)
    }).collect();
    files.sort_by(|a, b| a.0.cmp(&b.0));

    functions.sort_by(|a, b| a.qualified_name.cmp(&b.qualified_name));
    classes.sort_by(|a, b| a.qualified_name.cmp(&b.qualified_name));

    let files_html: String = files.iter().map(|(fp, count)| format!(r#"<tr><td><span class="badge badge-file">file</span></td><td><code>{}</code></td><td>{}</td><td>{}</td></tr>"#, fp, fp, count)).collect();
    let functions_html: String = functions.iter().take(100).map(|e| format!(r#"<tr><td><span class="badge badge-function">function</span></td><td><code>{}</code></td><td>{}</td><td>{}</td></tr>"#, e.qualified_name, e.file_path, e.line_end - e.line_start + 1)).collect();
    let classes_html: String = classes.iter().map(|e| format!(r#"<tr><td><span class="badge badge-class">class</span></td><td><code>{}</code></td><td>{}</td><td>{}</td></tr>"#, e.qualified_name, e.file_path, e.line_end - e.line_start + 1)).collect();

    let content = format!(
        r#"
        <div class="card">
            <h2>Search</h2>
            <input type="text" id="searchInput" placeholder="Search elements..." onkeyup="filterTable()">
        </div>
        <div class="card">
            <h2>Files ({})</h2>
            <table id="filesTable"><thead><tr><th>Type</th><th>Name</th><th>File</th><th>Lines</th></tr></thead><tbody>{}</tbody></table>
        </div>
        <div class="card">
            <h2>Functions ({})</h2>
            <table id="functionsTable"><thead><tr><th>Type</th><th>Name</th><th>File</th><th>Lines</th></tr></thead><tbody>{}</tbody></table>
        </div>
        <div class="card">
            <h2>Classes ({})</h2>
            <table id="classesTable"><thead><tr><th>Type</th><th>Name</th><th>File</th><th>Lines</th></tr></thead><tbody>{}</tbody></table>
        </div>
        <script>
            function filterTable() {{ const input = document.getElementById('searchInput').value.toLowerCase(); ['filesTable', 'functionsTable', 'classesTable'].forEach(id => {{ const t = document.getElementById(id); if (!t) return; [...t.rows].slice(1).forEach(r => r.style.display = r.textContent.toLowerCase().includes(input) ? '' : 'none'); }}); }}
        </script>"#,
        files.len(),
        files_html,
        functions.len(),
        functions_html,
        classes.len(),
        classes_html
    );

    axum::response::Html(base_html("Code Browser", &content))
}

#[allow(dead_code)]
pub async fn docs() -> axum::response::Html<String> {
    let content = r#"
        <div class="card">
            <h2>Documentation Viewer</h2>
            <p style="color: #666;">View generated documentation for your codebase.</p>
        </div>
        <div class="card">
            <h2>Available Documentation</h2>
            <div style="padding: 20px; background: #f5f5f5; border-radius: 8px; margin-bottom: 10px;">
                <h3>AGENTS.md</h3>
                <p style="color: #666;">Codebase context documentation for AI assistants</p>
            </div>
            <div style="padding: 20px; background: #f5f5f5; border-radius: 8px;">
                <h3>CLAUDE.md</h3>
                <p style="color: #666;">Claude-specific context documentation</p>
            </div>
        </div>
        <div class="card">
            <p style="color: #666;">Generate documentation using: <code>leankg generate</code></p>
        </div>"#;

    axum::response::Html(base_html("Documentation", content))
}

#[allow(dead_code)]
pub async fn annotate(State(state): State<AppState>) -> axum::response::Html<String> {
    let elements: Vec<_> = match state.get_graph_engine().await {
        Ok(g) => g.all_elements().unwrap_or_default(),
        Err(_) => vec![],
    };
    let annotations: Vec<_> = match state.get_graph_engine().await {
        Ok(g) => g.all_annotations().unwrap_or_default(),
        Err(_) => vec![],
    };

    let element_options: String = elements
        .iter()
        .map(|e| {
            format!(
                r#"<option value="{}">{} ({} - {})</option>"#,
                e.qualified_name, e.qualified_name, e.element_type, e.file_path
            )
        })
        .collect();
    let annotations_html: String = annotations
        .iter()
        .map(|a| {
            format!(
                r#"<tr><td><code>{}</code></td><td>{}</td><td>{}</td><td>{}</td></tr>"#,
                a.element_qualified,
                a.description,
                a.user_story_id.as_deref().unwrap_or("-"),
                a.feature_id.as_deref().unwrap_or("-")
            )
        })
        .collect();

    let content = format!(
        r#"
        <div class="card">
            <h2>Add Annotation</h2>
            <form id="annotationForm" onsubmit="submitAnnotation(event)">
                <div class="form-group"><label>Code Element</label><select id="elementSelect" required><option value="">Select...</option>{}</select></div>
                <div class="form-group"><label>Description</label><textarea id="descriptionInput" rows="4" required></textarea></div>
                <div class="form-group"><label>User Story ID</label><input type="text" id="userStoryInput" placeholder="US-123"></div>
                <div class="form-group"><label>Feature ID</label><input type="text" id="featureInput" placeholder="FEAT-AUTH"></div>
                <button type="submit">Save</button>
            </form>
            <div id="formMessage"></div>
        </div>
        <div class="card">
            <h2>Existing Annotations ({})</h2>
            <table><thead><tr><th>Element</th><th>Description</th><th>User Story</th><th>Feature</th></tr></thead><tbody>{}</tbody></table>
        </div>
        <script>
            async function submitAnnotation(e) {{ e.preventDefault(); const d = {{ element_qualified: document.getElementById('elementSelect').value, description: document.getElementById('descriptionInput').value, user_story_id: document.getElementById('userStoryInput').value || null, feature_id: document.getElementById('featureInput').value || null }}; try {{ const r = await fetch('/api/annotations', {{ method: 'POST', headers: {{ 'Content-Type': 'application/json' }}, body: JSON.stringify(d) }}); const res = await r.json(); document.getElementById('formMessage').innerHTML = res.success ? '<div class="success">Saved!</div>' : '<div class="error">' + (res.error || 'Error') + '</div>'; if (res.success) setTimeout(() => location.reload(), 1000); }} catch (err) {{ document.getElementById('formMessage').innerHTML = '<div class="error">' + err.message + '</div>'; }} }}
        </script>"#,
        element_options,
        annotations.len(),
        annotations_html
    );

    axum::response::Html(base_html("Annotate", &content))
}

#[allow(dead_code)]
pub async fn quality() -> axum::response::Html<String> {
    let content = r#"
        <div class="card">
            <h2>Code Quality Metrics</h2>
            <p style="color: #666;">Analyze code quality and identify potential issues.</p>
        </div>
        <div class="card">
            <h2>Quality Checks</h2>
            <div style="padding: 20px; background: #f5f5f5; border-radius: 8px; margin-bottom: 15px;">
                <h3>Large Functions</h3>
                <p style="color: #666;">Functions exceeding 50 lines may benefit from refactoring.</p>
                <code>leankg quality --min-lines 50</code>
            </div>
            <div style="padding: 20px; background: #f5f5f5; border-radius: 8px;">
                <h3>Language Filter</h3>
                <p style="color: #666;">Filter by programming language.</p>
                <code>leankg quality --lang go</code>
            </div>
        </div>"#;

    axum::response::Html(base_html("Code Quality", content))
}

#[allow(dead_code)]
pub async fn export_page() -> axum::response::Html<String> {
    let content = r#"
        <div class="card">
            <h2>Export Graph Data</h2>
            <p style="color: #666;">Download the code dependency graph data for external visualization.</p>
            <button onclick="exportJSON()">Export as JSON</button>
            <div id="exportStatus" style="margin-top: 15px;"></div>
        </div>
        <div class="card">
            <h2>Export Instructions</h2>
            <p style="color: #666;">The JSON export contains all graph data (nodes and edges) that can be visualized using D3.js or similar libraries.</p>
            <p style="color: #666; margin-top: 10px;">Use the /api/export/graph endpoint to fetch data programmatically.</p>
        </div>
        <script>
            function exportJSON() {
                const status = document.getElementById('exportStatus');
                status.innerHTML = '<div class="loading">Generating...</div>';
                fetch('/api/export/graph')
                    .then(r => r.json())
                    .then(d => {
                        if (d.success && d.data) {
                            const blob = new Blob([JSON.stringify(d.data, null, 2)], { type: 'application/json' });
                            const url = URL.createObjectURL(blob);
                            const a = document.createElement('a');
                            a.href = url;
                            a.download = 'leankg-graph.json';
                            a.click();
                            URL.revokeObjectURL(url);
                            status.innerHTML = '<div class="success">Export complete!</div>';
                        } else {
                            status.innerHTML = '<div class="error">No data. Index first.</div>';
                        }
                    })
                    .catch(e => { status.innerHTML = '<div class="error">Error: ' + e.message + '</div>'; });
            }
        </script>"#;

    axum::response::Html(base_html("Export", content))
}

#[allow(dead_code)]
pub async fn settings() -> axum::response::Html<String> {
    let content = r#"
        <div class="card">
            <h2>Settings</h2>
            <p style="color: #666;">Configure your LeanKG settings.</p>
        </div>
        <div class="card">
            <h2>Project Configuration</h2>
            <div class="form-group"><label>Database Path</label><code>.leankg/</code></div>
            <div class="form-group"><label>Index Path</label><code>./src</code></div>
        </div>
        <div class="card">
            <h2>About</h2>
            <p style="color: #666;">LeanKG - Lightweight knowledge graph for AI-assisted development.</p>
            <p style="color: #666; margin-top: 10px;">Version: 0.1.0</p>
        </div>"#;

    axum::response::Html(base_html("Settings", content))
}

#[allow(dead_code)]
pub async fn api_elements(State(state): State<AppState>) -> impl IntoResponse {
    let result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_elements().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match result {
        Ok(elements) => ApiResponse {
            success: true,
            data: Some(elements),
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_relationships(State(state): State<AppState>) -> impl IntoResponse {
    let result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_relationships().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match result {
        Ok(rels) => ApiResponse {
            success: true,
            data: Some(rels),
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_annotations(State(state): State<AppState>) -> impl IntoResponse {
    let result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_annotations().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match result {
        Ok(anns) => ApiResponse {
            success: true,
            data: Some(anns),
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_create_annotation(
    State(state): State<AppState>,
    Json(req): Json<AnnotationRequest>,
) -> impl IntoResponse {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => {
            return ApiResponse {
                success: false,
                data: None,
                error: Some(e.to_string()),
            }
        }
    };
    let result = db::create_business_logic(
        &db,
        &req.element_qualified,
        &req.description,
        req.user_story_id.as_deref(),
        req.feature_id.as_deref(),
    );
    match result {
        Ok(bl) => ApiResponse {
            success: true,
            data: Some(bl),
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e.to_string()),
        },
    }
}

#[allow(dead_code)]
pub async fn api_get_annotation(
    State(state): State<AppState>,
    Path(element): Path<String>,
) -> impl IntoResponse {
    let result: Result<Option<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.get_annotation(&element).map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match result {
        Ok(ann) => ApiResponse {
            success: true,
            data: ann,
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_update_annotation(
    State(state): State<AppState>,
    Path(_element): Path<String>,
    Json(req): Json<AnnotationRequest>,
) -> impl IntoResponse {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => {
            return ApiResponse {
                success: false,
                data: None,
                error: Some(e.to_string()),
            }
        }
    };
    let result = db::update_business_logic(
        &db,
        &req.element_qualified,
        &req.description,
        req.user_story_id.as_deref(),
        req.feature_id.as_deref(),
    );
    match result {
        Ok(bl) => ApiResponse {
            success: true,
            data: bl,
            error: None,
        },
        Err(e) => ApiResponse {
            success: false,
            data: None,
            error: Some(e.to_string()),
        },
    }
}

#[allow(dead_code)]
pub async fn api_search(
    State(state): State<AppState>,
    Query(params): Query<SearchParams>,
) -> impl IntoResponse {
    let elements_result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_elements().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    let elements = match elements_result {
        Ok(e) => e,
        Err(e) => {
            return ApiResponse {
                success: false,
                data: None,
                error: Some(e.to_string()),
            }
        }
    };
    let mut filtered: Vec<_> = elements.iter().collect();
    if let Some(ref q) = params.q {
        let ql = q.to_lowercase();
        filtered.retain(|e| {
            e.qualified_name.to_lowercase().contains(&ql)
                || e.name.to_lowercase().contains(&ql)
                || e.file_path.to_lowercase().contains(&ql)
        });
    }
    if let Some(ref et) = params.element_type {
        filtered.retain(|e| e.element_type == *et);
    }
    if let Some(ref fp) = params.file_path {
        filtered.retain(|e| e.file_path.contains(fp));
    }
    let result: Vec<_> = filtered.into_iter().cloned().collect();
    ApiResponse {
        success: true,
        data: Some(result),
        error: None,
    }
}

#[allow(dead_code)]
pub async fn api_graph_data(State(state): State<AppState>) -> impl IntoResponse {
    let elements_result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_elements().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };

    let relationships_result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_relationships().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match (elements_result, relationships_result) {
        (Ok(elements), Ok(relationships)) => {
            let nodes: Vec<GraphNode> = elements
                .iter()
                .map(|e| GraphNode {
                    id: e.qualified_name.clone(),
                    label: e.name.clone(),
                    element_type: e.element_type.clone(),
                    file_path: e.file_path.clone(),
                })
                .collect();
            let node_ids: std::collections::HashSet<_> = nodes.iter().map(|n| n.id.clone()).collect();
            let edges: Vec<GraphEdge> = relationships
                .iter()
                .filter(|r| node_ids.contains(&r.source_qualified))
                .map(|r| GraphEdge {
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                })
                .collect();
            ApiResponse {
                success: true,
                data: Some(GraphData { nodes, edges, filtered: None }),
                error: None,
            }
        }
        (Err(e), _) | (_, Err(e)) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_export_graph(State(state): State<AppState>) -> impl IntoResponse {
    let elements_result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_elements().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    let relationships_result: Result<Vec<_>, String> = match state.get_graph_engine().await {
        Ok(g) => g.all_relationships().map_err(|e| e.to_string()),
        Err(e) => Err(e.to_string()),
    };
    match (elements_result, relationships_result) {
        (Ok(elements), Ok(relationships)) => {
            let nodes: Vec<GraphNode> = elements
                .iter()
                .map(|e| GraphNode {
                    id: e.qualified_name.clone(),
                    label: e.name.clone(),
                    element_type: e.element_type.clone(),
                    file_path: e.file_path.clone(),
                })
                .collect();
            let node_ids: std::collections::HashSet<_> = nodes.iter().map(|n| n.id.clone()).collect();
            let edges: Vec<GraphEdge> = relationships
                .iter()
                .filter(|r| node_ids.contains(&r.source_qualified))
                .map(|r| GraphEdge {
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                })
                .collect();
            ApiResponse {
                success: true,
                data: Some(GraphData { nodes, edges, filtered: None }),
                error: None,
            }
        }
        (Err(e), _) | (_, Err(e)) => ApiResponse {
            success: false,
            data: None,
            error: Some(e),
        },
    }
}

#[allow(dead_code)]
pub async fn api_query(
    axum::extract::Json(_req): axum::extract::Json<QueryRequest>,
) -> Result<axum::extract::Json<QueryResponse>, (StatusCode, &'static str)> {
    Ok(axum::extract::Json(QueryResponse { result: vec![] }))
}

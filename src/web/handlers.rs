use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    response::IntoResponse,
    Json,
};
use serde::{Deserialize, Serialize};
use std::process::Command;

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

#[allow(dead_code)]
fn is_test_element(element: &crate::db::models::CodeElement) -> bool {
    let qn = &element.qualified_name;
    let fp = &element.file_path;
    qn.contains("test_")
        || qn.contains("_test.")
        || qn.ends_with("_test")
        || fp.contains("_test.")
        || fp.contains("/test/")
        || fp.contains("\\test\\")
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
    let mut has_project = false;

    if let Ok(graph) = state.get_graph_engine().await {
        if let Ok(elements) = graph.all_elements() {
            has_project = !elements.is_empty();
            element_count = elements.len();
            let unique_files: std::collections::HashSet<_> =
                elements.iter().map(|e| e.file_path.clone()).collect();
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

    if !has_project {
        return project_selector(State(state)).await;
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
            <h2>Current Project</h2>
            <p style="color: #666; margin-bottom: 10px;">
                <code>{}</code>
            </p>
            <p style="color: #666; margin-bottom: 15px;">
                <a href="/project" style="color: #0066cc;">Switch Project</a>
            </p>
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
        classes_count,
        state.current_project_path.read().await.display()
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
            <div id="node-tooltip" style="position: fixed;">
                <div class="tooltip-header">
                    <span class="tooltip-name" id="tooltip-name"></span>
                    <span class="tooltip-type" id="tooltip-type"></span>
                </div>
                <div class="tooltip-related" id="tooltip-related" style="display: none;">
                    <div class="tooltip-related-title">Related Nodes</div>
                    <ul class="tooltip-related-list" id="tooltip-related-list"></ul>
                </div>
            </div>
        </div>
        <div class="card">
            <h3>Graph Controls</h3>
            <div style="display: flex; gap: 10px; flex-wrap: wrap; align-items: center;">
                <button onclick="zoomIn()">Zoom In</button>
                <button onclick="zoomOut()">Zoom Out</button>
                <button onclick="resetZoom()">Reset</button>
                <select id="layout-select" onchange="applyLayout(this.value)" style="padding: 8px; border-radius: 6px; border: 1px solid #ddd; font-size: 14px; cursor: pointer;">
                    <option value="force">Force Atlas 2</option>
                    <option value="circular">Circular</option>
                    <option value="grid">Grid</option>
                    <option value="hierarchical">Hierarchical</option>
                    <option value="random">Random</option>
                </select>
                <button onclick="applyLayout(document.getElementById('layout-select').value)">Apply Layout</button>
            </div>
        </div>
        <style>
            .filter-btn { background: #e0e0e0; color: #333; padding: 8px 16px; border: none; border-radius: 6px; cursor: pointer; font-weight: 500; transition: all 0.2s; }
            .filter-btn:hover { background: #d0d0d0; }
            .filter-btn.active { background: #0066cc; color: #fff; }
            #graph-container { width: 100%; height: 600px; }
            #graph-container canvas { width: 100% !important; height: 100% !important; }
            #node-tooltip {
                position: fixed;
                background: #fff;
                border: 1px solid #ddd;
                border-radius: 8px;
                padding: 12px 16px;
                box-shadow: 0 4px 12px rgba(0,0,0,0.15);
                z-index: 1000;
                max-width: 300px;
                pointer-events: none;
                opacity: 0;
                transition: opacity 0.15s ease-in;
                visibility: hidden;
            }
            #node-tooltip.visible {
                opacity: 1;
                visibility: visible;
            }
            #node-tooltip .tooltip-header {
                display: flex;
                align-items: center;
                gap: 8px;
                margin-bottom: 8px;
            }
            #node-tooltip .tooltip-name {
                font-weight: 600;
                font-size: 14px;
                color: #333;
                word-break: break-word;
            }
            #node-tooltip .tooltip-type {
                display: inline-block;
                padding: 2px 8px;
                border-radius: 4px;
                font-size: 11px;
                font-weight: 500;
            }
            .tooltip-type-function { background: #e3f2fd; color: #1565c0; }
            .tooltip-type-class { background: #f3e5f5; color: #7b1fa2; }
            .tooltip-type-file { background: #e8f5e9; color: #2e7d32; }
            .tooltip-type-module { background: #fff3e0; color: #e65100; }
            .tooltip-type-document { background: #fff3e0; color: #e65100; }
            .tooltip-type-struct { background: #e3f2fd; color: #1565c0; }
            .tooltip-type-default { background: #f5f5f5; color: #666; }
            #node-tooltip .tooltip-related {
                border-top: 1px solid #eee;
                padding-top: 8px;
                margin-top: 4px;
            }
            #node-tooltip .tooltip-related-title {
                font-size: 11px;
                color: #888;
                margin-bottom: 4px;
                text-transform: uppercase;
                letter-spacing: 0.5px;
            }
            #node-tooltip .tooltip-related-list {
                list-style: none;
                padding: 0;
                margin: 0;
            }
            #node-tooltip .tooltip-related-list li {
                font-size: 12px;
                color: #555;
                padding: 2px 0;
                display: flex;
                align-items: flex-start;
                gap: 4px;
            }
            #node-tooltip .tooltip-related-list .rel-arrow {
                color: #888;
                flex-shrink: 0;
            }
            #node-tooltip .tooltip-related-list .rel-type {
                color: #999;
                font-size: 11px;
            }
        </style>
        <script src="https://cdnjs.cloudflare.com/ajax/libs/graphology/0.25.4/graphology.umd.min.js"></script>
        <script src="https://cdnjs.cloudflare.com/ajax/libs/sigma.js/2.4.0/sigma.min.js"></script>
        <script>
            const Graph = graphology.Graph;
            let sig = null;
            window.sig = null;
            let graphDataCache = null;
            let currentFilter = 'all';
            const docTypes = ['document', 'doc_section'];
            const funcTypes = ['function', 'class', 'struct'];
            const nodePositionCache = {};
            let mouseX = 0, mouseY = 0;
            
            document.addEventListener('mousemove', (e) => {
                mouseX = e.clientX;
                mouseY = e.clientY;
            });
            
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
                const filteredNodes = data.nodes.filter(n => nodeIds.has(n.id));
                const filteredEdges = data.edges.filter(e => nodeIds.has(e.source) && nodeIds.has(e.target));
                return { nodes: filteredNodes.map(n => ({...n})), edges: filteredEdges.map(e => ({...e})) };
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
                
                if (currentFilter === 'all') {
                    data.nodes.forEach(n => { filteredNodes.push({...n}); nodeIds.add(n.id); });
                    data.edges.forEach(e => { if (nodeIds.has(e.source) && nodeIds.has(e.target)) { filteredEdges.push({...e}); } });
                    return { nodes: filteredNodes, edges: filteredEdges };
                } else if (currentFilter === 'document') {
                    data.nodes.forEach(n => { if (docTypes.includes(n.element_type)) { filteredNodes.push({...n}); nodeIds.add(n.id); } });
                    data.edges.forEach(e => { if (nodeIds.has(e.source) && nodeIds.has(e.target)) { filteredEdges.push({...e}); } });
                    return { nodes: filteredNodes, edges: filteredEdges };
                } else if (currentFilter === 'function') {
                    data.nodes.forEach(n => { if (funcTypes.includes(n.element_type)) { filteredNodes.push({...n}); nodeIds.add(n.id); } });
                    data.edges.forEach(e => { if (nodeIds.has(e.source) && nodeIds.has(e.target)) { filteredEdges.push({...e}); } });
                    return { nodes: filteredNodes, edges: filteredEdges };
                }
                return { nodes: filteredNodes.map(n => ({...n})), edges: filteredEdges.map(e => ({...e})) };
            }
            
            function applyLimitAndOrphan(data) {
                if (data.nodes.length <= 500 && data.edges.length <= 1000) {
                    return { nodes: data.nodes.map(n => ({...n})), edges: data.edges.map(e => ({...e})) };
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
                return { nodes: filteredNodes.map(n => ({...n})), edges: filteredEdges.map(e => ({...e})) };
            }
            
            function initGraph(fullData) {
                if (!fullData || !fullData.nodes || !fullData.edges) {
                    document.getElementById('graph-container').innerHTML = '<div class="error">Invalid graph data.</div>';
                    return;
                }
                
                if (sig) {
                    try { sig.kill(); } catch(e) { /* ignore */ }
                    sig = null;
                }
                
                const filtered = getFilteredData(fullData);
                const data = applyLimitAndOrphan(filtered);
                const container = document.getElementById('graph-container');
                container.innerHTML = '';
                
                if (data.nodes.length === 0) {
                    container.innerHTML = '<div class="error">No nodes match this filter.</div>';
                    return;
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
                
                const nodeCount = data.nodes.length;
                const maxDim = Math.max(100, Math.sqrt(nodeCount) * 8);
                
                const edgeNodeIds = new Set();
                data.edges.forEach(e => {
                    edgeNodeIds.add(e.source);
                    edgeNodeIds.add(e.target);
                });
                
                const nodeSeen = new Set();
                const connectedNodes = [];
                data.nodes.forEach(n => {
                    if (!nodeSeen.has(n.id)) {
                        nodeSeen.add(n.id);
                        connectedNodes.push(n);
                    }
                });
                const connectedNodeIds = new Set(connectedNodes.map(n => n.id));
                
                const finalEdges = data.edges.filter(e => 
                    connectedNodeIds.has(e.source) && connectedNodeIds.has(e.target)
                );
                
                const connectedNodeIndexMap = {};
                connectedNodes.forEach((n, idx) => { connectedNodeIndexMap[n.id] = idx; });
                
                const parentMapConnected = {};
                const childMapConnected = {};
                finalEdges.forEach(e => {
                    if (connectedNodeIds.has(e.source) && connectedNodeIds.has(e.target)) {
                        if (!childMapConnected[e.source]) childMapConnected[e.source] = [];
                        childMapConnected[e.source].push(e.target);
                        parentMapConnected[e.target] = e.source;
                    }
                });
                
                const connectedRootNodes = connectedNodes.filter(n => !parentMapConnected[n.id]).map(n => n.id);
                const positioned = {};
                const inProgress = {};
                
                const getOrPosition = (nodeId, depth) => {
                    if (positioned[nodeId]) return positioned[nodeId];
                    if (inProgress[nodeId]) return { x: (Math.random() - 0.5) * maxDim, y: (Math.random() - 0.5) * maxDim };
                    
                    const idx = connectedNodeIndexMap[nodeId];
                    if (idx === undefined) return { x: (Math.random() - 0.5) * maxDim, y: (Math.random() - 0.5) * maxDim };
                    
                    const cachedPos = nodePositionCache[nodeId];
                    if (cachedPos && typeof cachedPos.x === 'number' && typeof cachedPos.y === 'number') {
                        positioned[nodeId] = cachedPos;
                        return cachedPos;
                    }
                    
                    inProgress[nodeId] = true;
                    
                    const children = childMapConnected[nodeId] || [];
                    let cx = 0, cy = 0, childCount = 0;
                    children.forEach(c => {
                        if (!inProgress[c] && positioned[c]) {
                            cx += positioned[c].x;
                            cy += positioned[c].y;
                            childCount++;
                        }
                    });
                    
                    const parentId = parentMapConnected[nodeId];
                    let px = (Math.random() - 0.5) * maxDim;
                    let py = (Math.random() - 0.5) * maxDim;
                    
                    if (parentId && positioned[parentId]) {
                        px = positioned[parentId].x;
                        py = positioned[parentId].y;
                    } else {
                        const rootIdx = connectedRootNodes.indexOf(nodeId);
                        if (rootIdx >= 0) {
                            const angle = (2 * Math.PI * rootIdx) / Math.max(connectedRootNodes.length, 1);
                            px = Math.cos(angle) * maxDim * 0.7;
                            py = Math.sin(angle) * maxDim * 0.7;
                        }
                    }
                    
                    let x = px + (Math.random() - 0.5) * maxDim * 0.2;
                    let y = py + (Math.random() - 0.5) * maxDim * 0.2;
                    
                    if (childCount > 0) {
                        x = px * 0.3 + (cx / childCount) * 0.7;
                        y = py * 0.3 + (cy / childCount) * 0.7;
                    }
                    
                    if (isNaN(x) || isNaN(y)) {
                        x = (Math.random() - 0.5) * maxDim;
                        y = (Math.random() - 0.5) * maxDim;
                    }
                    
                    delete inProgress[nodeId];
                    positioned[nodeId] = { x, y };
                    return { x, y };
                };
                
                connectedNodes.forEach(n => {
                    if (!positioned[n.id]) {
                        getOrPosition(n.id, 0);
                    }
                });
                
                const nodeDegrees = {};
                connectedNodes.forEach(n => nodeDegrees[n.id] = 0);
                finalEdges.forEach(e => {
                    if (nodeDegrees[e.source] !== undefined) nodeDegrees[e.source]++;
                    if (nodeDegrees[e.target] !== undefined) nodeDegrees[e.target]++;
                });
                
                const maxDegree = Math.max(...Object.values(nodeDegrees), 1);
                const MIN_NODE_SIZE = 3;
                const MAX_NODE_SIZE = 20;
                
                const graphData = {
                    nodes: connectedNodes.map(n => {
                        const pos = positioned[n.id] || { x: (Math.random() - 0.5) * maxDim, y: (Math.random() - 0.5) * maxDim };
                        if (isNaN(pos.x) || isNaN(pos.y)) {
                            pos.x = (Math.random() - 0.5) * maxDim;
                            pos.y = (Math.random() - 0.5) * maxDim;
                        }
                        const degree = nodeDegrees[n.id] || 0;
                        const size = MIN_NODE_SIZE + ((degree / maxDegree) * (MAX_NODE_SIZE - MIN_NODE_SIZE));
                        return {
                            id: n.id,
                            label: n.label,
                            x: pos.x,
                            y: pos.y,
                            size: nodeCount > 500 ? Math.min(size, 8) : (nodeCount > 300 ? Math.min(size, 6) : size),
                            color: colors[n.element_type] || '#666',
                            elementType: n.element_type,
                            degree: degree
                        };
                    }),
                    edges: finalEdges.map(e => ({
                        ...e,
                        size: nodeCount > 500 ? 0.3 : 0.5
                    }))
                };
                
                connectedNodes.forEach(n => {
                    const pos = positioned[n.id];
                    if (pos) nodePositionCache[n.id] = pos;
                });
                
                const graph = new Graph();
                graphData.nodes.forEach(n => {
                    graph.addNode(n.id, {
                        label: n.label || n.id.split('::').pop() || n.id,
                        x: n.x,
                        y: n.y,
                        size: n.size,
                        color: n.color,
                        elementType: n.elementType,
                        degree: n.degree
                    });
                });
                graphData.edges.forEach(e => {
                    if (graph.hasNode(e.source) && graph.hasNode(e.target)) {
                        graph.addEdge(e.source, e.target, {
                            size: e.size || 1,
                            color: 'rgba(100,100,100,0.6)',
                            relType: e.rel_type
                        });
                    }
                });
                
                if (sig) {
                    sig.kill();
                }
                
                let hoveredNode = null;
                const NODE_FADE_COLOR = '#ccc';
                const EDGE_FADE_COLOR = 'rgba(200,200,200,0.3)';
                
                const tooltip = document.getElementById('node-tooltip');
                const tooltipName = document.getElementById('tooltip-name');
                const tooltipType = document.getElementById('tooltip-type');
                const tooltipRelated = document.getElementById('tooltip-related');
                const tooltipRelatedList = document.getElementById('tooltip-related-list');
                
                if (!tooltip || !tooltipName || !tooltipType) {
                    console.error('Tooltip elements not found');
                    return;
                }
                
                sig = new Sigma(graph, container, {
                    renderLabels: true,
                    labelFont: 'Arial',
                    labelSize: 12,
                    labelColor: '#333333',
                    labelRenderedSizeThreshold: 8,
                    defaultNodeColor: '#666',
                    defaultEdgeColor: 'rgba(150,150,150,0.6)',
                    defaultNodeType: 'circle',
                    defaultEdgeType: 'arrow',
                    minCameraRatio: 0.01,
                    maxCameraRatio: 100,
                    hideEdgesOnMove: false,
                    hideLabelsOnMove: false,
                    enableEdgeClickEvents: false,
                    enableNodeClickEvents: true,
                    nodeReducer: (node, data) => {
                        if (hoveredNode) {
                            const isConnected = node === hoveredNode || graph.hasEdge(node, hoveredNode) || graph.hasEdge(hoveredNode, node);
                            return isConnected ? { ...data, zIndex: 1 } : { ...data, zIndex: 0, label: '', color: NODE_FADE_COLOR, hidden: false };
                        }
                        return data;
                    },
                    edgeReducer: (edge, data) => {
                        if (hoveredNode) {
                            const [src, tgt] = graph.extremities(edge);
                            const isConnected = src === hoveredNode || tgt === hoveredNode;
                            return isConnected ? { ...data, color: '#666', size: Math.min(data.size * 2, 4), zIndex: 1 } : { ...data, color: EDGE_FADE_COLOR, hidden: true };
                        }
                        return data;
                    }
                });
                
                window.sig = sig;
                
                sig.getGraph().forEachNode((node) => {
                    const edges = graph.edges().filter(eid => {
                        const [src, tgt] = graph.extremities(eid);
                        return src === node || tgt === node;
                    });
                    graph.setNodeAttribute(node, 'connectionCount', edges.length);
                });
                
                sig.on('enterNode', ({ node }) => {
                    hoveredNode = node;
                    sig.refresh();
                    
                    if (!graph || !tooltip || !tooltipName || !tooltipType) return;
                    
                    const attrs = graph.getNodeAttributes(node);
                    const nodeLabel = (attrs && attrs.label) ? String(attrs.label) : String(node);
                    const nodeType = (attrs && attrs.elementType) ? String(attrs.elementType) : 'unknown';
                    
                    tooltipName.textContent = nodeLabel;
                    tooltipType.textContent = nodeType;
                    tooltipType.className = 'tooltip-type tooltip-type-' + nodeType.toLowerCase();
                    
                    const edges = graph.edges();
                    const connectedEdges = edges.filter(eid => {
                        const [src, tgt] = graph.extremities(eid);
                        return src === node || tgt === node;
                    });
                    
                    if (tooltipRelatedList) tooltipRelatedList.innerHTML = '';
                    if (connectedEdges.length > 0) {
                        if (tooltipRelated) tooltipRelated.style.display = 'block';
                        connectedEdges.slice(0, 5).forEach(eid => {
                            const [src, tgt] = graph.extremities(eid);
                            const other = src === node ? tgt : src;
                            const otherAttrs = graph.getNodeAttributes(other);
                            const otherLabel = (otherAttrs && otherAttrs.label) ? String(otherAttrs.label) : String(other);
                            const relType = graph.getEdgeAttribute(eid, 'relType') || 'related';
                            const direction = src === node ? '->' : '<-';
                            
                            if (tooltipRelatedList) {
                                const li = document.createElement('li');
                                li.innerHTML = '<span class="rel-arrow">' + direction + '</span><span>' + otherLabel + '</span> <span class="rel-type">(' + relType + ')</span>';
                                tooltipRelatedList.appendChild(li);
                            }
                        });
                    } else {
                        if (tooltipRelated) tooltipRelated.style.display = 'none';
                    }
                    
                    const container = document.getElementById('graph-container');
                    if (!container) return;
                    const containerRect = container.getBoundingClientRect();
                    let tooltipX = mouseX + 15;
                    let tooltipY = mouseY + 15;
                    
                    const viewWidth = window.innerWidth;
                    const viewHeight = window.innerHeight;
                    
                    if (tooltipX + 300 > viewWidth) {
                        tooltipX = mouseX - 315;
                    }
                    if (tooltipY + 200 > viewHeight) {
                        tooltipY = mouseY - 200;
                    }
                    
                    if (tooltipX < 0) tooltipX = 15;
                    if (tooltipY < 0) tooltipY = 15;
                    
                    tooltip.style.left = tooltipX + 'px';
                    tooltip.style.top = tooltipY + 'px';
                    tooltip.classList.add('visible');
                });
                
                sig.on('leaveNode', () => {
                    hoveredNode = null;
                    sig.refresh();
                    if (tooltip) tooltip.classList.remove('visible');
                });
                
                sig.on('clickNode', function(e) {
                    const node = e.node;
                    const attrs = graph.getNodeAttributes(node);
                    const edges = graph.edges();
                    const connectedEdges = edges.filter(eid => {
                        const [src, tgt] = graph.extremities(eid);
                        return src === node || tgt === node;
                    });
                    let edgeInfo = '';
                    connectedEdges.slice(0, 5).forEach(eid => {
                        const [src, tgt] = graph.extremities(eid);
                        const other = src === node ? tgt : src;
                        const otherAttrs = graph.getNodeAttributes(other);
                        const otherLabel = typeof otherAttrs.label === 'string' ? otherAttrs.label : other;
                        edgeInfo += '\n- ' + (src === node ? '-> ' : '<- ') + otherLabel;
                    });
                    alert('Element: ' + attrs.label + '\nType: ' + attrs.elementType + '\nConnections:' + (edgeInfo || '\n(none)') + '\n\nID: ' + node);
                });
            }
            
            function startLayout() {
                if (!sig) return;
                
                const graph = sig.graph;
                const nodes = graph.nodes();
                const edges = graph.edges();
                const nodeCount = nodes.length;
                
                if (nodeCount === 0) return;
                
                const iterations = 150;
                const springLength = nodeCount > 300 ? 60 : 80;
                const springStrength = 0.08;
                const repulsionStrength = nodeCount > 300 ? 300 : 500;
                const damping = 0.9;
                const BarnesHutTheta = 0.6;
                
                const velocities = {};
                const oldDelta = {};
                nodes.forEach(n => { 
                    velocities[n] = { x: 0, y: 0 }; 
                    oldDelta[n] = { x: 0, y: 0 };
                });
                
                const nodeMass = {};
                nodes.forEach(n => {
                    const type = graph.getNodeAttribute(n, 'type');
                    if (type === 'file' || type === 'module') nodeMass[n] = 3;
                    else if (type === 'class' || type === 'struct') nodeMass[n] = 2;
                    else nodeMass[n] = 1;
                });
                
                const nodesArray = nodes.slice();
                const n = nodesArray.length;
                const xPos = nodesArray.map(n => graph.getNodeAttribute(n, 'x'));
                const yPos = nodesArray.map(n => graph.getNodeAttribute(n, 'y'));
                
                function getCenterOfMass(nodeIndex) {
                    let cx = 0, cy = 0, mass = 0;
                    for (let i = 0; i < n; i++) {
                        if (i === nodeIndex) continue;
                        const dx = xPos[i] - xPos[nodeIndex];
                        const dy = yPos[i] - yPos[nodeIndex];
                        const dist = Math.sqrt(dx * dx + dy * dy);
                        if (dist > BarnesHutTheta * 100) continue;
                        const m = nodeMass[nodesArray[i]] || 1;
                        cx += xPos[i] * m;
                        cy += yPos[i] * m;
                        mass += m;
                    }
                    return mass > 0 ? { x: cx / mass, y: cy / mass } : { x: xPos[nodeIndex], y: yPos[nodeIndex] };
                }
                
                for (let iter = 0; iter < iterations; iter++) {
                    for (let i = 0; i < n; i++) {
                        const n1 = nodesArray[i];
                        const n1Type = graph.getNodeAttribute(n1, 'type');
                        let fx = 0, fy = 0;
                        
                        const center = getCenterOfMass(i);
                        const dx = xPos[i] - center.x;
                        const dy = yPos[i] - center.y;
                        const distToCenter = Math.sqrt(dx * dx + dy * dy) || 1;
                        const clusterForce = repulsionStrength * 0.3 / (distToCenter + 1);
                        fx += (dx / distToCenter) * clusterForce;
                        fy += (dy / distToCenter) * clusterForce;
                        
                        for (let j = 0; j < n; j++) {
                            if (i === j) continue;
                            const ddx = xPos[i] - xPos[j];
                            const ddy = yPos[i] - yPos[j];
                            const ddist = Math.sqrt(ddx * ddx + ddy * ddy) || 0.1;
                            
                            if (ddist > BarnesHutTheta * 100) continue;
                            
                            const m1 = nodeMass[n1] || 1;
                            const m2 = nodeMass[nodesArray[j]] || 1;
                            const force = repulsionStrength * m1 * m2 / (ddist * ddist);
                            fx += (ddx / ddist) * force;
                            fy += (ddy / ddist) * force;
                        }
                        
                        edges.forEach(e => {
                            const [src, tgt] = graph.extremities(e);
                            if (src !== n1 && tgt !== n1) return;
                            const other = src === n1 ? tgt : src;
                            let otherIdx = -1;
                            for (let k = 0; k < n; k++) {
                                if (nodesArray[k] === other) { otherIdx = k; break; }
                            }
                            if (otherIdx < 0) return;
                            
                            const ddx = xPos[i] - xPos[otherIdx];
                            const ddy = yPos[i] - yPos[otherIdx];
                            const ddist = Math.sqrt(ddx * ddx + ddy * ddy) || 1;
                            const displacement = ddist - springLength;
                            const force = springStrength * displacement;
                            fx -= (ddx / ddist) * force;
                            fy -= (ddy / ddist) * force;
                        });
                        
                        const d1 = damping;
                        const d2 = 1 - damping;
                        velocities[n1].x = velocities[n1].x * d1 + fx * d2 + oldDelta[n1].x * 0.2;
                        velocities[n1].y = velocities[n1].y * d1 + fy * d2 + oldDelta[n1].y * 0.2;
                        oldDelta[n1].x = velocities[n1].x;
                        oldDelta[n1].y = velocities[n1].y;
                    }
                    
                    for (let i = 0; i < n; i++) {
                        const n1 = nodesArray[i];
                        const maxMove = 10;
                        const vx = Math.max(-maxMove, Math.min(maxMove, velocities[n1].x));
                        const vy = Math.max(-maxMove, Math.min(maxMove, velocities[n1].y));
                        xPos[i] += vx;
                        yPos[i] += vy;
                        graph.setNodeAttribute(n1, 'x', xPos[i]);
                        graph.setNodeAttribute(n1, 'y', yPos[i]);
                    }
                }
                 
                 sig.refresh();
             }
              
            function zoomIn() { 
                if (!sig) return;
                const camera = sig.getCamera();
                if (camera) camera.goTo({ ratio: camera.ratio * 0.7 }); 
            }
            function zoomOut() { 
                if (!sig) return;
                const camera = sig.getCamera();
                if (camera) camera.goTo({ ratio: camera.ratio * 1.3 }); 
            }
            function resetZoom() { 
                if (!sig) return;
                const camera = sig.getCamera();
                if (camera) camera.goTo({ x: 0, y: 0, ratio: 1 }); 
            }
            
            function applyLayout(layoutType) {
                if (!sig) return;
                
                const graph = sig.graph;
                const nodes = graph.nodes();
                const nodeCount = nodes.length;
                
                if (nodeCount === 0) return;
                
                const nodesArray = nodes.slice();
                const n = nodesArray.length;
                
                const centerX = 0;
                const centerY = 0;
                const spread = Math.max(100, Math.sqrt(nodeCount) * 5);
                
                if (layoutType === 'force') {
                    const iterations = 100;
                    const springLength = spread * 0.3;
                    const springStrength = 0.05;
                    const repulsionStrength = spread * spread * 0.1;
                    const damping = 0.85;
                    
                    const velocities = {};
                    nodes.forEach(n => { velocities[n] = { x: 0, y: 0 }; });
                    
                    const xPos = nodesArray.map(n => graph.getNodeAttribute(n, 'x'));
                    const yPos = nodesArray.map(n => graph.getNodeAttribute(n, 'y'));
                    
                    for (let iter = 0; iter < iterations; iter++) {
                        for (let i = 0; i < n; i++) {
                            const n1 = nodesArray[i];
                            let fx = 0, fy = 0;
                            
                            for (let j = 0; j < n; j++) {
                                if (i === j) continue;
                                const dx = xPos[i] - xPos[j];
                                const dy = yPos[i] - yPos[j];
                                const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                                const force = repulsionStrength / (dist * dist);
                                fx += (dx / dist) * force;
                                fy += (dy / dist) * force;
                            }
                            
                            graph.forEachEdge(n1, (e) => {
                                const [src, tgt] = graph.extremities(e);
                                const other = src === n1 ? tgt : src;
                                let otherIdx = -1;
                                for (let k = 0; k < n; k++) {
                                    if (nodesArray[k] === other) { otherIdx = k; break; }
                                }
                                if (otherIdx < 0) return;
                                
                                const dx = xPos[i] - xPos[otherIdx];
                                const dy = yPos[i] - yPos[otherIdx];
                                const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                                const displacement = dist - springLength;
                                const force = springStrength * displacement;
                                fx -= (dx / dist) * force;
                                fy -= (dy / dist) * force;
                            });
                            
                            velocities[n1].x = velocities[n1].x * damping + fx * (1 - damping);
                            velocities[n1].y = velocities[n1].y * damping + fy * (1 - damping);
                        }
                        
                        for (let i = 0; i < n; i++) {
                            const n1 = nodesArray[i];
                            const maxMove = spread * 0.2;
                            const vx = Math.max(-maxMove, Math.min(maxMove, velocities[n1].x));
                            const vy = Math.max(-maxMove, Math.min(maxMove, velocities[n1].y));
                            xPos[i] += vx;
                            yPos[i] += vy;
                            graph.setNodeAttribute(n1, 'x', xPos[i]);
                            graph.setNodeAttribute(n1, 'y', yPos[i]);
                        }
                    }
                } else if (layoutType === 'circular') {
                    const angleStep = (2 * Math.PI) / n;
                    const radius = spread;
                    nodesArray.forEach((node, i) => {
                        const angle = i * angleStep;
                        const x = centerX + radius * Math.cos(angle);
                        const y = centerY + radius * Math.sin(angle);
                        graph.setNodeAttribute(node, 'x', x);
                        graph.setNodeAttribute(node, 'y', y);
                    });
                } else if (layoutType === 'grid') {
                    const cols = Math.ceil(Math.sqrt(n));
                    const cellSize = spread * 2 / cols;
                    nodesArray.forEach((node, i) => {
                        const row = Math.floor(i / cols);
                        const col = i % cols;
                        const x = centerX - (cols * cellSize) / 2 + col * cellSize;
                        const y = centerY - (Math.ceil(n / cols) * cellSize) / 2 + row * cellSize;
                        graph.setNodeAttribute(node, 'x', x);
                        graph.setNodeAttribute(node, 'y', y);
                    });
                } else if (layoutType === 'hierarchical') {
                    const parentMap = {};
                    const childMap = {};
                    graph.forEachEdge((e, attrs, src, tgt) => {
                        if (!childMap[src]) childMap[src] = [];
                        childMap[src].push(tgt);
                        parentMap[tgt] = src;
                    });
                    
                    const rootNodes = nodesArray.filter(n => !parentMap[n]);
                    const positioned = {};
                    const getDepth = (nodeId, depth = 0) => {
                        const children = childMap[nodeId] || [];
                        if (children.length === 0) return depth;
                        return Math.max(...children.map(c => getDepth(c, depth + 1)));
                    };
                    
                    const maxDepth = Math.max(...rootNodes.map(n => getDepth(n)));
                    const levelHeight = spread * 1.5 / Math.max(maxDepth, 1);
                    
                    const positionLevel = (nodeId, depth, levelNodes) => {
                        if (positioned[nodeId]) return;
                        const levelWidth = spread * 2 / (levelNodes.length + 1);
                        const levelIndex = levelNodes.indexOf(nodeId);
                        const x = centerX - spread + (levelIndex + 1) * levelWidth;
                        const y = centerY - spread * 0.7 + depth * levelHeight;
                        positioned[nodeId] = { x, y };
                        graph.setNodeAttribute(nodeId, 'x', x);
                        graph.setNodeAttribute(nodeId, 'y', y);
                        
                        const children = childMap[nodeId] || [];
                        children.forEach(c => positionLevel(c, depth + 1, childMap[nodeId] || []));
                    };
                    
                    rootNodes.forEach((n, i) => {
                        const angle = (2 * Math.PI * i) / Math.max(rootNodes.length, 1);
                        const x = centerX + spread * 0.5 * Math.cos(angle);
                        const y = centerY + spread * 0.5 * Math.sin(angle);
                        positioned[n] = { x, y };
                        graph.setNodeAttribute(n, 'x', x);
                        graph.setNodeAttribute(n, 'y', y);
                        const children = childMap[n] || [];
                        children.forEach(c => positionLevel(c, 1, children));
                    });
                    
                    nodesArray.forEach(n => {
                        if (!positioned[n]) {
                            positioned[n] = { x: centerX + (Math.random() - 0.5) * spread, y: centerY + (Math.random() - 0.5) * spread };
                            graph.setNodeAttribute(n, 'x', positioned[n].x);
                            graph.setNodeAttribute(n, 'y', positioned[n].y);
                        }
                    });
                } else {
                    nodesArray.forEach((node, i) => {
                        const x = centerX + (Math.random() - 0.5) * spread * 2;
                        const y = centerY + (Math.random() - 0.5) * spread * 2;
                        graph.setNodeAttribute(node, 'x', x);
                        graph.setNodeAttribute(node, 'y', y);
                    });
                }
                
                sig.refresh();
            }
             
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

    let mut file_paths: std::collections::HashSet<_> =
        elements.iter().map(|e| e.file_path.clone()).collect();
    let mut files: Vec<_> = file_paths
        .drain()
        .map(|fp| {
            let count = elements.iter().filter(|e| e.file_path == fp).count();
            (fp, count)
        })
        .collect();
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
            let mut nodes: Vec<GraphNode> = elements
                .iter()
                .map(|e| GraphNode {
                    id: e.qualified_name.clone(),
                    label: e.name.clone(),
                    element_type: e.element_type.clone(),
                    file_path: e.file_path.clone(),
                })
                .collect();

            let mut file_map: std::collections::HashMap<String, Vec<String>> =
                std::collections::HashMap::new();
            for element in &elements {
                file_map
                    .entry(element.file_path.clone())
                    .or_default()
                    .push(element.qualified_name.clone());
            }

            for (file_path, _element_ids) in &file_map {
                let file_node = GraphNode {
                    id: format!("file::{}", file_path),
                    label: file_path.split('/').last().unwrap_or(file_path).to_string(),
                    element_type: "file".to_string(),
                    file_path: file_path.clone(),
                };
                nodes.push(file_node);
            }

            let node_ids: std::collections::HashSet<_> =
                nodes.iter().map(|n| n.id.clone()).collect();
            let mut existing_edges: std::collections::HashSet<(String, String)> =
                std::collections::HashSet::new();
            let mut edges: Vec<GraphEdge> = relationships
                .iter()
                .filter(|r| {
                    node_ids.contains(&r.source_qualified) && node_ids.contains(&r.target_qualified)
                })
                .filter(|r| !r.rel_type.starts_with("contains"))
                .map(|r| {
                    existing_edges.insert((r.source_qualified.clone(), r.target_qualified.clone()));
                    GraphEdge {
                        source: r.source_qualified.clone(),
                        target: r.target_qualified.clone(),
                        rel_type: r.rel_type.clone(),
                    }
                })
                .collect();

            for (file_path, element_ids) in &file_map {
                let file_node_id = format!("file::{}", file_path);
                for element_id in element_ids {
                    let edge_key = (file_node_id.clone(), element_id.clone());
                    if !existing_edges.contains(&edge_key) {
                        existing_edges.insert(edge_key);
                        edges.push(GraphEdge {
                            source: file_node_id.clone(),
                            target: element_id.clone(),
                            rel_type: "contains".to_string(),
                        });
                    }
                }
            }

            ApiResponse {
                success: true,
                data: Some(GraphData {
                    nodes,
                    edges,
                    filtered: None,
                }),
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
            let node_ids: std::collections::HashSet<_> =
                nodes.iter().map(|n| n.id.clone()).collect();
            let edges: Vec<GraphEdge> = relationships
                .iter()
                .filter(|r| {
                    node_ids.contains(&r.source_qualified) && node_ids.contains(&r.target_qualified)
                })
                .map(|r| GraphEdge {
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                })
                .collect();
            ApiResponse {
                success: true,
                data: Some(GraphData {
                    nodes,
                    edges,
                    filtered: None,
                }),
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

#[derive(Deserialize)]
pub struct PathSwitchRequest {
    pub path: Option<String>,
    pub github_url: Option<String>,
}

#[derive(Serialize)]
pub struct PathSwitchResponse {
    pub is_directory: bool,
    pub has_database: bool,
    pub needs_indexing: bool,
    pub is_github: bool,
    pub project_path: String,
}

#[allow(dead_code)]
pub async fn api_switch_path(
    State(state): State<AppState>,
    Json(req): Json<PathSwitchRequest>,
) -> impl IntoResponse {
    let project_path: String;

    if let Some(ref github_url) = req.github_url {
        let url = github_url.trim();

        if !url.contains("github.com") {
            return ApiResponse::<PathSwitchResponse> {
                success: false,
                data: None,
                error: Some("Only GitHub URLs are supported".to_string()),
            };
        }

        let repo_name = url
            .split('/')
            .filter(|s| !s.is_empty())
            .last()
            .unwrap_or("repo")
            .replace(".git", "");

        let home_dir = dirs::home_dir().unwrap_or_else(|| std::path::PathBuf::from("."));
        let clone_base = home_dir.join(".leankg").join("clones");

        if let Err(e) = std::fs::create_dir_all(&clone_base) {
            return ApiResponse::<PathSwitchResponse> {
                success: false,
                data: None,
                error: Some(format!("Failed to create clones directory: {}", e)),
            };
        }

        let clone_path = clone_base.join(&repo_name);

        if !clone_path.exists() {
            let output = Command::new("git")
                .args(["clone", url, clone_path.to_str().unwrap_or(&repo_name)])
                .output();

            match output {
                Ok(output) if !output.status.success() => {
                    let err = String::from_utf8_lossy(&output.stderr);
                    return ApiResponse::<PathSwitchResponse> {
                        success: false,
                        data: None,
                        error: Some(format!("Git clone failed: {}", err)),
                    };
                }
                Err(e) => {
                    return ApiResponse::<PathSwitchResponse> {
                        success: false,
                        data: None,
                        error: Some(format!("Failed to execute git: {}", e)),
                    };
                }
                _ => {}
            }
        }

        project_path = clone_path.to_string_lossy().to_string();
    } else if let Some(ref path) = req.path {
        project_path = path.trim().to_string();
    } else {
        return ApiResponse::<PathSwitchResponse> {
            success: false,
            data: None,
            error: Some("Either path or github_url must be provided".to_string()),
        };
    }

    let path_obj = std::path::Path::new(&project_path);

    if !path_obj.exists() {
        return ApiResponse::<PathSwitchResponse> {
            success: false,
            data: None,
            error: Some("Directory not found. Please check the path and try again.".to_string()),
        };
    }

    if !path_obj.is_dir() {
        return ApiResponse::<PathSwitchResponse> {
            success: false,
            data: None,
            error: Some("Path is not a directory".to_string()),
        };
    }

    let absolute_path = path_obj.to_string_lossy().to_string();
    let db_path = path_obj.join(".leankg");

    if let Err(e) = std::fs::create_dir_all(&db_path) {
        return ApiResponse::<PathSwitchResponse> {
            success: false,
            data: None,
            error: Some(format!("Failed to create .leankg directory: {}", e)),
        };
    }

    let has_database = db_path.exists();

    let new_state = state.clone();
    let absolute_path_clone = absolute_path.clone();
    let project_path_for_response = absolute_path.clone();
    let path_obj_clone = path_obj.to_path_buf();

    let indexing_state = new_state.indexing_state.clone();
    let rt = tokio::runtime::Handle::current();

    std::thread::spawn(move || {
        let _enter = rt.enter();

        let init_err = {
            let result = rt.block_on(new_state.switch_project(path_obj_clone.clone()));
            result.err().map(|e| e.to_string())
        };
        if let Some(err_msg) = init_err {
            tracing::error!("Failed to switch project: {}", err_msg);
            rt.block_on(new_state.set_indexing_error(err_msg));
            return;
        }

        let files = crate::indexer::find_files_sync(&absolute_path_clone);
        let files = match files {
            Ok(f) => f,
            Err(e) => {
                let err_msg = e.to_string();
                tracing::error!("Failed to find files: {}", err_msg);
                rt.block_on(new_state.set_indexing_error(err_msg));
                return;
            }
        };

        rt.block_on(new_state.set_indexing_started(files.len()));

        let graph = match rt.block_on(new_state.get_graph_engine()) {
            Ok(g) => g,
            Err(e) => {
                let err_msg = e.to_string();
                tracing::error!("Failed to get graph engine: {}", err_msg);
                rt.block_on(new_state.set_indexing_error(err_msg));
                return;
            }
        };

        let mut parser_manager = crate::indexer::ParserManager::new();
        if let Err(e) = parser_manager.init_parsers() {
            let err_msg = e.to_string();
            tracing::error!("Failed to init parsers: {}", err_msg);
            rt.block_on(new_state.set_indexing_error(err_msg));
            return;
        }

        let total = files.len();

        for (idx, file_path) in files.iter().enumerate() {
            {
                let mut state_guard = rt.block_on(indexing_state.write());
                state_guard.indexed_files = idx + 1;
                state_guard.current_file = file_path.clone();
                if total > 0 {
                    state_guard.progress_percent = ((idx + 1) * 100) / total;
                }
            }

            if let Err(e) = crate::indexer::index_file_sync(&graph, &mut parser_manager, file_path)
            {
                tracing::warn!("Failed to index {}: {}", file_path, e);
            }
        }

        if let Err(e) = graph.resolve_call_edges() {
            tracing::warn!("Failed to resolve call edges: {}", e);
        }

        rt.block_on(new_state.set_indexing_complete());
        tracing::info!("Indexing complete for {}", absolute_path_clone);
    });

    ApiResponse::<PathSwitchResponse> {
        success: true,
        data: Some(PathSwitchResponse {
            is_directory: true,
            has_database,
            needs_indexing: true,
            is_github: req.github_url.is_some(),
            project_path: project_path_for_response,
        }),
        error: None,
    }
}

#[derive(Serialize)]
pub struct IndexStatusResponse {
    pub is_indexing: bool,
    pub progress_percent: usize,
    pub current_file: String,
    pub total_files: usize,
    pub indexed_files: usize,
}

#[allow(dead_code)]
pub async fn api_index_status(State(state): State<AppState>) -> impl IntoResponse {
    let indexing_state = state.indexing_state.read().await;

    ApiResponse {
        success: true,
        data: Some(IndexStatusResponse {
            is_indexing: indexing_state.is_indexing,
            progress_percent: indexing_state.progress_percent,
            current_file: indexing_state.current_file.clone(),
            total_files: indexing_state.total_files,
            indexed_files: indexing_state.indexed_files,
        }),
        error: indexing_state.error.clone(),
    }
}

#[derive(Deserialize)]
pub struct GitHubCloneRequest {
    pub url: String,
}

#[derive(Serialize)]
pub struct GitHubCloneResponse {
    pub clone_path: String,
    pub is_indexing: bool,
}

pub async fn project_selector(State(state): State<AppState>) -> axum::response::Html<String> {
    let indexing_state = state.indexing_state.read().await;

    let progress_html = if indexing_state.is_indexing {
        format!(
            r#"
            <div class="card" id="progress-card">
                <h2>Indexing in Progress</h2>
                <div style="margin: 20px 0;">
                    <div style="background: #e0e0e0; border-radius: 8px; height: 24px; overflow: hidden;">
                        <div id="progress-bar" style="background: #0066cc; height: 100%; width: {}%; transition: width 0.3s;"></div>
                    </div>
                    <p style="margin-top: 10px; color: #666;">
                        Indexing: {} of {} files
                    </p>
                    <p style="color: #888; font-size: 14px;">
                        Current: <code id="current-file">{}</code>
                    </p>
                </div>
            </div>
            <script>
                async function pollStatus() {{
                    try {{
                        const res = await fetch('/api/index/status');
                        const data = await res.json();
                        if (data.success && data.data) {{
                            const progressBar = document.getElementById('progress-bar');
                            const currentFile = document.getElementById('current-file');
                            if (progressBar) progressBar.style.width = data.data.progress_percent + '%';
                            if (currentFile) currentFile.textContent = data.data.current_file || 'Processing...';
                            
                            if (!data.data.is_indexing) {{
                                if (data.data.error) {{
                                    document.getElementById('progress-card').innerHTML = 
                                        '<div class="error"><p>Indexing failed: ' + data.data.error + '</p><button onclick="location.reload()">Try Again</button></div>';
                                }} else {{
                                    window.location.href = '/';
                                }}
                            }} else {{
                                setTimeout(pollStatus, 2000);
                            }}
                        }}
                    }} catch (e) {{
                        console.error('Poll error:', e);
                        setTimeout(pollStatus, 5000);
                    }}
                }}
                pollStatus();
            </script>"#,
            indexing_state.progress_percent,
            indexing_state.indexed_files,
            indexing_state.total_files,
            indexing_state.current_file
        )
    } else {
        String::new()
    };

    let error_html = if let Some(ref error) = indexing_state.error {
        format!(r#"<div class="error"><p>{}</p></div>"#, error)
    } else {
        String::new()
    };

    let content = format!(
        r#"
        <div class="card">
            <h2>Welcome to LeanKG</h2>
            <p style="color: #666; margin-bottom: 20px;">
                Enter a local path or GitHub URL to start analyzing a codebase.
            </p>
            <form id="project-form" onsubmit="handleSubmit(event)">
                <div class="form-group">
                    <label for="path-input">Project Path or GitHub URL</label>
                    <input 
                        type="text" 
                        id="path-input" 
                        name="path" 
                        placeholder="e.g., /Users/name/project or https://github.com/user/repo"
                        required
                        style="font-size: 16px; padding: 12px;"
                    >
                </div>
                <button type="submit" id="submit-btn" style="font-size: 16px; padding: 12px 24px;">
                    Load Project
                </button>
            </form>
            <div id="message" style="margin-top: 15px;"></div>
        </div>
        {}
        {}
        <div class="card">
            <h3>Quick Examples</h3>
            <p style="color: #666; margin-bottom: 10px;">Try with a public GitHub repository:</p>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                <button onclick="document.getElementById('path-input').value='https://github.com/FreePeak/LeanKG'" style="background: #666;">
                    LeanKG
                </button>
                <button onclick="document.getElementById('path-input').value='https://github.com/tokio-rs/tokio'" style="background: #666;">
                    Tokio
                </button>
                <button onclick="document.getElementById('path-input').value='https://github.com/serde-rs/serde'" style="background: #666;">
                    Serde
                </button>
            </div>
        </div>
        <style>
            #project-form {{
                margin-bottom: 20px;
            }}
            #message {{
                margin-top: 15px;
            }}
            #message .error {{
                background: #ffebee;
                color: #c62828;
                padding: 15px;
                border-radius: 6px;
            }}
            #message .success {{
                background: #e8f5e9;
                color: #2e7d32;
                padding: 15px;
                border-radius: 6px;
            }}
        </style>
        <script>
            async function handleSubmit(e) {{
                e.preventDefault();
                const input = document.getElementById('path-input');
                const btn = document.getElementById('submit-btn');
                const message = document.getElementById('message');
                const value = input.value.trim();
                
                if (!value) return;
                
                btn.disabled = true;
                btn.textContent = 'Loading...';
                message.innerHTML = '';
                
                try {{
                    let body;
                    if (value.startsWith('http://') || value.startsWith('https://')) {{
                        if (!value.includes('github.com')) {{
                            message.innerHTML = '<div class="error">Only GitHub URLs are supported currently.</div>';
                            btn.disabled = false;
                            btn.textContent = 'Load Project';
                            return;
                        }}
                        body = JSON.stringify({{ github_url: value }});
                    }} else {{
                        body = JSON.stringify({{ path: value }});
                    }}
                    
                    const response = await fetch('/api/project/switch', {{
                        method: 'POST',
                        headers: {{ 'Content-Type': 'application/json' }},
                        body: body
                    }});
                    
                    const data = await response.json();
                    
                    if (data.success) {{
                        message.innerHTML = '<div class="success">Project loaded! Starting indexing...</div>';
                        setTimeout(() => {{ window.location.href = '/'; }}, 500);
                    }} else {{
                        message.innerHTML = '<div class="error">' + (data.error || 'Failed to load project') + '</div>';
                        btn.disabled = false;
                        btn.textContent = 'Load Project';
                    }}
                }} catch (err) {{
                    message.innerHTML = '<div class="error">Error: ' + err.message + '</div>';
                    btn.disabled = false;
                    btn.textContent = 'Load Project';
                }}
            }}
        </script>"#,
        progress_html, error_html
    );

    axum::response::Html(base_html("Welcome", &content))
}

#[allow(dead_code)]
pub async fn api_github_clone(
    State(_state): State<AppState>,
    Json(req): Json<GitHubCloneRequest>,
) -> impl IntoResponse {
    let url = req.url.trim();

    if !url.contains("github.com") {
        return ApiResponse::<GitHubCloneResponse> {
            success: false,
            data: None,
            error: Some("Only GitHub URLs are supported".to_string()),
        };
    }

    let repo_name = url
        .split('/')
        .filter(|s| !s.is_empty())
        .last()
        .unwrap_or("repo")
        .replace(".git", "");

    let home_dir = dirs::home_dir().unwrap_or_else(|| std::path::PathBuf::from("."));
    let clone_base = home_dir.join(".leankg").join("clones");

    if let Err(e) = std::fs::create_dir_all(&clone_base) {
        return ApiResponse::<GitHubCloneResponse> {
            success: false,
            data: None,
            error: Some(format!("Failed to create clones directory: {}", e)),
        };
    }

    let clone_path = clone_base.join(&repo_name);

    if clone_path.exists() {
        return ApiResponse::<GitHubCloneResponse> {
            success: true,
            data: Some(GitHubCloneResponse {
                clone_path: clone_path.to_string_lossy().to_string(),
                is_indexing: false,
            }),
            error: None,
        };
    }

    let output = Command::new("git")
        .args(["clone", url, clone_path.to_str().unwrap_or(&repo_name)])
        .output();

    match output {
        Ok(output) if output.status.success() => ApiResponse::<GitHubCloneResponse> {
            success: true,
            data: Some(GitHubCloneResponse {
                clone_path: clone_path.to_string_lossy().to_string(),
                is_indexing: true,
            }),
            error: None,
        },
        Ok(output) => {
            let err = String::from_utf8_lossy(&output.stderr);
            ApiResponse::<GitHubCloneResponse> {
                success: false,
                data: None,
                error: Some(format!("Git clone failed: {}", err)),
            }
        }
        Err(e) => ApiResponse::<GitHubCloneResponse> {
            success: false,
            data: None,
            error: Some(format!("Failed to execute git: {}", e)),
        },
    }
}

use crate::db::models::{CodeElement, Relationship};
use std::collections::HashMap;

pub struct HtmlExporter;

pub struct SvgExporter;

pub struct GraphMlExporter;

pub struct Neo4jExporter;

impl HtmlExporter {
    pub fn new() -> Self {
        Self
    }

    pub fn generate_html(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> String {
        let nodes_json = self.generate_nodes_json(elements);
        let edges_json = self.generate_edges_json(relationships);
        let legend = self.generate_legend();
        let search_html = self.generate_search_html();
        let colors_json = self.generate_color_config();

        format!(
            r#"<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>LeanKG Graph Visualization</title>
    <script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
    <style>
        * {{
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }}
        body {{
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: #1a1a2e;
            color: #eee;
        }}
        #header {{
            background: #16213e;
            padding: 15px 20px;
            border-bottom: 1px solid #0f3460;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }}
        #header h1 {{
            font-size: 1.5rem;
            color: #e94560;
        }}
        #search-container {{
            display: flex;
            gap: 10px;
            align-items: center;
        }}
        #search-input {{
            padding: 8px 12px;
            border: 1px solid #0f3460;
            border-radius: 4px;
            background: #1a1a2e;
            color: #eee;
            width: 250px;
        }}
        #search-input:focus {{
            outline: none;
            border-color: #e94560;
        }}
        #filter-select {{
            padding: 8px 12px;
            border: 1px solid #0f3460;
            border-radius: 4px;
            background: #1a1a2e;
            color: #eee;
        }}
        #mynetwork {{
            width: 100%;
            height: calc(100vh - 130px);
            background: #1a1a2e;
        }}
        #legend {{
            position: absolute;
            bottom: 20px;
            left: 20px;
            background: rgba(22, 33, 62, 0.95);
            padding: 15px;
            border-radius: 8px;
            border: 1px solid #0f3460;
            z-index: 100;
        }}
        #legend h3 {{
            margin-bottom: 10px;
            color: #e94560;
            font-size: 0.9rem;
        }}
        .legend-item {{
            display: flex;
            align-items: center;
            margin: 5px 0;
            font-size: 0.85rem;
        }}
        .legend-color {{
            width: 16px;
            height: 16px;
            border-radius: 3px;
            margin-right: 8px;
        }}
        #stats {{
            position: absolute;
            top: 70px;
            right: 20px;
            background: rgba(22, 33, 62, 0.95);
            padding: 10px 15px;
            border-radius: 8px;
            border: 1px solid #0f3460;
            font-size: 0.85rem;
            z-index: 100;
        }}
        #tooltip {{
            position: absolute;
            display: none;
            background: rgba(22, 33, 62, 0.98);
            padding: 12px;
            border-radius: 6px;
            border: 1px solid #e94560;
            max-width: 400px;
            z-index: 1000;
            font-size: 0.85rem;
        }}
        #tooltip h4 {{
            color: #e94560;
            margin-bottom: 8px;
        }}
        #tooltip p {{
            margin: 4px 0;
            color: #ccc;
        }}
        #tooltip code {{
            background: #0f3460;
            padding: 2px 6px;
            border-radius: 3px;
            font-size: 0.8rem;
        }}
    </style>
</head>
<body>
    <div id="header">
        <h1>LeanKG Graph</h1>
        {search_html}
    </div>
    <div id="mynetwork"></div>
    <div id="legend">
        <h3>Node Types</h3>
        {legend}
    </div>
    <div id="stats">
        <div>Nodes: <span id="node-count">0</span></div>
        <div>Edges: <span id="edge-count">0</span></div>
    </div>
    <div id="tooltip"></div>

    <script type="text/javascript">
        const nodes = new vis.DataSet({nodes_json});
        const edges = new vis.DataSet({edges_json});
        const colors = {colors_json};

        const container = document.getElementById('mynetwork');
        const data = {{ nodes, edges }};
        const options = {{
            nodes: {{
                shape: 'dot',
                size: 15,
                font: {{ color: '#eee', size: 12, face: 'Helvetica' }},
                borderWidth: 2,
                shadow: true
            }},
            edges: {{
                width: 1.5,
                color: {{ color: '#555', highlight: '#e94560' }},
                arrows: {{ to: {{ enabled: true, scaleFactor: 0.5 }} }},
                font: {{ color: '#888', size: 10, align: 'middle' }},
                smooth: {{ type: 'continuous' }}
            }},
            physics: {{
                forceAtlas2Based: {{
                    gravitationalConstant: -50,
                    centralGravity: 0.01,
                    springLength: 150,
                    springConstant: 0.08,
                    damping: 0.4
                }},
                solver: 'forceAtlas2Based',
                stabilization: {{ iterations: 100 }}
            }},
            interaction: {{
                hover: true,
                tooltipDelay: 200,
                zoomView: true,
                dragView: true
            }},
            groups: {{
                function: {{ color: {{ background: '#4ecdc4', border: '#45b7aa' }} }},
                struct: {{ color: {{ background: '#ff6b6b', border: '#ee5a5a' }} }},
                class: {{ color: {{ background: '#ffd93d', border: '#f0c929' }} }},
                module: {{ color: {{ background: '#6bcb77', border: '#5ab868' }} }},
                file: {{ color: {{ background: '#4d96ff', border: '#3d86ef' }} }},
                interface: {{ color: {{ background: '#c9b1ff', border: '#b8a1ff' }} }},
                enum: {{ color: {{ background: '#ff9f43', border: '#f08c30' }} }},
                trait: {{ color: {{ background: '#f8b500', border: '#e0a500' }} }},
                type: {{ color: {{ background: '#a8e6cf', border: '#95d5ba' }} }},
                default: {{ color: {{ background: '#888', border: '#666' }} }}
            }}
        }};

        const network = new vis.Network(container, data, options);

        network.on("stabilizationIterationsDone", function() {{
            network.setOptions({{ physics: {{ enabled: false }} }});
        }});

        network.on("hoverNode", function(params) {{
            const node = nodes.get(params.node);
            const tooltip = document.getElementById('tooltip');
            tooltip.innerHTML = `
                <h4>${{node.label}}</h4>
                <p><strong>Type:</strong> <code>${{node.group}}</code></p>
                <p><strong>File:</strong> <code>${{node.file}}</code></p>
                <p><strong>Lines:</strong> ${{node.lines ? node.lines[0] : '?'}} - ${{node.lines ? node.lines[1] : '?'}}</p>
                ${{node.cluster ? `<p><strong>Cluster:</strong> ${{node.cluster}}</p>` : ''}}
            `;
            tooltip.style.display = 'block';
            tooltip.style.left = (params.event.center.x + 10) + 'px';
            tooltip.style.top = (params.event.center.y + 10) + 'px';
        }});

        network.on("blurNode", function() {{
            document.getElementById('tooltip').style.display = 'none';
        }});

        document.getElementById('node-count').textContent = nodes.length;
        document.getElementById('edge-count').textContent = edges.length;

        const searchInput = document.getElementById('search-input');
        const filterSelect = document.getElementById('filter-select');

        function filterNodes() {{
            const query = searchInput.value.toLowerCase();
            const filter = filterSelect.value;

            nodes.forEach(function(node) {{
                const matchesQuery = node.label.toLowerCase().includes(query);
                const matchesFilter = filter === 'all' || node.group === filter;
                nodes.update({{ id: node.id, hidden: !(matchesQuery && matchesFilter) }});
            }});
        }}

        searchInput.addEventListener('input', filterNodes);
        filterSelect.addEventListener('change', filterNodes);
    </script>
</body>
</html>"#,
            nodes_json = nodes_json,
            edges_json = edges_json,
            legend = legend,
            search_html = search_html,
            colors_json = colors_json
        )
    }

    fn generate_nodes_json(&self, elements: &[CodeElement]) -> String {
        let nodes: Vec<serde_json::Value> = elements
            .iter()
            .map(|e| {
                let label = e.name.clone();
                let group = e.element_type.clone();
                let cluster = e.cluster_label.clone();
                let file = e.file_path.clone();
                let lines = vec![e.line_start as i64, e.line_end as i64];

                serde_json::json!({
                    "id": e.qualified_name,
                    "label": label,
                    "group": group,
                    "file": file,
                    "lines": lines,
                    "cluster": cluster,
                    "title": format!("{}\nType: {}\nFile: {} (lines {}-{})",
                        e.qualified_name, e.element_type, e.file_path, e.line_start, e.line_end)
                })
            })
            .collect();

        serde_json::to_string(&nodes).unwrap_or_else(|_| "[]".to_string())
    }

    fn generate_edges_json(&self, relationships: &[Relationship]) -> String {
        let edges: Vec<serde_json::Value> = relationships
            .iter()
            .map(|r| {
                serde_json::json!({
                    "from": r.source_qualified,
                    "to": r.target_qualified,
                    "label": r.rel_type,
                    "arrows": "to",
                    "title": format!("Confidence: {:.2}", r.confidence)
                })
            })
            .collect();

        serde_json::to_string(&edges).unwrap_or_else(|_| "[]".to_string())
    }

    fn generate_legend(&self) -> String {
        let types = [
            ("function", "#4ecdc4"),
            ("struct", "#ff6b6b"),
            ("class", "#ffd93d"),
            ("module", "#6bcb77"),
            ("file", "#4d96ff"),
            ("interface", "#c9b1ff"),
            ("enum", "#ff9f43"),
            ("trait", "#f8b500"),
            ("type", "#a8e6cf"),
        ];

        types
            .iter()
            .map(|(name, color)| {
                format!(
                    r#"<div class="legend-item"><div class="legend-color" style="background:{};"></div>{}</div>"#,
                    color, name
                )
            })
            .collect::<Vec<_>>()
            .join("\n")
    }

    fn generate_search_html(&self) -> String {
        let element_types = [
            "function",
            "struct",
            "class",
            "module",
            "file",
            "interface",
            "enum",
            "trait",
            "type",
        ];

        let options = element_types
            .iter()
            .map(|t| format!(r#"<option value="{}">{}</option>"#, t, t))
            .collect::<Vec<_>>()
            .join("\n");

        format!(
            r#"<div id="search-container">
                <input type="text" id="search-input" placeholder="Search nodes...">
                <select id="filter-select">
                    <option value="all">All Types</option>
                    {}
                </select>
            </div>"#,
            options
        )
    }

    fn generate_color_config(&self) -> String {
        let colors: std::collections::HashMap<&str, &str> = [
            ("function", "#4ecdc4"),
            ("struct", "#ff6b6b"),
            ("class", "#ffd93d"),
            ("module", "#6bcb77"),
            ("file", "#4d96ff"),
            ("interface", "#c9b1ff"),
            ("enum", "#ff9f43"),
            ("trait", "#f8b500"),
            ("type", "#a8e6cf"),
        ]
        .into_iter()
        .collect();

        serde_json::to_string(&colors).unwrap_or_else(|_| "{{}}".to_string())
    }
}

impl Default for HtmlExporter {
    fn default() -> Self {
        Self::new()
    }
}

impl SvgExporter {
    pub fn new() -> Self {
        Self
    }

    pub fn generate_svg(&self, elements: &[CodeElement], relationships: &[Relationship]) -> String {
        let mut svg = String::new();
        svg.push_str("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n");
        svg.push_str("<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 1200 800\">\n");
        svg.push_str("  <style>\n");
        svg.push_str("    .node { fill: #4ecdc4; stroke: #45b7aa; stroke-width: 2; }\n");
        svg.push_str("    .node-struct { fill: #ff6b6b; stroke: #ee5a5a; }\n");
        svg.push_str("    .node-class { fill: #ffd93d; stroke: #f0c929; }\n");
        svg.push_str("    .node-module { fill: #6bcb77; stroke: #5ab868; }\n");
        svg.push_str("    .node-file { fill: #4d96ff; stroke: #3d86ef; }\n");
        svg.push_str("    .edge { stroke: #555; stroke-width: 1.5; fill: none; marker-end: url(#arrowhead); }\n");
        svg.push_str(
            "    .label { font-family: Helvetica, sans-serif; font-size: 10px; fill: #888; }\n",
        );
        svg.push_str(
            "    .nodelabel { font-family: Helvetica, sans-serif; font-size: 11px; fill: #eee; }\n",
        );
        svg.push_str("    text { font-family: Helvetica, sans-serif; }\n");
        svg.push_str("  </style>\n");
        svg.push_str("  <defs>\n");
        svg.push_str("    <marker id=\"arrowhead\" markerWidth=\"10\" markerHeight=\"7\" refX=\"9\" refY=\"3.5\" orient=\"auto\">\n");
        svg.push_str("      <polygon points=\"0 0, 10 3.5, 0 7\" fill=\"#555\"/>\n");
        svg.push_str("    </marker>\n");
        svg.push_str("  </defs>\n");
        svg.push_str("  <rect width=\"1200\" height=\"800\" fill=\"#1a1a2e\"/>\n");

        let node_positions = self.calculate_node_positions(elements, relationships);
        let node_colors = self.get_node_colors();

        for (i, element) in elements.iter().enumerate() {
            let default_pos = (100.0, 100.0 + i as f64 * 30.0);
            let pos = node_positions
                .get(&element.qualified_name)
                .unwrap_or(&default_pos);
            let short_name = element.name.split("::").last().unwrap_or(&element.name);
            let color_class = self.get_node_color_class(&element.element_type, &node_colors);

            svg.push_str(&format!(
                "  <g class=\"node {4}\">\n    <circle cx=\"{0}\" cy=\"{1}\" r=\"20\"/>\n    <text x=\"{0}\" y=\"{1}\" text-anchor=\"middle\" dominant-baseline=\"middle\" class=\"nodelabel\">{2}</text>\n    <title>{3}</title>\n  </g>\n",
                pos.0, pos.1, short_name, element.qualified_name, color_class
            ));
        }

        for rel in relationships {
            if let (Some(src_pos), Some(tgt_pos)) = (
                node_positions.get(&rel.source_qualified),
                node_positions.get(&rel.target_qualified),
            ) {
                svg.push_str(&format!(
                    "  <line class=\"edge\" x1=\"{0}\" y1=\"{1}\" x2=\"{2}\" y3=\"{3}\"/>\n  <text x=\"{4}\" y=\"{5}\" class=\"label\">{6}</text>\n",
                    src_pos.0,
                    src_pos.1,
                    tgt_pos.0,
                    tgt_pos.1,
                    (src_pos.0 + tgt_pos.0) / 2.0,
                    (src_pos.1 + tgt_pos.1) / 2.0 - 5.0,
                    rel.rel_type
                ));
            }
        }

        svg.push_str("</svg>\n");
        svg
    }

    fn calculate_node_positions(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> std::collections::HashMap<String, (f64, f64)> {
        use std::collections::{HashMap, HashSet};

        let mut positions: HashMap<String, (f64, f64)> = HashMap::new();
        let mut in_degree: HashMap<String, usize> = HashMap::new();

        for element in elements {
            in_degree.insert(element.qualified_name.clone(), 0);
        }

        for rel in relationships {
            if let Some(count) = in_degree.get_mut(&rel.target_qualified) {
                *count += 1;
            }
        }

        let mut roots: Vec<String> = in_degree
            .iter()
            .filter(|(_, &count)| count == 0)
            .map(|(name, _)| name.clone())
            .collect();

        if roots.is_empty() && !elements.is_empty() {
            roots.push(elements[0].qualified_name.clone());
        }

        let mut visited: HashSet<String> = HashSet::new();
        let mut queue: Vec<(String, f64, f64, f64)> = Vec::new();

        for (i, root) in roots.iter().enumerate() {
            queue.push((root.clone(), 100.0 + (i as f64 * 200.0), 100.0, 0.0));
        }

        let mut x_counter = 0;
        while let Some((qn, x, y, level)) = queue.pop() {
            if visited.contains(&qn) || level > 5.0 {
                continue;
            }
            visited.insert(qn.clone());
            positions.insert(qn.clone(), (x, y));

            let children: Vec<String> = relationships
                .iter()
                .filter(|r| r.source_qualified == qn)
                .map(|r| r.target_qualified.clone())
                .collect();

            for child in children {
                if !visited.contains(&child) {
                    x_counter += 1;
                    let child_x = 100.0 + (x_counter as f64 * 100.0) % 1000.0;
                    let child_y = y + 80.0;
                    queue.push((child, child_x, child_y, level + 1.0));
                }
            }
        }

        for (i, element) in elements.iter().enumerate() {
            if !positions.contains_key(&element.qualified_name) {
                positions.insert(
                    element.qualified_name.clone(),
                    (
                        150.0 + (i as f64 * 80.0) % 1000.0,
                        100.0 + (i as f64 * 50.0) % 600.0,
                    ),
                );
            }
        }

        positions
    }

    fn get_node_colors(&self) -> HashMap<String, String> {
        let mut colors: HashMap<String, String> = HashMap::new();
        colors.insert("function".to_string(), "#4ecdc4".to_string());
        colors.insert("struct".to_string(), "#ff6b6b".to_string());
        colors.insert("class".to_string(), "#ffd93d".to_string());
        colors.insert("module".to_string(), "#6bcb77".to_string());
        colors.insert("file".to_string(), "#4d96ff".to_string());
        colors
    }

    fn get_node_color_class(&self, element_type: &str, colors: &HashMap<String, String>) -> String {
        let color = colors.get(element_type).unwrap_or(&"#888".to_string());
        format!("node-{}", element_type)
    }
}

impl Default for SvgExporter {
    fn default() -> Self {
        Self::new()
    }
}

impl GraphMlExporter {
    pub fn new() -> Self {
        Self
    }

    pub fn generate_graphml(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> String {
        let mut graphml = String::new();

        graphml.push_str(
            r#"<?xml version="1.0" encoding="UTF-8"?>
<graphml xmlns="http://graphml.graphdrawing.org/xmlns"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://graphml.graphdrawing.org/xmlns
         http://graphml.graphdrawing.org/xmlns/1.0/graphml.xsd">
"#,
        );

        graphml.push_str(
            "  <key id=\"label\" for=\"node\" attr.name=\"label\" attr.type=\"string\"/>\n",
        );
        graphml.push_str("  <key id=\"element_type\" for=\"node\" attr.name=\"element_type\" attr.type=\"string\"/>\n");
        graphml.push_str(
            "  <key id=\"file_path\" for=\"node\" attr.name=\"file_path\" attr.type=\"string\"/>\n",
        );
        graphml.push_str(
            "  <key id=\"line_start\" for=\"node\" attr.name=\"line_start\" attr.type=\"int\"/>\n",
        );
        graphml.push_str(
            "  <key id=\"line_end\" for=\"node\" attr.name=\"line_end\" attr.type=\"int\"/>\n",
        );
        graphml.push_str(
            "  <key id=\"language\" for=\"node\" attr.name=\"language\" attr.type=\"string\"/>\n",
        );
        graphml.push_str(
            "  <key id=\"cluster\" for=\"node\" attr.name=\"cluster\" attr.type=\"string\"/>\n",
        );
        graphml.push_str(
            "  <key id=\"rel_type\" for=\"edge\" attr.name=\"rel_type\" attr.type=\"string\"/>\n",
        );
        graphml.push_str("  <key id=\"confidence\" for=\"edge\" attr.name=\"confidence\" attr.type=\"double\"/>\n");

        graphml.push_str("  <graph id=\"LeanKG\" edgedefault=\"directed\">\n");

        for element in elements {
            let label = xml_escape(&element.name);
            let element_type = xml_escape(&element.element_type);
            let file_path = xml_escape(&element.file_path);
            let cluster = element
                .cluster_label
                .as_ref()
                .map(|c| xml_escape(c))
                .unwrap_or_default();

            graphml.push_str(&format!(
                "    <node id=\"{}\">\n\
                   <data key=\"label\">{}</data>\n\
                   <data key=\"element_type\">{}</data>\n\
                   <data key=\"file_path\">{}</data>\n\
                   <data key=\"line_start\">{}</data>\n\
                   <data key=\"line_end\">{}</data>\n\
                   <data key=\"language\">{}</data>\n\
                   <data key=\"cluster\">{}</data>\n\
                 </node>\n",
                xml_escape(&element.qualified_name),
                label,
                element_type,
                file_path,
                element.line_start,
                element.line_end,
                xml_escape(&element.language),
                cluster
            ));
        }

        for relationship in relationships {
            graphml.push_str(&format!(
                "    <edge source=\"{}\" target=\"{}\">\n\
                   <data key=\"rel_type\">{}</data>\n\
                   <data key=\"confidence\">{:.2}</data>\n\
                 </edge>\n",
                xml_escape(&relationship.source_qualified),
                xml_escape(&relationship.target_qualified),
                xml_escape(&relationship.rel_type),
                relationship.confidence
            ));
        }

        graphml.push_str("  </graph>\n</graphml>");

        graphml
    }
}

impl Default for GraphMlExporter {
    fn default() -> Self {
        Self::new()
    }
}

impl Neo4jExporter {
    pub fn new() -> Self {
        Self
    }

    pub fn generate_cypher(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> String {
        let mut cypher = String::new();

        cypher.push_str("// Neo4j Cypher Import Script for LeanKG\n");
        cypher.push_str(&format!(
            "// Generated by LeanKG v{}\n",
            env!("CARGO_PKG_VERSION")
        ));
        cypher.push_str("// \n");
        cypher.push_str("// Import instructions:\n");
        cypher.push_str("// 1. Open Neo4j Browser or cypher-shell\n");
        cypher.push_str("// 2. Run this script with :source or COPY FROM\n");
        cypher.push_str("//\n\n");

        cypher.push_str("// Create nodes\n");
        for element in elements {
            let labels = self.get_neo4j_labels(&element.element_type);
            let props = self.generate_node_properties(element);

            cypher.push_str(&format!("CREATE (n:{} {{ {} }});\n", labels, props));
        }

        cypher.push_str("\n// Create relationships\n");
        for relationship in relationships {
            let rel_type = self.sanitize_relationship_type(&relationship.rel_type);
            let source_id = self.sanitize_qualified_name(&relationship.source_qualified);
            let target_id = self.sanitize_qualified_name(&relationship.target_qualified);

            cypher.push_str(&format!(
                "MATCH (src {{qualified_name: '{}'}}), (tgt {{qualified_name: '{}'}}) \
                 CREATE (src)-[r:{} {{confidence: {:.2}}}]->(tgt);\n",
                source_id, target_id, rel_type, relationship.confidence
            ));
        }

        cypher.push_str("\n// Create indexes for better performance\n");
        cypher.push_str("CREATE INDEX IF NOT EXISTS FOR (n:CodeElement) ON (n.qualified_name);\n");
        cypher.push_str("CREATE INDEX IF NOT EXISTS FOR (n:CodeElement) ON (n.element_type);\n");
        cypher.push_str("CREATE INDEX IF NOT EXISTS FOR (n:CodeElement) ON (n.file_path);\n");

        cypher
    }

    fn get_neo4j_labels(&self, element_type: &str) -> String {
        let base_labels = match element_type {
            "function" => "CodeElement:Function",
            "struct" => "CodeElement:Struct",
            "class" => "CodeElement:Class",
            "module" => "CodeElement:Module",
            "file" => "CodeElement:File",
            "interface" => "CodeElement:Interface",
            "enum" => "CodeElement:Enum",
            "trait" => "CodeElement:Trait",
            "type" => "CodeElement:Type",
            "constant" => "CodeElement:Constant",
            "variable" => "CodeElement:Variable",
            "method" => "CodeElement:Method",
            _ => "CodeElement",
        };

        let cluster_label = match element_type {
            "function" => "Function",
            "struct" => "Struct",
            "class" => "Class",
            "module" => "Module",
            "file" => "File",
            "interface" => "Interface",
            "enum" => "Enum",
            "trait" => "Trait",
            "type" => "Type",
            _ => "CodeElement",
        };

        if base_labels.contains(':') {
            format!("{}:{}", base_labels, cluster_label)
        } else {
            format!("CodeElement:{}", cluster_label)
        }
    }

    fn generate_node_properties(&self, element: &CodeElement) -> String {
        let mut props = vec![
            format!(
                "qualified_name: '{}'",
                self.sanitize_qualified_name(&element.qualified_name)
            ),
            format!("name: '{}'", self.sanitize_string(&element.name)),
            format!(
                "element_type: '{}'",
                self.sanitize_string(&element.element_type)
            ),
            format!("file_path: '{}'", self.sanitize_string(&element.file_path)),
            format!("line_start: {}", element.line_start),
            format!("line_end: {}", element.line_end),
            format!("language: '{}'", self.sanitize_string(&element.language)),
        ];

        if let Some(ref parent) = element.parent_qualified {
            props.push(format!(
                "parent_qualified: '{}'",
                self.sanitize_qualified_name(parent)
            ));
        }

        if let Some(ref cluster_id) = element.cluster_id {
            props.push(format!(
                "cluster_id: '{}'",
                self.sanitize_string(cluster_id)
            ));
        }

        if let Some(ref cluster_label) = element.cluster_label {
            props.push(format!(
                "cluster_label: '{}'",
                self.sanitize_string(cluster_label)
            ));
        }

        props.join(", ")
    }

    fn sanitize_qualified_name(&self, name: &str) -> String {
        name.replace('\\', "\\\\")
            .replace('\'', "\\'")
            .replace('"', "\\\"")
    }

    fn sanitize_string(&self, s: &str) -> String {
        s.replace('\\', "\\\\")
            .replace('\'', "\\'")
            .replace('"', "\\\"")
            .replace('\n', "\\n")
            .replace('\r', "\\r")
            .replace('\t', "\\t")
    }

    fn sanitize_relationship_type(&self, rel_type: &str) -> String {
        rel_type.replace('-', "_")
    }
}

impl Default for Neo4jExporter {
    fn default() -> Self {
        Self::new()
    }
}

fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_html_exporter_generates_valid_html() {
        let exporter = HtmlExporter::new();
        let elements = vec![CodeElement {
            qualified_name: "test.rs::func".to_string(),
            element_type: "function".to_string(),
            name: "func".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: Some("TestCluster".to_string()),
            metadata: serde_json::json!({}),
        }];
        let relationships = vec![];

        let html = exporter.generate_html(&elements, &relationships);

        assert!(html.contains("<!DOCTYPE html>"));
        assert!(html.contains("vis-network"));
        assert!(html.contains("test.rs::func"));
        assert!(html.contains("func"));
    }

    #[test]
    fn test_svg_exporter_generates_valid_svg() {
        let exporter = SvgExporter::new();
        let elements = vec![CodeElement {
            qualified_name: "test.rs::func".to_string(),
            element_type: "function".to_string(),
            name: "func".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
        }];
        let relationships = vec![];

        let svg = exporter.generate_svg(&elements, &relationships);

        assert!(svg.contains("<svg"));
        assert!(svg.contains("xmlns=\"http://www.w3.org/2000/svg\""));
        assert!(svg.contains("func"));
    }

    #[test]
    fn test_graphml_exporter_generates_valid_graphml() {
        let exporter = GraphMlExporter::new();
        let elements = vec![CodeElement {
            qualified_name: "test.rs::func".to_string(),
            element_type: "function".to_string(),
            name: "func".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
        }];
        let relationships = vec![];

        let graphml = exporter.generate_graphml(&elements, &relationships);

        assert!(graphml.contains("<graphml"));
        assert!(graphml.contains("<graph id=\"LeanKG\""));
        assert!(graphml.contains("func"));
        assert!(graphml.contains("<node id="));
    }

    #[test]
    fn test_neo4j_exporter_generates_valid_cypher() {
        let exporter = Neo4jExporter::new();
        let elements = vec![CodeElement {
            qualified_name: "test.rs::func".to_string(),
            element_type: "function".to_string(),
            name: "func".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: Some("TestCluster".to_string()),
            metadata: serde_json::json!({}),
        }];
        let relationships = vec![];

        let cypher = exporter.generate_cypher(&elements, &relationships);

        assert!(cypher.contains("CREATE (n:"));
        assert!(cypher.contains("qualified_name:"));
        assert!(cypher.contains("test.rs::func"));
    }

    #[test]
    fn test_xml_escape() {
        assert_eq!(xml_escape("<test>"), "&lt;test&gt;");
        assert_eq!(xml_escape("a & b"), "a &amp; b");
        assert_eq!(xml_escape("\"quote\""), "&quot;quote&quot;");
    }
}

/**
 * LeanKG plugin for OpenCode.ai
 *
 * Auto-injects LeanKG knowledge graph tools into the agent's context.
 * Registers LeanKG as the default grep replacement with mandatory usage rule.
 */

import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const getLeanKGBootstrap = () => `
<LEANKG_BOOTSTRAP>
LeanKG is a lightweight knowledge graph for codebase understanding.

**Auto-Activated Tools:**
- \`mcp_status\` - Check if LeanKG is initialized
- \`mcp_init\` - Initialize LeanKG for a project
- \`mcp_index\` - Index codebase
- \`search_code\` - Search code elements by name/type
- \`find_function\` - Locate function definitions
- \`get_impact_radius\` - Calculate blast radius of changes
- \`get_dependencies\` - Get direct imports of a file
- \`get_dependents\` - Get files depending on target
- \`get_context\` - Get AI-optimized context for a file
- \`get_tested_by\` - Get test coverage info
- \`query_file\` - Find files by name/pattern
- \`get_call_graph\` - Get function call chains
- \`find_large_functions\` - Find oversized functions
- \`get_doc_for_file\` - Get documentation for a file
- \`get_traceability\` - Get full traceability chain
- \`get_code_tree\` - Get codebase structure
- \`get_doc_tree\` - Get documentation tree
- \`get_clusters\` - Get functional clusters
- \`detect_changes\` - Pre-commit risk analysis

**MANDATORY WORKFLOW: LeanKG First, Grep Fallback**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via \`mcp_status\`
2. If LeanKG is not initialized, run \`mcp_init\` first
3. Use LeanKG tools: \`search_code\`, \`find_function\`, \`query_file\`, \`get_impact_radius\`, etc.
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | \`search_code\` or \`find_function\` |
| glob + content search for tests | \`get_tested_by\` |
| Manual dependency tracing | \`get_impact_radius\` or \`get_dependencies\` |
| Reading entire files | \`get_context\` (token-optimized) |

**When user asks about:**
- "What breaks if I change X?" → Use \`get_impact_radius\`
- "Where is X defined?" → Use \`search_code\` or \`find_function\`
- "How does X work?" → Use \`get_context\` or \`get_call_graph\`
- "What tests cover X?" → Use \`get_tested_by\`
</LEANKG_BOOTSTRAP>
`;

export const LeanKGPlugin = async ({ client, directory }) => {
  const skillsDir = path.resolve(__dirname, '../skills');

  return {
    config: async (config) => {
      config.skills = config.skills || {};
      config.skills.paths = config.skills.paths || [];
      if (!config.skills.paths.includes(skillsDir)) {
        config.skills.paths.push(skillsDir);
      }
    },

    'experimental.chat.system.transform': async (_input, output) => {
      (output.system ||= []).push(getLeanKGBootstrap());
    }
  };
};

import { useEffect, useState } from 'react';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { X, Loader2, MousePointerClick, FileCode } from 'lucide-react';
import type { KGNode } from '../lib/graph-adapter';

interface CodeViewerProps {
  selectedNode: string | null;
  graphData: { nodes: KGNode[] } | null;
  onClose: () => void;
}

const customTheme = {
  ...vscDarkPlus,
  'pre[class*="language-"]': {
    ...vscDarkPlus['pre[class*="language-"]'],
    background: '#0a0a10',
    margin: 0,
    padding: '12px 0',
    fontSize: '13px',
    lineHeight: '1.6',
  },
  'code[class*="language-"]': {
    ...vscDarkPlus['code[class*="language-"]'],
    background: 'transparent',
    fontFamily: '"JetBrains Mono", "Fira Code", monospace',
  },
};

export const CodeViewer = ({ selectedNode, graphData, onClose }: CodeViewerProps) => {
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [width, setWidth] = useState(420);
  const [isResizing, setIsResizing] = useState(false);

  const node = graphData?.nodes.find((n) => n.id === selectedNode);
  const props = node?.properties as Record<string, unknown> | undefined;
  const filePath = (props?.filePath || props?.file_path) as string | undefined;
  const startLine = ((props?.startLine || props?.start_line) as number | undefined) ?? 0;
  const endLine = ((props?.endLine || props?.end_line) as number | undefined) ?? startLine;
  
  useEffect(() => {
    if (!isResizing) return;
    const handleMouseMove = (e: MouseEvent) => {
      const newWidth = document.body.clientWidth - e.clientX;
      setWidth(Math.max(300, Math.min(newWidth, 1200)));
    };
    const handleMouseUp = () => setIsResizing(false);
    
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isResizing]);

  useEffect(() => {
    if (!filePath) {
      setContent(null);
      return;
    }

    let cancelled = false;
    setLoading(true);

    // Attempt to fetch from LeanKG API, fallback to text if not configured
    // Note: In real life this depends on the backend providing an /api/file endpoint
    fetch(`/api/file?path=${encodeURIComponent(filePath)}`)
      .then(res => res.json())
      .then(response => {
        if (!cancelled) {
          if (response.success && response.data?.content) {
            setContent(response.data.content);
          } else {
            setContent(`/* Load Error: ${response.error || 'Failed to read file from workspace'} */`);
          }
        }
      })
      .catch((err) => {
        if (!cancelled) setContent(`/* Error fetching file: ${err.message} */`);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [filePath, startLine, endLine]);

  if (!selectedNode || !node) return null;

  return (
    <div 
      className="absolute right-0 top-0 bottom-0 bg-[#0A0F24]/95 shadow-2xl backdrop-blur-md border-l border-slate-800 flex flex-col z-40 animate-slide-in select-none"
      style={{ width }}
    >
      {/* Draggable Resizer Handle */}
      <div 
        className="absolute top-0 bottom-0 left-0 w-1.5 cursor-col-resize hover:bg-cyan-500/50 active:bg-cyan-500 z-50 transition-colors"
        onMouseDown={() => setIsResizing(true)}
      />

      <div className="flex items-center justify-between border-b border-amber-500/20 bg-gradient-to-r from-amber-500/8 to-orange-500/5 px-3 py-2 select-auto">
        <div className="flex items-center gap-2 flex-1 min-w-0">
          <div className="flex items-center gap-1.5 rounded-md border border-amber-500/25 bg-amber-500/15 px-2 py-0.5 shrink-0">
            <MousePointerClick className="h-3 w-3 text-amber-400" />
            <span className="text-[10px] font-semibold tracking-wide text-amber-300 uppercase">Selected</span>
          </div>
          <FileCode className="h-3.5 w-3.5 text-amber-400/70 shrink-0" />
          <span className="truncate font-mono text-xs text-slate-200">
            {filePath?.split('/').pop() || node.label || node.id}
          </span>
        </div>
        <button
          onClick={onClose}
          className="rounded p-1 text-slate-400 transition-colors hover:bg-slate-800 hover:text-slate-200 ml-2 shrink-0"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-auto bg-[#0a0a10] select-auto">
        {loading ? (
          <div className="flex items-center justify-center h-full text-slate-400">
            <Loader2 className="w-5 h-5 animate-spin mr-2" />
            <span className="text-sm">Loading source code...</span>
          </div>
        ) : content ? (
          <SyntaxHighlighter
            language={(() => {
              const ext = (filePath || '').split('.').pop()?.toLowerCase() || '';
              const langMap: Record<string, string> = {
                rs: 'rust', ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx',
                py: 'python', go: 'go', java: 'java', rb: 'ruby', c: 'c', cpp: 'cpp',
                h: 'c', hpp: 'cpp', cs: 'csharp', swift: 'swift', kt: 'kotlin',
                toml: 'toml', yaml: 'yaml', yml: 'yaml', json: 'json', md: 'markdown',
                html: 'html', css: 'css', scss: 'scss', sql: 'sql', sh: 'bash',
                bash: 'bash', zsh: 'bash', dockerfile: 'docker', tf: 'hcl',
              };
              return langMap[ext] || 'typescript';
            })()}
            style={customTheme as any}
            showLineNumbers
            startingLineNumber={1}
            lineNumberStyle={{
              minWidth: '3em',
              paddingRight: '1em',
              color: '#5a5a70',
              textAlign: 'right',
              userSelect: 'none',
            }}
            lineProps={(lineNumber) => {
              const isHighlighted = lineNumber >= startLine && lineNumber <= endLine;
              return {
                style: {
                  display: 'block',
                  backgroundColor: isHighlighted ? 'rgba(6, 182, 212, 0.14)' : 'transparent',
                  borderLeft: isHighlighted ? '3px solid #06b6d4' : '3px solid transparent',
                  paddingLeft: '12px',
                  paddingRight: '16px',
                },
              };
            }}
            wrapLines
          >
            {content}
          </SyntaxHighlighter>
        ) : (
          <div className="p-4 text-sm text-slate-400">
            {!filePath ? "Source location not available in graph data." : "No content loaded."}
          </div>
        )}
      </div>
    </div>
  );
};

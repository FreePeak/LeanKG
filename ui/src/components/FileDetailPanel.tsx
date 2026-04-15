import { useState, useEffect } from 'react';
import { X, ChevronRight, ArrowUpRight, ArrowDownLeft, FileCode, FolderOpen } from 'lucide-react';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import type { KGNode, KGEdge } from '../lib/graph-adapter';
import { getFileFunctions, getNodeRelationships } from '../lib/graph-adapter';

interface FileDetailPanelProps {
  selectedFileId: string | null;
  graphData: { nodes: KGNode[]; relationships: KGEdge[] } | null;
  onClose: () => void;
  onFunctionSelect?: (functionId: string) => void;
  onNavigateToFile?: (fileId: string) => void;
}

const FILE_DETAIL_STYLES = `
  @keyframes slide-in-right {
    from { transform: translateX(100%); opacity: 0; }
    to { transform: translateX(0); opacity: 1; }
  }
  .file-detail-panel {
    animation: slide-in-right 200ms ease-out;
  }
  .function-item:hover {
    background: rgba(59, 130, 246, 0.1);
  }
  .relationship-item:hover {
    background: rgba(124, 58, 237, 0.1);
  }
`;

const codeTheme = {
  ...vscDarkPlus,
  'pre[class*="language-"]': {
    ...vscDarkPlus['pre[class*="language-"]'],
    background: '#0a0a10',
    margin: 0,
    padding: '12px 0',
    fontSize: '12px',
    lineHeight: '1.6',
  },
  'code[class*="language-"]': {
    ...vscDarkPlus['code[class*="language-"]'],
    background: 'transparent',
    fontFamily: '"JetBrains Mono", "Fira Code", monospace',
  },
};

export const FileDetailPanel = ({
  selectedFileId,
  graphData,
  onClose,
  onFunctionSelect,
  onNavigateToFile,
}: FileDetailPanelProps) => {
  const [activeTab, setActiveTab] = useState<'functions' | 'relationships' | 'content'>('functions');
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [contentLoading, setContentLoading] = useState(false);

  if (!selectedFileId || !graphData) return null;

  const fileNode = graphData.nodes.find(n => n.id === selectedFileId);
  const fileProps = fileNode?.properties as Record<string, unknown> | undefined;
  const filePath = (fileProps?.filePath || fileProps?.file_path) as string | undefined;

  const functions = getFileFunctions(graphData.nodes, graphData.relationships, selectedFileId);
  const { callsFrom, callsTo, imports } = getNodeRelationships(graphData.nodes, graphData.relationships, selectedFileId);

  const uniqueCallTargets = [...new Set(callsFrom.map(c => c.target_id || c.targetId).filter((id): id is string => Boolean(id)))];
  const uniqueCallers = [...new Set(callsTo.map(c => c.source_id || c.sourceId).filter((id): id is string => Boolean(id)))];

  useEffect(() => {
    if (!filePath) {
      setFileContent(null);
      return;
    }
    let cancelled = false;
    setContentLoading(true);
    fetch(`/api/file?path=${encodeURIComponent(filePath)}`)
      .then(res => res.json())
      .then(response => {
        if (!cancelled) {
          if (response.success && response.data?.content) {
            setFileContent(response.data.content);
          } else {
            setFileContent(`/* ${response.error || 'Failed to read file'}: ${filePath} */`);
          }
        }
      })
      .catch((err) => {
        if (!cancelled) setFileContent(`/* Error fetching file: ${err.message} */`);
      })
      .finally(() => {
        if (!cancelled) setContentLoading(false);
      });
    return () => { cancelled = true; };
  }, [filePath]);

  return (
    <>
      <style>{FILE_DETAIL_STYLES}</style>
      <div className="file-detail-panel absolute right-0 top-0 bottom-0 w-[400px] bg-[#0A0F24]/95 backdrop-blur-md shadow-2xl border-l border-slate-700/50 flex flex-col z-30">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-700/50 px-4 py-3 bg-gradient-to-r from-blue-500/10 to-purple-500/5">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <FolderOpen className="h-4 w-4 text-blue-400 shrink-0" />
            <span className="truncate font-mono text-xs text-slate-200">
              {filePath?.split('/').pop() || fileNode?.label || selectedFileId.split('::').pop()}
            </span>
          </div>
          <button
            onClick={onClose}
            className="rounded p-1 text-slate-400 transition-colors hover:bg-slate-800 hover:text-slate-200 ml-2 shrink-0"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* File path */}
        {filePath && (
          <div className="px-4 py-2 border-b border-slate-800 bg-slate-900/30">
            <p className="text-[10px] font-mono text-slate-500 truncate" title={filePath}>
              {filePath}
            </p>
          </div>
        )}

        {/* Tabs */}
        <div className="flex border-b border-slate-700/50">
          <button
            onClick={() => setActiveTab('functions')}
            className={`flex-1 px-4 py-2.5 text-xs font-medium transition-colors ${
              activeTab === 'functions'
                ? 'text-blue-400 border-b-2 border-blue-400 bg-blue-500/5'
                : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            Functions ({functions.length})
          </button>
          <button
            onClick={() => setActiveTab('relationships')}
            className={`flex-1 px-4 py-2.5 text-xs font-medium transition-colors ${
              activeTab === 'relationships'
                ? 'text-purple-400 border-b-2 border-purple-400 bg-purple-500/5'
                : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            Relationships ({callsFrom.length + callsTo.length + imports.length})
          </button>
          <button
            onClick={() => setActiveTab('content')}
            className={`flex-1 px-4 py-2.5 text-xs font-medium transition-colors ${
              activeTab === 'content'
                ? 'text-emerald-400 border-b-2 border-emerald-400 bg-emerald-500/5'
                : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            Content
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {activeTab === 'functions' ? (
            <div className="p-2">
              {functions.length === 0 ? (
                <p className="text-xs text-slate-500 text-center py-8">No functions found in this file</p>
              ) : (
                <div className="flex flex-col gap-1">
                  {functions.map((func) => {
                    const funcProps = func.properties as Record<string, unknown> | undefined;
                    const funcPath = (funcProps?.filePath || funcProps?.file_path) as string | undefined;
                    const startLine = ((funcProps?.startLine || funcProps?.start_line) as number | undefined) ?? 0;
                    const endLine = ((funcProps?.endLine || funcProps?.end_line) as number | undefined) ?? startLine;

                    return (
                      <button
                        key={func.id}
                        onClick={() => onFunctionSelect?.(func.id)}
                        className="function-item w-full text-left px-3 py-2 rounded-lg border border-transparent hover:border-blue-500/30 transition-colors"
                      >
                        <div className="flex items-center gap-2">
                          <FileCode className="h-3.5 w-3.5 text-emerald-400 shrink-0" />
                          <span className="text-xs font-mono text-slate-200 truncate">
                            {func.label || func.id.split('::').pop()}
                          </span>
                        </div>
                        {funcPath && (
                          <p className="text-[10px] text-slate-500 mt-1 pl-5 font-mono">
                            L{startLine}-{endLine}
                          </p>
                        )}
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          ) : activeTab === 'content' ? (
            <div className="h-full overflow-auto bg-[#0a0a10]">
              {contentLoading ? (
                <div className="flex items-center justify-center h-full text-slate-400 text-sm">
                  Loading content...
                </div>
              ) : fileContent ? (
                <SyntaxHighlighter
                  language={(filePath?.split('.').pop()?.toLowerCase() || 'text') as any}
                  style={codeTheme as any}
                  showLineNumbers
                  startingLineNumber={1}
                  lineNumberStyle={{
                    minWidth: '3em',
                    paddingRight: '1em',
                    color: '#5a5a70',
                    textAlign: 'right',
                    userSelect: 'none',
                  }}
                  wrapLines
                >
                  {fileContent}
                </SyntaxHighlighter>
              ) : (
                <p className="text-xs text-slate-500 text-center py-8">No content available</p>
              )}
            </div>
          ) : (
            <div className="p-2">
              {/* Calls from this file */}
              {uniqueCallTargets.length > 0 && (
                <div className="mb-4">
                  <h4 className="flex items-center gap-1.5 px-2 py-1.5 text-[10px] font-semibold text-slate-400 uppercase tracking-wider">
                    <ArrowUpRight className="h-3 w-3 text-purple-400" />
                    Calls ({uniqueCallTargets.length})
                  </h4>
                  <div className="flex flex-col gap-1">
                    {uniqueCallTargets.map((targetId) => {
                      const targetNode = graphData.nodes.find(n => n.id === targetId);
                      return (
                        <button
                          key={targetId}
                          onClick={() => {
                            if (targetNode?.properties?.filePath) {
                              onNavigateToFile?.(targetId);
                            }
                          }}
                          className="relationship-item w-full text-left px-3 py-2 rounded-lg border border-transparent hover:border-purple-500/30 transition-colors"
                        >
                          <span className="text-xs font-mono text-slate-300 truncate block">
                            {targetNode?.label || targetId.split('::').pop() || targetId}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Callers of this file */}
              {uniqueCallers.length > 0 && (
                <div className="mb-4">
                  <h4 className="flex items-center gap-1.5 px-2 py-1.5 text-[10px] font-semibold text-slate-400 uppercase tracking-wider">
                    <ArrowDownLeft className="h-3 w-3 text-amber-400" />
                    Called By ({uniqueCallers.length})
                  </h4>
                  <div className="flex flex-col gap-1">
                    {uniqueCallers.map((callerId) => {
                      const callerNode = graphData.nodes.find(n => n.id === callerId);
                      return (
                        <button
                          key={callerId}
                          onClick={() => {
                            if (callerNode?.properties?.filePath) {
                              onNavigateToFile?.(callerId);
                            }
                          }}
                          className="relationship-item w-full text-left px-3 py-2 rounded-lg border border-transparent hover:border-amber-500/30 transition-colors"
                        >
                          <span className="text-xs font-mono text-slate-300 truncate block">
                            {callerNode?.label || callerId.split('::').pop() || callerId}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Imports */}
              {imports.length > 0 && (
                <div className="mb-4">
                  <h4 className="flex items-center gap-1.5 px-2 py-1.5 text-[10px] font-semibold text-slate-400 uppercase tracking-wider">
                    <ChevronRight className="h-3 w-3 text-blue-400" />
                    Imports ({imports.length})
                  </h4>
                  <div className="flex flex-col gap-1">
                    {[...new Set(imports.map(i => i.target_id || i.targetId).filter((id): id is string => Boolean(id)))].map((importId) => {
                      const importNode = graphData.nodes.find(n => n.id === importId);
                      return (
                        <button
                          key={importId}
                          onClick={() => {
                            if (importNode?.properties?.filePath) {
                              onNavigateToFile?.(importId);
                            }
                          }}
                          className="relationship-item w-full text-left px-3 py-2 rounded-lg border border-transparent hover:border-blue-500/30 transition-colors"
                        >
                          <span className="text-xs font-mono text-slate-300 truncate block">
                            {importNode?.label || importId?.split('::').pop() || importId}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {uniqueCallTargets.length === 0 && uniqueCallers.length === 0 && imports.length === 0 && (
                <p className="text-xs text-slate-500 text-center py-8">No relationships found</p>
              )}
            </div>
          )}
        </div>
      </div>
    </>
  );
};
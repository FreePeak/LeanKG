// hooks/core/tool-naming.mjs
export function createToolNamer(platform) {
  return function toolName(bareTool) {
    return bareTool;
  };
}
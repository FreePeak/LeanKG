// Maps the report Transcript (internal/telemetry/report Message) onto
// assistant-ui ThreadMessageLike, the input of useExternalStoreRuntime.
//
// Every tool call becomes a "tool-call" part with toolCallId, toolName,
// argsText, args, and an isError flag. LeanKG calls are renamed to the
// toolkit key "leankg" so one renderer handles them. Their enrichment, plus
// the post-LeanKG fallback flag, travels in `result`. Every other tool keeps
// its own name and uses the default collapsed renderer.

import type { ThreadMessageLike } from '@assistant-ui/react';
import type { CallRow, Message, MessagePart, ToolCallPart, Transcript } from '@/api/types';

/** Toolkit key for LeanKG tool calls. */
export const LEANKG_TOOL_NAME = 'leankg';

/** The `result` payload carried by every mapped tool-call part. */
export interface ReplayResult {
  /** The tool's text output, as captured. */
  output: string;
  /** Original tool name, e.g. a Read or Grep call or the agent's MCP name. */
  originalToolName: string;
  /** A post-LeanKG search outside the hits (DS-17). */
  fallback: boolean;
  /** LeanKG enrichment, present only for LeanKG calls. */
  leankg?: CallRow;
}

type Args = Record<string, string | number | boolean | null>;

/**
 * Parse the captured argsText into a flat object. Non-object JSON or invalid
 * JSON yields an empty object, and nested values are re-stringified, so this
 * never throws.
 */
export function parseArgs(argsText: string): Args {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argsText);
  } catch {
    return {};
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
  const out: Args = {};
  for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
    if (v === null || typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') {
      out[k] = v;
    } else {
      out[k] = JSON.stringify(v);
    }
  }
  return out;
}

export function toolPartToReplay(tool: ToolCallPart): ThreadMessageLike['content'] extends infer C ? C extends readonly unknown[] ? C[number] : never : never {
  const isLeanKG = Boolean(tool.leankg);
  const result: ReplayResult = {
    output: tool.result ?? '',
    originalToolName: tool.toolName,
    fallback: Boolean(tool.fallback),
    ...(isLeanKG ? { leankg: tool.leankg } : {}),
  };
  return {
    type: 'tool-call',
    toolCallId: tool.toolCallId,
    toolName: isLeanKG ? LEANKG_TOOL_NAME : tool.toolName,
    args: parseArgs(tool.argsText),
    argsText: tool.argsText,
    result,
    isError: Boolean(tool.isError),
  };
}

function partToReplay(part: MessagePart) {
  if (part.type === 'tool-call' && part.tool) return toolPartToReplay(part.tool);
  return { type: 'text' as const, text: part.text ?? '' };
}

export function messageToReplay(m: Message): ThreadMessageLike {
  const role = m.role === 'user' ? 'user' : 'assistant';
  return {
    id: m.id,
    role,
    createdAt: new Date(m.createdAt),
    content: (m.content ?? []).map(partToReplay),
    // A captured assistant message is finished. Without this, assistant-ui
    // falls back to "running" and shows an in-progress placeholder. The runtime
    // rejects a status on user messages, so it is set on assistant turns only.
    ...(role === 'assistant' ? { status: { type: 'complete' as const, reason: 'stop' as const } } : {}),
  };
}

/** Map a whole transcript. Never returns a null list. */
export function toThreadMessages(transcript: Transcript): ThreadMessageLike[] {
  return (transcript.messages ?? []).map(messageToReplay);
}

/** Narrow an unknown `result` back to the replay envelope. */
export function asReplayResult(value: unknown): ReplayResult {
  if (value && typeof value === 'object') {
    const v = value as Partial<ReplayResult>;
    return {
      output: typeof v.output === 'string' ? v.output : '',
      originalToolName: typeof v.originalToolName === 'string' ? v.originalToolName : '',
      fallback: v.fallback === true,
      leankg: v.leankg,
    };
  }
  return { output: typeof value === 'string' ? value : '', originalToolName: '', fallback: false };
}

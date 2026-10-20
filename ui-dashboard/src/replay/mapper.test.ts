import { describe, expect, it } from 'vitest';
import { asReplayResult, LEANKG_TOOL_NAME, parseArgs, toThreadMessages } from './mapper';
import type { CallRow, Transcript } from '@/api/types';

const call: CallRow = {
  id: 'c1',
  ts: '2026-10-10T09:00:00Z',
  latency_ms: 40,
  transport: 'stdio',
  tool: 'query',
  action: 'search',
  command: 'linkSession',
  outcome: 'ok',
  outcome_reason: '',
  error_code: '',
  rung: 'L1',
  confidence: 'high',
  freshness: 'fresh',
  hits: 3,
  out_tokens: 100,
  baseline_tokens: 900,
  baseline_method: 'estimate',
  tokens_saved: 800,
};

const transcript: Transcript = {
  session_id: 's1',
  synthetic: false,
  messages: [
    { id: 'u1', role: 'user', createdAt: '2026-10-10T09:00:00Z', tokens: 4, content: [{ type: 'text', text: 'hi' }] },
    {
      id: 'a1',
      role: 'assistant',
      createdAt: '2026-10-10T09:00:05Z',
      tokens: 10,
      content: [
        { type: 'text', text: 'looking' },
        {
          type: 'tool-call',
          tool: { type: 'tool-call', toolCallId: 't1', toolName: 'mcp__leankg__query', argsText: '{"query":"x","limit":3}', result: 'ok', isError: false, leankg: call, fallback: false },
        },
        {
          type: 'tool-call',
          tool: { type: 'tool-call', toolCallId: 't2', toolName: 'Grep', argsText: '{"pattern":"y"}', result: 'no match', isError: true, fallback: true },
        },
      ],
    },
  ],
};

describe('parseArgs', () => {
  it('returns a flat object for valid object JSON', () => {
    expect(parseArgs('{"query":"x","limit":3,"deep":{"a":1},"ok":true,"none":null}')).toEqual({
      query: 'x',
      limit: 3,
      deep: '{"a":1}',
      ok: true,
      none: null,
    });
  });

  it('never throws: invalid JSON, arrays and scalars give an empty object', () => {
    expect(parseArgs('{not json')).toEqual({});
    expect(parseArgs('[1,2]')).toEqual({});
    expect(parseArgs('"text"')).toEqual({});
    expect(parseArgs('')).toEqual({});
  });
});

describe('toThreadMessages', () => {
  const messages = toThreadMessages(transcript);

  it('maps roles and timestamps', () => {
    expect(messages).toHaveLength(2);
    expect(messages[0]).toMatchObject({ id: 'u1', role: 'user' });
    expect(messages[1].role).toBe('assistant');
    expect(messages[1].createdAt).toBeInstanceOf(Date);
    expect((messages[1].createdAt as Date).toISOString()).toBe('2026-10-10T09:00:05.000Z');
  });

  it('marks every mapped message complete so no in-progress placeholder renders', () => {
    expect(messages[1].status).toEqual({ type: 'complete', reason: 'stop' });
    // The runtime throws if a user message carries a status.
    expect(messages[0]).not.toHaveProperty('status');
  });

  it('maps text parts to text content', () => {
    expect(messages[0].content).toEqual([{ type: 'text', text: 'hi' }]);
    expect((messages[1].content as unknown[])[0]).toEqual({ type: 'text', text: 'looking' });
  });

  it('renames LeanKG calls to the toolkit key and carries enrichment in result', () => {
    const part = (messages[1].content as unknown as Array<Record<string, unknown>>)[1];
    expect(part).toMatchObject({
      type: 'tool-call',
      toolCallId: 't1',
      toolName: LEANKG_TOOL_NAME,
      argsText: '{"query":"x","limit":3}',
      isError: false,
    });
    expect(part.args).toEqual({ query: 'x', limit: 3 });
    const result = asReplayResult(part.result);
    expect(result.leankg).toEqual(call);
    expect(result.originalToolName).toBe('mcp__leankg__query');
    expect(result.fallback).toBe(false);
  });

  it('keeps non-LeanKG tool names, marks errors and post-LeanKG fallbacks', () => {
    const part = (messages[1].content as unknown as Array<Record<string, unknown>>)[2];
    expect(part).toMatchObject({ toolName: 'Grep', isError: true, toolCallId: 't2' });
    const result = asReplayResult(part.result);
    expect(result.fallback).toBe(true);
    expect(result.leankg).toBeUndefined();
    expect(result.output).toBe('no match');
  });

  it('never returns a null list, even for an empty transcript', () => {
    expect(toThreadMessages({ session_id: 'x', synthetic: true, messages: [] })).toEqual([]);
    expect(toThreadMessages({ session_id: 'x', synthetic: true, messages: null as unknown as [] })).toEqual([]);
  });
});

describe('asReplayResult', () => {
  it('tolerates unknown shapes', () => {
    expect(asReplayResult(undefined)).toEqual({ output: '', originalToolName: '', fallback: false });
    expect(asReplayResult('plain')).toEqual({ output: 'plain', originalToolName: '', fallback: false });
  });
});

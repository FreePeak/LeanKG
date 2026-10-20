// Renders the real assistant-ui runtime over a transcript (jsdom). This is the
// check that the external-store wiring, the read-only thread and the LeanKG
// toolkit renderer work together in 0.15.26, not only in type space.
import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { ReplayThread } from './replay-thread';
import type { Transcript } from '@/api/types';

const transcript: Transcript = {
  session_id: 's1',
  synthetic: true,
  reason: 'only captured LeanKG calls',
  messages: [
    { id: 'u1', role: 'user', createdAt: '2026-10-10T09:00:00Z', tokens: 3, content: [{ type: 'text', text: 'Where is the linker?' }] },
    {
      id: 'a1',
      role: 'assistant',
      createdAt: '2026-10-10T09:00:05Z',
      tokens: 10,
      content: [
        {
          type: 'tool-call',
          tool: {
            type: 'tool-call',
            toolCallId: 't1',
            toolName: 'mcp__leankg__query',
            argsText: '{"query":"linkSession"}',
            result: '2 callers',
            isError: false,
            fallback: false,
            leankg: {
              id: 'c1',
              ts: '2026-10-10T09:00:05Z',
              latency_ms: 44,
              transport: 'stdio',
              tool: 'query',
              action: 'callers',
              command: 'linkSession',
              outcome: 'ok',
              outcome_reason: '',
              error_code: '',
              rung: 'L2',
              confidence: 'medium',
              freshness: 'fresh',
              hits: 3,
              out_tokens: 120,
              baseline_tokens: 960,
              baseline_method: 'median grep+read tokens (estimate)',
              tokens_saved: 840,
              signals: { hits_used: 2, hits_returned: 3, used_precision: 0.67, fallback: false, fallback_tools: [], requery: false, abandoned_after: false },
            },
          },
        },
        {
          type: 'tool-call',
          tool: {
            type: 'tool-call',
            toolCallId: 't2',
            toolName: 'Grep',
            argsText: '{"pattern":"linkSession"}',
            result: 'internal/sessionlink/link.go:41',
            isError: false,
            fallback: true,
          },
        },
        { type: 'text', text: 'The linker starts from the dashboard process.' },
      ],
    },
  ],
};

describe('ReplayThread', () => {
  it('renders LeanKG and other tool calls and the text, read-only', () => {
    const { container } = render(<ReplayThread transcript={transcript} />);

    expect(screen.getByText('Where is the linker?')).toBeTruthy();
    expect(screen.getByText('The linker starts from the dashboard process.')).toBeTruthy();
    const leankg = screen.getByRole('region', { name: 'LeanKG query callers' });
    expect(within(leankg).getByText('rung L2')).toBeTruthy();
    expect(within(leankg).getByText('estimate')).toBeTruthy();
    expect(within(leankg).getByText(/Used 2 of 3 hits/)).toBeTruthy();

    expect(screen.getByText('post-LeanKG fallback')).toBeTruthy();
    expect(screen.getByText('Grep')).toBeTruthy();

    expect(screen.getByText(/only captured LeanKG calls/)).toBeTruthy();
    expect(container.querySelector('textarea')).toBeNull();
  });
});

// Read-only session replay on assistant-ui (plan v4.15 DS-23).
//
// Verified in @assistant-ui/react 0.15.26 .d.ts:
//   - useExternalStoreRuntime(adapter) with isDisabled and onNew (required,
//     never reached: there is no composer in this layout).
//   - AssistantRuntimeProvider takes runtime and config (AuiConfig).
//   - ThreadPrimitive.Root / Viewport / Messages (children render function) /
//     Empty and MessagePrimitive.Root / Parts (components.Text, components.tools).
// The composer is omitted, so the thread cannot send anything.

import { useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  MessagePartPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import type { Transcript } from '@/api/types';
import { OtherToolCall, replayConfig } from './tool-renderers';
import { toThreadMessages } from './mapper';

const identity = (m: ThreadMessageLike) => m;
const noop = async () => {};

function ReplayText() {
  return (
    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed">
      <MessagePartPrimitive.Text />
    </p>
  );
}

function ReplayMessage({ role }: { role: 'user' | 'assistant' | 'system' }) {
  return (
    <MessagePrimitive.Root
      className={
        role === 'user'
          ? 'ml-auto max-w-[85%] rounded-lg bg-surface-muted px-3 py-2'
          : 'mr-auto flex w-full flex-col gap-2'
      }
    >
      <MessagePrimitive.Parts components={{ Text: ReplayText, tools: { Fallback: OtherToolCall } }} />
    </MessagePrimitive.Root>
  );
}

/** Read-only thread over a transcript. Synthetic transcripts get a notice. */
export function ReplayThread({ transcript }: { transcript: Transcript }) {
  const messages = useMemo(() => toThreadMessages(transcript), [transcript]);
  const runtime = useExternalStoreRuntime<ThreadMessageLike>({
    messages,
    convertMessage: identity,
    isDisabled: true,
    isRunning: false,
    onNew: noop,
  });

  return (
    <div className="flex flex-col gap-3">
      {transcript.synthetic ? (
        <Card role="note" className="border-dashed p-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Partial replay</Badge>
            <span className="text-secondary-foreground">
              {transcript.reason ?? 'Only captured LeanKG calls are shown. Grant transcript reading in Settings to see the full session.'}
            </span>
          </div>
        </Card>
      ) : null}
      <AssistantRuntimeProvider runtime={runtime} config={replayConfig}>
        <ThreadPrimitive.Root className="rounded-lg border border-border bg-surface">
          <ThreadPrimitive.Viewport className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto p-4">
            <ThreadPrimitive.Empty>
              <p className="py-6 text-center text-sm text-muted-foreground">This session has no captured messages.</p>
            </ThreadPrimitive.Empty>
            <ThreadPrimitive.Messages>
              {({ message }) => <ReplayMessage role={message.role} />}
            </ThreadPrimitive.Messages>
          </ThreadPrimitive.Viewport>
        </ThreadPrimitive.Root>
      </AssistantRuntimeProvider>
    </div>
  );
}

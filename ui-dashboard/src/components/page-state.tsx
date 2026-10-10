import type { ReactNode } from 'react';
import { AlertTriangle, Inbox, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';

export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="mt-1 text-sm text-secondary-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function LoadingState({ label = 'Loading' }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex items-center gap-2 py-10 text-sm text-muted-foreground">
      <Loader2 className="size-4 animate-spin" aria-hidden="true" />
      {label}...
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: Error; onRetry?: () => void }) {
  return (
    <Card role="alert" className="flex flex-col gap-3 border-status-critical p-4">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-status-critical" aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-sm font-medium">Could not load this view.</p>
          <p className="mt-1 break-words text-sm text-secondary-foreground">{error.message}</p>
        </div>
      </div>
      {onRetry ? (
        <div>
          <Button variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </div>
      ) : null}
    </Card>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border px-4 py-10 text-center">
      <Inbox className="size-5 text-muted-foreground" aria-hidden="true" />
      <p className="text-sm font-medium">{title}</p>
      {children ? <div className="max-w-md text-sm text-secondary-foreground">{children}</div> : null}
    </div>
  );
}

/** Wrap a page body with its loading, error and empty states. */
export function AsyncBody<T>({
  data,
  error,
  loading,
  reload,
  isEmpty,
  emptyTitle,
  emptyHint,
  children,
}: {
  data: T | undefined;
  error: Error | undefined;
  loading: boolean;
  reload: () => void;
  isEmpty?: (d: T) => boolean;
  emptyTitle?: string;
  emptyHint?: ReactNode;
  children: (d: T) => ReactNode;
}) {
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (data === undefined) return loading ? <LoadingState /> : null;
  if (isEmpty?.(data)) return <EmptyState title={emptyTitle ?? 'Nothing to show yet.'}>{emptyHint}</EmptyState>;
  return <>{children(data)}</>;
}

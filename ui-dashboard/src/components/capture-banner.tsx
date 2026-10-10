import { Link } from 'react-router';
import { ShieldAlert } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';

/** Shown on Overview when capture is off, linking to consent in Settings. */
export function CaptureBanner({ capture }: { capture: string }) {
  if (capture !== 'off') return null;
  return (
    <Card role="status" className="mb-5 flex flex-col gap-3 border-dashed p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-start gap-2.5">
        <ShieldAlert className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div>
          <p className="text-sm font-medium">Capture is off</p>
          <p className="text-sm text-secondary-foreground">No LeanKG calls are recorded. Turn on capture to see these figures.</p>
        </div>
      </div>
      <Button asChild variant="outline" size="sm">
        <Link to="/settings">Review consent</Link>
      </Button>
    </Card>
  );
}

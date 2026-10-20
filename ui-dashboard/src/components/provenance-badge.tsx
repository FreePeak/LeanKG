import { Badge, type BadgeProps } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import type { Provenance } from '@/lib/provenance';

/** A badge that names how a figure was produced. Hover or focus shows the method. */
export function ProvenanceBadge({ provenance, className }: { provenance: Provenance; className?: string }) {
  const variant: BadgeProps['variant'] = provenance.kind === 'measured' ? 'measured' : 'estimate';
  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge variant={variant} className={className} tabIndex={0} aria-label={`${provenance.label}: ${provenance.method}`}>
            {provenance.label}
          </Badge>
        </TooltipTrigger>
        <TooltipContent>
          <span className="font-medium">{provenance.label === 'measured' ? 'Measured' : `Counterfactual ${provenance.label}`}.</span>{' '}
          {provenance.method}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

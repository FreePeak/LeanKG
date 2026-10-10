import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '@/lib/utils';

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
  {
    variants: {
      variant: {
        default: 'border-border bg-surface-muted text-foreground',
        outline: 'border-border text-secondary-foreground',
        /** Dashed border: an estimate or proxy, never a measurement. */
        estimate: 'border-dashed border-muted-foreground text-secondary-foreground bg-transparent',
        measured: 'border-accent bg-accent text-accent-foreground',
        good: 'border-status-good text-foreground',
        warning: 'border-status-warning text-foreground bg-status-warning/20',
        serious: 'border-status-serious text-foreground',
        critical: 'border-status-critical text-foreground',
      },
    },
    defaultVariants: { variant: 'default' },
  },
);

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { badgeVariants };

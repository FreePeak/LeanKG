import * as React from 'react';
import { cn } from '@/lib/utils';

/** Native checkbox with an accent tint. Native keeps keyboard and screen-reader behaviour for free. */
export function Checkbox({ className, ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      type="checkbox"
      className={cn('size-4 cursor-pointer accent-[var(--accent)] disabled:cursor-not-allowed', className)}
      {...props}
    />
  );
}

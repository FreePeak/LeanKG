import { useState, type ReactNode } from 'react';
import { NavLink } from 'react-router';
import { Activity, Brain, FlaskConical, LayoutDashboard, ListTree, Menu, Settings, TriangleAlert, Wrench, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

const NAV = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, end: true },
  { to: '/sessions', label: 'Sessions', icon: ListTree, end: false },
  { to: '/failures', label: 'Failures', icon: TriangleAlert, end: false },
  { to: '/tools', label: 'Tools', icon: Wrench, end: false },
  { to: '/memory', label: 'Memory', icon: Brain, end: false },
  { to: '/evidence', label: 'Evidence', icon: FlaskConical, end: false },
  { to: '/settings', label: 'Settings', icon: Settings, end: false },
] as const;

/**
 * App frame. Desktop shows a fixed sidebar; below md the sidebar becomes a
 * menu behind a header button, so pages keep the full width on a phone.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);

  return (
    <div className="min-h-screen md:grid md:grid-cols-[13rem_1fr]">
      <header className="sticky top-0 z-30 flex items-center justify-between border-b border-border bg-background px-4 py-3 md:hidden">
        <span className="text-sm font-semibold">LeanKG</span>
        <Button variant="ghost" size="icon" aria-label={open ? 'Close menu' : 'Open menu'} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? <X /> : <Menu />}
        </Button>
      </header>

      <aside
        className={cn(
          'border-b border-border bg-surface px-3 py-4 md:sticky md:top-0 md:h-screen md:border-b-0 md:border-r',
          open ? 'block' : 'hidden',
          'md:block',
        )}
      >
        <div className="mb-4 hidden items-center gap-2 px-2 md:flex">
          <Activity className="size-4 text-accent" aria-hidden="true" />
          <span className="text-sm font-semibold">LeanKG</span>
        </div>
        <nav aria-label="Dashboard">
          <ul className="flex flex-col gap-0.5">
            {NAV.map((item) => {
              const Icon = item.icon;
              return (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    onClick={close}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm text-secondary-foreground hover:bg-surface-muted',
                        isActive && 'bg-surface-muted font-medium text-foreground',
                      )
                    }
                  >
                    <Icon className="size-4" aria-hidden="true" />
                    {item.label}
                  </NavLink>
                </li>
              );
            })}
          </ul>
        </nav>
      </aside>

      <main className="mx-auto w-full max-w-6xl px-4 py-6 md:px-8">{children}</main>
    </div>
  );
}

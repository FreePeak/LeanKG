// Daily calls and errors as a two-series line chart. Hand-rolled SVG (no
// chart library). Two series means a legend is always shown, and a table
// alternative sits behind a disclosure. Hover shows a crosshair and tooltip.

import { useId, useMemo, useState } from 'react';
import type { DayPoint } from '@/api/types';
import { formatDay, formatNumber } from '@/lib/format';

const W = 640;
const H = 220;
const PAD = { top: 12, right: 12, bottom: 28, left: 40 };

/** A "nice" upper bound for the y axis so ticks land on round numbers. */
export function niceMax(value: number): number {
  if (value <= 0) return 1;
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  const f = value / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nice * exp;
}

export function DailyChart({ series }: { series: DayPoint[] }) {
  const id = useId();
  const [hover, setHover] = useState<number | null>(null);
  const points = series ?? [];

  const geo = useMemo(() => {
    const innerW = W - PAD.left - PAD.right;
    const innerH = H - PAD.top - PAD.bottom;
    const max = niceMax(Math.max(1, ...points.map((p) => Math.max(p.calls, p.errors))));
    const x = (i: number) => (points.length <= 1 ? PAD.left + innerW / 2 : PAD.left + (i * innerW) / (points.length - 1));
    const y = (v: number) => PAD.top + innerH - (v / max) * innerH;
    const path = (get: (p: DayPoint) => number) =>
      points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(get(p)).toFixed(1)}`).join(' ');
    const ticks = [0, 0.5, 1].map((t) => ({ v: max * t, y: y(max * t) }));
    return { max, x, y, callsPath: path((p) => p.calls), errorsPath: path((p) => p.errors), ticks, innerW };
  }, [points]);

  if (points.length === 0) return null;

  const first = points[0];
  const last = points[points.length - 1];
  const hovered = hover !== null ? points[hover] : null;

  return (
    <figure className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-4 text-xs text-secondary-foreground">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-0.5 w-4 rounded-full" style={{ background: 'var(--viz-1)' }} aria-hidden="true" />
          Calls per day
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-0.5 w-4 rounded-full" style={{ background: 'var(--viz-2)' }} aria-hidden="true" />
          Errors per day
        </span>
      </div>
      <div className="relative">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          role="img"
          aria-labelledby={`${id}-title`}
          className="block h-auto w-full"
          onMouseLeave={() => setHover(null)}
        >
          <title id={`${id}-title`}>
            Daily LeanKG calls and errors, {formatDay(first.day)} to {formatDay(last.day)}
          </title>
          {geo.ticks.map((t) => (
            <g key={t.v}>
              <line x1={PAD.left} x2={W - PAD.right} y1={t.y} y2={t.y} stroke="var(--border)" strokeWidth={1} />
              <text x={PAD.left - 6} y={t.y + 4} textAnchor="end" fontSize={11} fill="var(--text-muted)">
                {formatNumber(t.v)}
              </text>
            </g>
          ))}
          <text x={PAD.left} y={H - 6} fontSize={11} fill="var(--text-muted)">{formatDay(first.day)}</text>
          <text x={W - PAD.right} y={H - 6} textAnchor="end" fontSize={11} fill="var(--text-muted)">{formatDay(last.day)}</text>
          <path d={geo.callsPath} fill="none" stroke="var(--viz-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          <path d={geo.errorsPath} fill="none" stroke="var(--viz-2)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          {points.map((p, i) => (
            <g key={p.day}>
              <circle cx={geo.x(i)} cy={geo.y(p.calls)} r={hover === i ? 4 : 3} fill="var(--viz-1)" />
              <circle cx={geo.x(i)} cy={geo.y(p.errors)} r={hover === i ? 4 : 3} fill="var(--viz-2)" />
            </g>
          ))}
          {points.map((p, i) => {
            const band = points.length > 1 ? (W - PAD.left - PAD.right) / (points.length - 1) : W;
            return (
              <rect
                key={`hit-${p.day}`}
                x={geo.x(i) - band / 2}
                y={PAD.top}
                width={band}
                height={H - PAD.top - PAD.bottom}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onFocus={() => setHover(i)}
                tabIndex={0}
                aria-label={`${formatDay(p.day)}: ${p.calls} calls, ${p.errors} errors`}
              />
            );
          })}
          {hovered && hover !== null ? (
            <line x1={geo.x(hover)} x2={geo.x(hover)} y1={PAD.top} y2={H - PAD.bottom} stroke="var(--text-muted)" strokeDasharray="3 3" strokeWidth={1} />
          ) : null}
        </svg>
        {hovered && hover !== null ? (
          <div
            className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs shadow-md"
            style={{ left: `${(geo.x(hover) / W) * 100}%` }}
            role="status"
          >
            <div className="font-medium">{formatDay(hovered.day)}</div>
            <div>{formatNumber(hovered.calls)} calls</div>
            <div>{formatNumber(hovered.errors)} errors</div>
            <div>{formatNumber(hovered.sessions)} sessions</div>
          </div>
        ) : null}
      </div>
      <details className="text-sm">
        <summary className="cursor-pointer text-secondary-foreground">View as table</summary>
        <div className="mt-2 overflow-x-auto">
          <table className="w-full text-sm">
            <caption className="sr-only">Daily calls, errors and sessions</caption>
            <thead className="text-left text-muted-foreground">
              <tr>
                <th scope="col" className="px-2 py-1 font-medium">Day</th>
                <th scope="col" className="px-2 py-1 text-right font-medium">Calls</th>
                <th scope="col" className="px-2 py-1 text-right font-medium">Errors</th>
                <th scope="col" className="px-2 py-1 text-right font-medium">Sessions</th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.day} className="border-t border-border">
                  <td className="px-2 py-1">{formatDay(p.day)}</td>
                  <td className="px-2 py-1 text-right tabular-nums">{formatNumber(p.calls)}</td>
                  <td className="px-2 py-1 text-right tabular-nums">{formatNumber(p.errors)}</td>
                  <td className="px-2 py-1 text-right tabular-nums">{formatNumber(p.sessions)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  );
}

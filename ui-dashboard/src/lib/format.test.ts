import { describe, expect, it } from 'vitest';
import { formatBytes, formatDuration, formatMs, formatNumber, formatPercent, formatRatio, humanize } from './format';
import { niceMax } from '@/components/charts/daily-chart';

describe('formatNumber', () => {
  it('keeps small values exact and compacts from 10,000', () => {
    expect(formatNumber(0)).toBe('0');
    expect(formatNumber(1842)).toBe('1,842');
    expect(formatNumber(12_300)).toBe('12.3K');
    expect(formatNumber(412_300)).toBe('412.3K');
  });

  it('returns a dash for non-finite input', () => {
    expect(formatNumber(Number.NaN)).toBe('-');
    expect(formatNumber(Number.POSITIVE_INFINITY)).toBe('-');
  });
});

describe('percent formatters', () => {
  it('formatPercent takes percent points', () => {
    expect(formatPercent(93.4)).toBe('93%');
    expect(formatPercent(93.4, 1)).toBe('93.4%');
  });

  it('formatRatio takes a 0..1 ratio', () => {
    expect(formatRatio(0.934)).toBe('93%');
    expect(formatRatio(1)).toBe('100%');
    expect(formatRatio(0)).toBe('0%');
  });
});

describe('formatDuration and formatMs', () => {
  it('formats seconds into short units', () => {
    expect(formatDuration(42)).toBe('42s');
    expect(formatDuration(192)).toBe('3m 12s');
    expect(formatDuration(7_500)).toBe('2h 05m');
    expect(formatDuration(100_800)).toBe('1d 4h');
    expect(formatDuration(-5)).toBe('0s');
  });

  it('formats milliseconds across ranges', () => {
    expect(formatMs(850)).toBe('850 ms');
    expect(formatMs(1200)).toBe('1.2 s');
    expect(formatMs(125_000)).toBe('2m 05s');
  });
});

describe('formatBytes', () => {
  it('uses binary units with one decimal', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(18_432_000)).toBe('17.6 MB');
    expect(formatBytes(3 * 1024 ** 3)).toBe('3.0 GB');
  });
});

describe('humanize and chart scale', () => {
  it('humanizes keys', () => {
    expect(humanize('tokens_saved')).toBe('Tokens saved');
    expect(humanize('claude-code')).toBe('Claude code');
  });

  it('niceMax rounds the y axis to 1, 2, 5 or 10 steps', () => {
    expect(niceMax(0)).toBe(1);
    expect(niceMax(7)).toBe(10);
    expect(niceMax(18)).toBe(20);
    expect(niceMax(360)).toBe(500);
  });
});

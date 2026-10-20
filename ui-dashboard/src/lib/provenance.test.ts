// The provenance rule (plan v4.15 section 2, rule 5). These tests pin it:
// counterfactuals are estimate or proxy with a method, and only a valid
// controlled A/B may say "measured".
import { describe, expect, it } from 'vitest';
import { controlledProvenance, kpiProvenance, proxyProvenance } from './provenance';

describe('kpiProvenance', () => {
  it('labels an estimate KPI as estimate and keeps its method', () => {
    expect(kpiProvenance({ estimate: true, method: 'baseline = median' })).toEqual({
      kind: 'estimate',
      label: 'estimate',
      method: 'baseline = median',
    });
  });

  it('never returns measured, even when the server sends one', () => {
    const p = kpiProvenance({ estimate: true, method: 'x' });
    expect(p?.kind).not.toBe('measured');
    expect(p?.label).not.toBe('measured');
  });

  it('gives an estimate without a method an explicit placeholder', () => {
    expect(kpiProvenance({ estimate: true, method: '   ' })?.method).toBe('Method not provided by the server.');
    expect(kpiProvenance({ estimate: true })?.method).toBe('Method not provided by the server.');
  });

  it('returns null for a direct observation (no badge)', () => {
    expect(kpiProvenance({ estimate: false, method: '' })).toBeNull();
  });
});

describe('proxyProvenance', () => {
  it('labels proxy figures as proxy with their method', () => {
    expect(proxyProvenance('share with no recall')).toEqual({ kind: 'proxy', label: 'proxy', method: 'share with no recall' });
  });
});

describe('controlledProvenance', () => {
  it('is measured only when the controlled A/B is valid', () => {
    expect(controlledProvenance(true, 3)).toMatchObject({ kind: 'measured', label: 'measured' });
    expect(controlledProvenance(true, 1)?.method).toContain('1 run,');
  });

  it('is null when the controlled A/B is not valid', () => {
    expect(controlledProvenance(false, 0)).toBeNull();
    expect(controlledProvenance(false, 5)).toBeNull();
  });
});

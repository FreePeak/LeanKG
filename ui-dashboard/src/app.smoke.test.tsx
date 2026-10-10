// Renders every route of the real App against the synthetic fixtures. This
// catches page-level runtime errors (bad props, missing data) that typecheck
// cannot see.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import App from './App';
import { setApiForTests } from './api/client';
import { createFixtureApi } from './fixtures';

const ROUTES: Array<[string, string]> = [
  ['#/', 'Overview'],
  ['#/consent', 'Session telemetry'],
  ['#/sessions', 'Sessions'],
  ['#/sessions/s-7f3a', 'Claude code session'],
  ['#/failures', 'Failures'],
  ['#/tools', 'Tools'],
  ['#/memory', 'Memory'],
  ['#/evidence', 'Evidence'],
  ['#/settings', 'Settings'],
  ['#/nope', 'Page not found'],
];

describe('App routes with fixtures', () => {
  beforeEach(() => {
    setApiForTests(createFixtureApi());
    window.sessionStorage.setItem('leankg-dashboard-consent-seen', '1');
  });
  afterEach(() => {
    cleanup();
    setApiForTests(null);
    window.sessionStorage.clear();
  });

  for (const [hash, heading] of ROUTES) {
    it(`renders ${hash}`, async () => {
      window.location.hash = hash.slice(1);
      render(<App />);
      expect(await screen.findByRole('heading', { level: 1, name: heading }, { timeout: 4000 })).toBeTruthy();
    });
  }
});

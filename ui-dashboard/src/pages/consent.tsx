import { Link, useNavigate } from 'react-router';
import { AsyncBody, PageHeader } from '@/components/page-state';
import { ConsentForm } from '@/components/consent-form';
import { useAsync } from '@/api/hooks';
import type { Settings } from '@/api/types';

/** First-run consent screen (DS-24). Nothing is recorded until a level other than off is saved. */
export function ConsentPage() {
  const navigate = useNavigate();
  const state = useAsync((api, signal) => api.consent(signal), []);

  return (
    <>
      <PageHeader
        title="Session telemetry"
        description={
          <>
            Capture is off. Choose what LeanKG may record on this machine. Nothing leaves it.{' '}
            <Link to="/" className="underline underline-offset-2">
              Skip for now
            </Link>
          </>
        }
      />
      <AsyncBody data={state.data} error={state.error} loading={state.loading} reload={state.reload}>
        {(s: Settings) => (
          <ConsentForm
            settings={s}
            onSaved={() => {
              state.reload();
              navigate('/');
            }}
          />
        )}
      </AsyncBody>
    </>
  );
}

/**
 * FR-E39 — loading/progress: overlay shown while the graph/layout loads.
 * aria-busy + progressbar roles (FR-E43).
 */
export default function LoadingOverlay({
  loading,
  label = 'Loading graph…',
  progress,
}: {
  loading: boolean;
  label?: string;
  progress?: number;
}) {
  if (!loading) return null;
  return (
    <div
      className="loading-overlay"
      role="progressbar"
      aria-busy="true"
      aria-label={label}
      data-testid="loading-overlay"
    >
      <div className="spinner" aria-hidden="true" />
      <span className="loading-label">{label}</span>
      {progress != null && <span className="loading-progress">{Math.round(progress)}%</span>}
    </div>
  );
}

/**
 * FR-E40 — error states: dismissible banner with retry. Role=alert so
 * screen readers announce it (FR-E43).
 */
export default function ErrorBanner({
  message,
  onRetry,
  onDismiss,
}: {
  message: string | null;
  onRetry?: () => void;
  onDismiss?: () => void;
}) {
  if (!message) return null;
  return (
    <div className="error-banner" role="alert" data-testid="error-banner">
      <span className="error-text">{message}</span>
      {onRetry && (
        <button className="error-retry" onClick={onRetry}>
          Retry
        </button>
      )}
      {onDismiss && (
        <button className="error-dismiss" onClick={onDismiss} aria-label="Dismiss error">
          ×
        </button>
      )}
    </div>
  );
}

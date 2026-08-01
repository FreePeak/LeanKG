/**
 * FR-E33/E36 — project selector + stats: list registered projects (registry
 * + LEANKG_PROJECT_DIRS) and switch the active one (multi-repo galaxies).
 */
export interface ProjectInfo {
  name: string;
  path: string;
  elementCount?: number;
  lastIndexed?: string;
}

export function projectKey(p: ProjectInfo): string {
  return p.path || p.name;
}

export default function ProjectPanel({
  projects,
  activePath,
  onSwitch,
  onRefresh,
  refreshing,
}: {
  projects: ProjectInfo[];
  activePath: string | null;
  onSwitch: (path: string) => void;
  onRefresh: () => void;
  refreshing?: boolean;
}) {
  return (
    <section className="panel" data-testid="project-panel" aria-label="Projects">
      <h2 className="panel-title">Projects</h2>
      {projects.length === 0 ? (
        <p className="panel-muted">No registered projects. Run `leankg register &lt;name&gt;`.</p>
      ) : (
        <ul className="project-list">
          {projects.map((p) => {
            const key = projectKey(p);
            const active = activePath != null && p.path === activePath;
            return (
              <li key={key}>
                <button
                  className={`project-row${active ? ' active' : ''}`}
                  onClick={() => onSwitch(p.path)}
                  aria-pressed={active}
                >
                  <span className="project-name">{p.name || p.path}</span>
                  {p.elementCount != null && (
                    <span className="project-count">{p.elementCount} elements</span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <div className="project-actions">
        <button onClick={onRefresh} disabled={refreshing}>
          {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
    </section>
  );
}

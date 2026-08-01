/**
 * FR-E32/E38 — display settings menu: bloom intensity, edge brightness,
 * label toggle, density scale. Pure controlled component (state lives in
 * the explorer via useSettings).
 */
export interface DisplaySettings {
  bloomIntensity: number;
  edgeBrightness: number;
  labelsVisible: boolean;
  densityScale: number;
}

export const DEFAULT_SETTINGS: DisplaySettings = {
  bloomIntensity: 0.8,
  edgeBrightness: 0.45,
  labelsVisible: true,
  densityScale: 1,
};

export function clampSetting(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

export default function SettingsPanel({
  settings,
  onChange,
}: {
  settings: DisplaySettings;
  onChange: (s: DisplaySettings) => void;
}) {
  const set = (patch: Partial<DisplaySettings>) => onChange({ ...settings, ...patch });
  return (
    <section className="panel" data-testid="settings-panel" aria-label="Display settings">
      <h2 className="panel-title">Settings</h2>
      <label className="setting-row">
        <span>Bloom intensity</span>
        <input
          type="range"
          min={0}
          max={2}
          step={0.1}
          value={settings.bloomIntensity}
          onChange={(e) => set({ bloomIntensity: Number(e.target.value) })}
        />
        <span className="setting-value">{settings.bloomIntensity.toFixed(1)}</span>
      </label>
      <label className="setting-row">
        <span>Edge brightness</span>
        <input
          type="range"
          min={0}
          max={1}
          step={0.05}
          value={settings.edgeBrightness}
          onChange={(e) => set({ edgeBrightness: Number(e.target.value) })}
        />
        <span className="setting-value">{settings.edgeBrightness.toFixed(2)}</span>
      </label>
      <label className="setting-row">
        <span>Density scale</span>
        <input
          type="range"
          min={0.5}
          max={2}
          step={0.1}
          value={settings.densityScale}
          onChange={(e) => set({ densityScale: Number(e.target.value) })}
        />
        <span className="setting-value">{settings.densityScale.toFixed(1)}</span>
      </label>
      <label className="setting-row toggle">
        <span>Show labels</span>
        <input
          type="checkbox"
          checked={settings.labelsVisible}
          onChange={(e) => set({ labelsVisible: e.target.checked })}
        />
      </label>
    </section>
  );
}

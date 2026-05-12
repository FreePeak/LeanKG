import React from 'react';

interface EnvironmentSelectorProps {
  currentEnv: string;
  onEnvChange: (env: string) => void;
}

const environments = [
  { value: 'local', label: 'Local', color: '#4CAF50' },
  { value: 'staging', label: 'Staging', color: '#FF9800' },
  { value: 'production', label: 'Production', color: '#F44336' },
];

export const EnvironmentSelector: React.FC<EnvironmentSelectorProps> = ({
  currentEnv,
  onEnvChange,
}) => {
  return (
    <div style={{ display: 'flex', gap: '8px', padding: '8px', background: '#f5f5f5', borderRadius: '8px' }}>
      {environments.map((env) => (
        <button
          key={env.value}
          onClick={() => onEnvChange(env.value)}
          style={{
            padding: '6px 16px',
            border: 'none',
            borderRadius: '4px',
            cursor: 'pointer',
            background: currentEnv === env.value ? env.color : '#e0e0e0',
            color: currentEnv === env.value ? '#fff' : '#333',
            fontWeight: currentEnv === env.value ? 'bold' : 'normal',
          }}
        >
          {env.label}
        </button>
      ))}
    </div>
  );
};

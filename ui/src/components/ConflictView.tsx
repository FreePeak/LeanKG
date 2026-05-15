import React, { useState } from 'react';

interface Conflict {
  conflict_type: string;
  detail: string;
  risk: string;
}

interface ConflictViewProps {
  service: string;
}

export const ConflictView: React.FC<ConflictViewProps> = ({ service }) => {
  const [conflicts, setConflicts] = useState<Conflict[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchConflicts = async () => {
    setLoading(true);
    try {
      const response = await fetch(`/api/conflicts?service=${encodeURIComponent(service)}`);
      if (response.ok) {
        const data = await response.json();
        if (data.success) {
          setConflicts(data.data?.conflicts || []);
        }
      }
    } catch (e) {
      console.error('Failed to fetch conflicts:', e);
    }
    setLoading(false);
  };

  return (
    <div style={{ padding: '16px', border: '1px solid #ddd', borderRadius: '8px', marginTop: '16px' }}>
      <h3>Environment Conflicts: {service}</h3>
      <button onClick={fetchConflicts} disabled={loading} style={{ marginBottom: '12px' }}>
        {loading ? 'Checking...' : 'Check for Conflicts'}
      </button>
      {conflicts.length === 0 && !loading && (
        <p style={{ color: '#4CAF50' }}>No conflicts detected across environments.</p>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
        {conflicts.map((c, i) => (
          <div
            key={i}
            style={{
              padding: '12px',
              border: '1px solid #e0e0e0',
              borderRadius: '4px',
              borderLeft: `4px solid ${c.risk === 'HIGH' ? '#F44336' : c.risk === 'MEDIUM' ? '#FF9800' : '#4CAF50'}`,
            }}
          >
            <strong>{c.conflict_type}</strong>
            <span style={{
              marginLeft: '8px',
              padding: '2px 8px',
              borderRadius: '4px',
              background: c.risk === 'HIGH' ? '#FFEBEE' : c.risk === 'MEDIUM' ? '#FFF3E0' : '#E8F5E9',
              color: c.risk === 'HIGH' ? '#C62828' : c.risk === 'MEDIUM' ? '#EF6C00' : '#2E7D32',
              fontSize: '12px',
            }}>
              {c.risk}
            </span>
            <p style={{ margin: '4px 0', fontSize: '14px' }}>{c.detail}</p>
          </div>
        ))}
      </div>
    </div>
  );
};

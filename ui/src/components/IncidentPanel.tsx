import React, { useState } from 'react';

interface Incident {
  id: string;
  title: string;
  severity: string;
  root_cause: string;
  resolution: string;
  occurred_at: number;
}

interface IncidentPanelProps {
  service: string;
  env: string;
}

export const IncidentPanel: React.FC<IncidentPanelProps> = ({ service, env }) => {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchIncidents = async () => {
    setLoading(true);
    try {
      const response = await fetch(`/api/incidents?service=${encodeURIComponent(service)}&env=${env}`);
      if (response.ok) {
        const data = await response.json();
        if (data.success) {
          setIncidents(data.data?.incidents || []);
        }
      }
    } catch (e) {
      console.error('Failed to fetch incidents:', e);
    }
    setLoading(false);
  };

  const formatDate = (ts: number): string => {
    if (!ts) return 'Unknown';
    return new Date(ts * 1000).toLocaleString();
  };

  return (
    <div style={{ padding: '16px', border: '1px solid #ddd', borderRadius: '8px', marginTop: '16px' }}>
      <h3>Incidents for {service} ({env})</h3>
      <button onClick={fetchIncidents} disabled={loading} style={{ marginBottom: '12px' }}>
        {loading ? 'Loading...' : 'Load Incidents'}
      </button>
      {incidents.length === 0 && !loading && (
        <p style={{ color: '#666' }}>No incidents found. Click "Load Incidents" to check.</p>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
        {incidents.map((inc) => (
          <div
            key={inc.id}
            style={{
              padding: '12px',
              border: '1px solid #e0e0e0',
              borderRadius: '4px',
              borderLeft: `4px solid ${getSeverityColor(inc.severity)}`,
            }}
          >
            <strong>{inc.title}</strong>
            <span style={{ marginLeft: '8px', fontSize: '12px', color: '#666' }}>
              {inc.severity}
            </span>
            <p style={{ margin: '4px 0', fontSize: '14px' }}>{inc.root_cause}</p>
            <p style={{ margin: '4px 0', fontSize: '12px', color: '#666' }}>
              Resolution: {inc.resolution}
            </p>
            <p style={{ margin: '4px 0', fontSize: '11px', color: '#999' }}>
              Occurred: {formatDate(inc.occurred_at)}
            </p>
          </div>
        ))}
      </div>
    </div>
  );
};

function getSeverityColor(severity: string): string {
  switch (severity.toUpperCase()) {
    case 'P0': return '#F44336';
    case 'P1': return '#FF5722';
    case 'P2': return '#FF9800';
    case 'P3': return '#FFC107';
    default: return '#9E9E9E';
  }
}

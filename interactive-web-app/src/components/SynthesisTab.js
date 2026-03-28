import React, { useState, useEffect, useMemo } from 'react';
import { t } from '../i18n';

export default function SynthesisTab({ state }) {
  const agents = useMemo(() => state?.agents || [], [state?.agents]);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [synthesis, setSynthesis] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (agents.length > 0 && !selectedAgent) {
      setSelectedAgent(agents[0].name);
    }
  }, [agents, selectedAgent]);

  useEffect(() => {
    if (!selectedAgent) return;
    setLoading(true);
    setError(null);
    fetch('/api/agents/' + encodeURIComponent(selectedAgent) + '/synthesis')
      .then(res => {
        if (!res.ok) throw new Error(res.statusText);
        return res.json();
      })
      .then(data => {
        setSynthesis(data);
        setLoading(false);
      })
      .catch(e => {
        setError(e.message);
        setLoading(false);
      });
  }, [selectedAgent]);

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 20 }}>
        <h2 style={{ fontSize: 20, fontWeight: 600 }}>{t('synthesis_title')}</h2>
        <select
          className="task-agent-select"
          value={selectedAgent}
          onChange={(e) => setSelectedAgent(e.target.value)}
        >
          {agents.map(a => (
            <option key={a.name} value={a.name}>{a.name}</option>
          ))}
        </select>
      </div>

      {loading && <p className="empty">{t('loading')}</p>}
      {error && <p className="empty" style={{ color: 'var(--red)' }}>Error: {error}</p>}
      {!loading && !error && !synthesis && (
        <p className="empty">{t('synthesis_empty')}</p>
      )}

      {synthesis && (
        <div>
          {/* Status overview */}
          <div className="memory-stats" style={{ marginBottom: 20 }}>
            <div className="mem-stat-card">
              <div className="val" style={{ color: synthesis.status === 'active' ? 'var(--green)' : 'var(--dim)' }}>
                {synthesis.status || 'unknown'}
              </div>
              <div className="lbl">{t('synthesis_status')}</div>
            </div>
            {synthesis.last_run && (
              <div className="mem-stat-card">
                <div className="val" style={{ fontSize: 14 }}>
                  {new Date(synthesis.last_run).toLocaleString()}
                </div>
                <div className="lbl">{t('synthesis_last_run')}</div>
              </div>
            )}
            {synthesis.cycles !== undefined && (
              <div className="mem-stat-card">
                <div className="val">{synthesis.cycles}</div>
                <div className="lbl">{t('synthesis_cycles')}</div>
              </div>
            )}
          </div>

          {/* Active Learnings */}
          {synthesis.learnings && synthesis.learnings.length > 0 && (
            <div className="collective-section">
              <h3>{t('synthesis_learnings')}</h3>
              {synthesis.learnings.map((learning, i) => (
                <div key={i} style={{
                  background: 'var(--card)', border: '1px solid var(--border)',
                  borderRadius: 'var(--radius)', padding: '12px 14px', marginBottom: 8
                }}>
                  <div style={{ fontSize: 13, marginBottom: 4 }}>
                    {learning.insight || learning.summary || learning.description || JSON.stringify(learning)}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--dim)', display: 'flex', gap: 12 }}>
                    {learning.source && <span>Source: {learning.source}</span>}
                    {learning.confidence !== undefined && (
                      <span>Confidence: {(learning.confidence * 100).toFixed(0)}%</span>
                    )}
                    {learning.timestamp && (
                      <span>{new Date(learning.timestamp).toLocaleString()}</span>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Raw data fallback */}
          {!synthesis.learnings && synthesis.data && (
            <div style={{
              background: 'var(--bg)', borderRadius: 'var(--radius)',
              padding: '12px 14px', fontSize: 13, whiteSpace: 'pre-wrap', wordBreak: 'break-word'
            }}>
              {JSON.stringify(synthesis.data, null, 2)}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

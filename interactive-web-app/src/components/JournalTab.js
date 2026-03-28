import React, { useState, useEffect, useMemo } from 'react';
import { t } from '../i18n';

const TYPE_ICONS = {
  strategy_update: '🧭',
  skill_learned: '🎓',
  reputation_change: '⭐',
  peer_interaction: '🤝',
  task_completed: '✅',
  mood_shift: '🎭',
  insight: '💡',
  error: '❌',
};

export default function JournalTab({ state }) {
  const agents = useMemo(() => state?.agents || [], [state?.agents]);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [journal, setJournal] = useState(null);
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
    fetch('/api/agents/' + encodeURIComponent(selectedAgent) + '/journal')
      .then(res => {
        if (!res.ok) throw new Error(res.statusText);
        return res.json();
      })
      .then(data => {
        setJournal(data);
        setLoading(false);
      })
      .catch(e => {
        setError(e.message);
        setLoading(false);
      });
  }, [selectedAgent]);

  // Group entries by date
  const entries = journal?.entries || journal || [];
  const grouped = {};
  (Array.isArray(entries) ? entries : []).forEach(entry => {
    const date = entry.timestamp ? new Date(entry.timestamp).toLocaleDateString() : 'Unknown';
    if (!grouped[date]) grouped[date] = [];
    grouped[date].push(entry);
  });

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 20 }}>
        <h2 style={{ fontSize: 20, fontWeight: 600 }}>{t('journal_title')}</h2>
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
      {!loading && !error && Object.keys(grouped).length === 0 && (
        <p className="empty">{t('journal_empty')}</p>
      )}

      {Object.entries(grouped).map(([date, items]) => (
        <div key={date} style={{ marginBottom: 24 }}>
          <div style={{
            fontSize: 12, fontWeight: 600, color: 'var(--dim)',
            textTransform: 'uppercase', letterSpacing: '0.05em',
            marginBottom: 10, paddingBottom: 6, borderBottom: '1px solid var(--border)'
          }}>
            {date}
          </div>
          {items.map((entry, i) => (
            <div key={i} style={{
              display: 'flex', gap: 12, padding: '10px 0',
              borderBottom: '1px solid var(--border)', alignItems: 'flex-start'
            }}>
              <span style={{ fontSize: 18, flexShrink: 0 }}>
                {TYPE_ICONS[entry.type] || '📝'}
              </span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, marginBottom: 2 }}>
                  {entry.summary || entry.description || entry.message || JSON.stringify(entry)}
                </div>
                <div style={{ fontSize: 11, color: 'var(--dim)', display: 'flex', gap: 12 }}>
                  {entry.type && (
                    <span style={{
                      fontSize: 10, padding: '1px 6px', borderRadius: 4,
                      background: 'var(--border)', color: 'var(--dim)'
                    }}>
                      {entry.type}
                    </span>
                  )}
                  {entry.timestamp && (
                    <span>{new Date(entry.timestamp).toLocaleTimeString()}</span>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

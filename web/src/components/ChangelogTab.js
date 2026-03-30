import React, { useState } from 'react';
import { t } from '../i18n';

const TYPE_ICONS = {
  skill_new: '🆕',
  skill_improved: '🔧',
  skill_fixed: '🩹',
  agent_spawned: '🐣',
  agent_stopped: '💀',
  collective_synthesis: '🧠',
  config_change: '⚙️',
  human_feedback: '👤',
};

function groupByDate(entries) {
  const groups = {};
  (entries || []).forEach(e => {
    const date = e.timestamp ? e.timestamp.slice(0, 10) : 'unknown';
    if (!groups[date]) groups[date] = [];
    groups[date].push(e);
  });
  return groups;
}

export default function ChangelogTab({ state }) {
  const [expanded, setExpanded] = useState({});
  const entries = state?.changelog || [];

  if (!entries.length) {
    return <div className="empty">{t('no_entries')}</div>;
  }

  const grouped = groupByDate(entries);

  return (
    <div className="changelog-tab">
      <h3>{t('changelog_title')}</h3>
      {Object.entries(grouped).map(([date, items]) => (
        <div key={date} className="changelog-group">
          <h4
            className="changelog-date"
            onClick={() => setExpanded(prev => ({ ...prev, [date]: !prev[date] }))}
            style={{ cursor: 'pointer' }}
          >
            {expanded[date] ? '▼' : '▶'} {date} ({items.length})
          </h4>
          {(expanded[date] !== false) && (
            <ul className="changelog-list">
              {items.map((e, i) => (
                <li key={i} className={`changelog-entry type-${e.type}`}>
                  <span className="changelog-icon">{TYPE_ICONS[e.type] || '📝'}</span>
                  <span className="changelog-type">{e.type}</span>
                  <span className="changelog-agent">{e.agent}</span>
                  <span className="changelog-summary">{e.summary}</span>
                  {e.details && <div className="changelog-details">{e.details}</div>}
                  {e.tags && e.tags.length > 0 && (
                    <div className="changelog-tags">
                      {e.tags.map((tag, j) => <span key={j} className="tag">{tag}</span>)}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      ))}
    </div>
  );
}

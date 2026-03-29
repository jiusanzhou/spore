import React, { useState } from 'react';
import { t } from '../i18n';

export default function ActivityTab({ state }) {
  const tasks = (state?.tasks || []).slice().sort((a, b) => {
    const ta = a.completed_at || a.submitted_at || '';
    const tb = b.completed_at || b.submitted_at || '';
    return new Date(tb) - new Date(ta);
  });

  if (!tasks.length) {
    return <p className="empty">{t('no_activity')}</p>;
  }

  return (
    <div className="activity-feed">
      {tasks.map(task => (
        <ActivityItem key={task.id || task.description} task={task} />
      ))}
    </div>
  );
}

function ActivityItem({ task }) {
  const [expanded, setExpanded] = useState(false);
  const status = task.status || 'queued';
  const ts = task.completed_at || task.submitted_at || '';
  const runtime = task.runtime
    ? (typeof task.runtime === 'number' ? task.runtime.toFixed(1) + 's' : task.runtime)
    : '';
  const hasResult = task.result && task.result.trim();
  const hasError = task.error && task.error.trim();
  const expandable = hasResult || hasError;

  return (
    <div
      className={'activity-item' + (expandable ? ' expandable' : '')}
      onClick={expandable ? () => setExpanded(prev => !prev) : undefined}
    >
      <span className="activity-agent">{task.agent}</span>
      <div className="activity-body">
        <div className="activity-desc">
          {task.description}
          {expandable && <span className="expand-hint">{expanded ? '▾' : '▸'}</span>}
        </div>
        <div className="activity-meta">
          <span className={`task-status task-${status}`}>{t(status)}</span>
          {runtime && <span>{runtime}</span>}
          <span>{relTime(ts)}</span>
        </div>
        {expandable && expanded && (
          <div className="activity-result">
            {hasResult ? task.result : task.error}
          </div>
        )}
      </div>
    </div>
  );
}

function relTime(ts) {
  if (!ts) return '';
  const diff = (Date.now() - new Date(ts).getTime()) / 1000;
  if (diff < 0) return 'just now';
  if (diff < 60) return Math.floor(diff) + 's ago';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  return Math.floor(diff / 86400) + 'd ago';
}

import React, { useState, useCallback } from 'react';
import { t } from '../i18n';

export default function TaskBar({ agents }) {
  const [agent, setAgent] = useState('');
  const [input, setInput] = useState('');
  const [feedback, setFeedback] = useState({ text: '', type: '' });
  const [submitting, setSubmitting] = useState(false);

  const submit = useCallback(async () => {
    const desc = input.trim();
    if (!desc) return;

    setSubmitting(true);
    setFeedback({ text: t('submitting'), type: '' });

    try {
      const body = { description: desc };
      if (agent) body.agent = agent;

      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const err = await res.text();
        throw new Error(err);
      }

      const data = await res.json();
      setFeedback({
        text: `${t('task_queued')} ${data.agent} (${data.task_id.substring(0, 8)})`,
        type: 'success',
      });
      setInput('');
    } catch (e) {
      setFeedback({ text: '✗ ' + e.message, type: 'error' });
    } finally {
      setSubmitting(false);
      setTimeout(() => setFeedback({ text: '', type: '' }), 5000);
    }
  }, [input, agent]);

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="task-bar">
      <div className="task-bar-inner">
        <select
          className="task-agent-select"
          value={agent}
          onChange={(e) => setAgent(e.target.value)}
        >
          <option value="">{t('swarm_auto')}</option>
          {(agents || []).map(a => (
            <option key={a.name} value={a.name}>{a.name}</option>
          ))}
        </select>
        <input
          type="text"
          className="task-input"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('task_placeholder')}
          autoComplete="off"
        />
        <button
          className="task-submit-btn"
          onClick={submit}
          disabled={submitting}
          title="Submit task"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M2 8h12M8 2l6 6-6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        </button>
      </div>
      {feedback.text && (
        <div className={'task-feedback' + (feedback.type ? ' ' + feedback.type : '')}>
          {feedback.text}
        </div>
      )}
    </div>
  );
}

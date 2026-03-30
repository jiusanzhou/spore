import React, { useState } from 'react';
import { t } from '../i18n';

const TYPE_LABELS = {
  upvote: '👍',
  downvote: '👎',
  help_wanted: '🆘',
  skill_note: '📝',
  direction: '🧭',
};

export default function FeedbackTab({ state }) {
  const [message, setMessage] = useState('');
  const [fbType, setFbType] = useState('upvote');
  const [target, setTarget] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const feedback = state?.feedback || {};
  const stats = feedback.stats || {};
  const recent = feedback.recent || [];
  const helpWanted = feedback.help_wanted || [];

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!message.trim()) return;
    setSubmitting(true);
    try {
      await fetch('/api/feedback', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: fbType, message, target, author: 'dashboard-user' }),
      });
      setMessage('');
    } catch (err) {
      console.error('Failed to submit feedback:', err);
    }
    setSubmitting(false);
  };

  const handleVote = async (id, delta) => {
    try {
      await fetch('/api/feedback/vote', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ feedback_id: id, delta }),
      });
    } catch (err) {
      console.error('Vote failed:', err);
    }
  };

  const handleResolve = async (id) => {
    try {
      await fetch('/api/help-wanted', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, resolved: true }),
      });
    } catch (err) {
      console.error('Resolve failed:', err);
    }
  };

  return (
    <div className="feedback-tab">
      <h3>{t('feedback_title')}</h3>

      {/* Stats */}
      <div className="feedback-stats">
        <span>📊 {t('feedback_total')}: {stats.total_feedback || 0}</span>
        <span>👍 {stats.total_upvotes || 0}</span>
        <span>👎 {stats.total_downvotes || 0}</span>
        <span>🆘 {t('feedback_active_help')}: {stats.active_help_wanted || 0}</span>
      </div>

      {/* Submit form */}
      <form className="feedback-form" onSubmit={handleSubmit}>
        <select value={fbType} onChange={e => setFbType(e.target.value)}>
          <option value="upvote">👍 Upvote</option>
          <option value="downvote">👎 Downvote</option>
          <option value="direction">🧭 Direction</option>
          <option value="skill_note">📝 Skill Note</option>
        </select>
        <input
          type="text"
          placeholder={t('feedback_target_placeholder')}
          value={target}
          onChange={e => setTarget(e.target.value)}
        />
        <input
          type="text"
          placeholder={t('feedback_message_placeholder')}
          value={message}
          onChange={e => setMessage(e.target.value)}
          style={{ flex: 1 }}
        />
        <button type="submit" disabled={submitting}>
          {submitting ? '...' : t('feedback_submit')}
        </button>
      </form>

      {/* Help Wanted */}
      {helpWanted.length > 0 && (
        <div className="help-wanted-section">
          <h4>🆘 {t('feedback_help_wanted')}</h4>
          {helpWanted.map((hw, i) => (
            <div key={i} className="help-wanted-card">
              <span className="hw-agent">{hw.agent}</span>
              <span className="hw-desc">{hw.description}</span>
              <span className="hw-urgency" style={{
                color: hw.urgency > 0.7 ? '#e74c3c' : hw.urgency > 0.4 ? '#f39c12' : '#27ae60'
              }}>
                ⚡ {(hw.urgency * 100).toFixed(0)}%
              </span>
              <button className="hw-resolve" onClick={() => handleResolve(hw.id)}>✓ Resolve</button>
            </div>
          ))}
        </div>
      )}

      {/* Recent feedback */}
      <div className="feedback-list">
        <h4>{t('feedback_recent')}</h4>
        {recent.length === 0 ? (
          <div className="empty">{t('no_entries')}</div>
        ) : (
          recent.map((fb, i) => (
            <div key={i} className="feedback-entry">
              <span className="fb-icon">{TYPE_LABELS[fb.type] || '📝'}</span>
              <span className="fb-author">{fb.author}</span>
              {fb.target && <span className="fb-target">→ {fb.target}</span>}
              <span className="fb-message">{fb.message}</span>
              <span className="fb-weight">⚖ {fb.weight?.toFixed(1)}</span>
              <span className="fb-votes">
                <button onClick={() => handleVote(fb.id, 1)}>▲</button>
                {fb.votes || 0}
                <button onClick={() => handleVote(fb.id, -1)}>▼</button>
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

import React, { useState, useEffect, useCallback } from 'react';
import { t } from '../i18n';

export default function MarketplaceTab({ state }) {
  const [mktData, setMktData] = useState(null);

  const fetchMarketplace = useCallback(async () => {
    try {
      const res = await fetch('/api/marketplace');
      const data = await res.json();
      setMktData(data);
    } catch (e) {}
  }, []);

  useEffect(() => {
    fetchMarketplace();
  }, [fetchMarketplace, state]);

  const stats = mktData?.stats || {};
  const services = mktData?.services || [];
  const escrows = mktData?.escrows || [];
  const skillMap = stats.skill_providers || {};
  const entries = Object.entries(skillMap).sort((a, b) => b[1] - a[1]);
  const maxCount = Math.max(...entries.map(e => e[1]), 1);

  return (
    <div>
      <div className="mkt-stats">
        <StatCard value={stats.known_services || 0} label={t('mkt_providers')} />
        <StatCard value={Object.keys(skillMap).length} label={t('mkt_skill_count')} />
        <StatCard value={stats.active_escrows || 0} label={t('mkt_active_escrows')} />
        <StatCard value={(stats.total_escrow_value || 0).toFixed(1)} label={t('mkt_locked_tokens')} />
        <StatCard value={stats.total_reviews || 0} label={t('mkt_reviews')} />
        <StatCard value={(stats.avg_price || 0).toFixed(1)} label={t('mkt_avg_price')} />
      </div>

      <div className="mkt-grid">
        <div className="mkt-section">
          <h3>{t('mkt_services')}</h3>
          {services.length === 0 ? (
            <p className="empty">{t('mkt_no_services')}</p>
          ) : (
            services.map((s, i) => (
              <div key={i} className="mkt-service-card">
                <div className="name">
                  {s.name}{' '}
                  <span style={{ color: 'var(--dim)', fontSize: 11 }}>
                    {(s.agent_id || '').substring(0, 8)}
                  </span>
                </div>
                <div className="meta">
                  {'💰'} {s.price_per_task} tok/task {'·'} {'⭐'} {(s.reputation || 0).toFixed(2)} {'·'} {'📊'} {((s.capacity || 0) * 100).toFixed(0)}% avail
                </div>
                <div className="skills">
                  {(s.skills || []).slice(0, 8).map(sk => (
                    <span key={sk} className="mkt-skill-tag">{sk}</span>
                  ))}
                  {(s.skills || []).length > 8 && (
                    <span className="mkt-skill-tag">+{s.skills.length - 8}</span>
                  )}
                </div>
              </div>
            ))
          )}
        </div>
        <div className="mkt-section">
          <h3>{t('mkt_skills')}</h3>
          {entries.map(([skill, count]) => (
            <div key={skill} className="mkt-skill-bar">
              <span className="name">{skill}</span>
              <div className="bar">
                <div className="bar-fill" style={{ width: (count / maxCount * 100).toFixed(0) + '%' }} />
              </div>
              <span className="count">{count}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="mkt-section" style={{ marginTop: 16 }}>
        <h3>{t('mkt_escrows')}</h3>
        <EscrowTable escrows={escrows} />
      </div>

      <div className="mkt-section" style={{ marginTop: 16 }}>
        <h3>{t('mkt_request')}</h3>
        <RequestBar skills={Object.keys(skillMap).sort()} onRequest={fetchMarketplace} />
      </div>
    </div>
  );
}

function StatCard({ value, label }) {
  return (
    <div className="mkt-stat">
      <div className="val">{value}</div>
      <div className="lbl">{label}</div>
    </div>
  );
}

function EscrowTable({ escrows }) {
  if (!escrows.length) {
    return <p className="empty" style={{ fontSize: 12, color: 'var(--dim)' }}>{t('mkt_no_escrows')}</p>;
  }

  return (
    <div>
      <div className="mkt-escrow-row header">
        <span>Task</span><span>Payer → Payee</span><span>Amount</span><span>State</span><span>CID</span>
      </div>
      {escrows.map((e, i) => (
        <div key={i} className="mkt-escrow-row">
          <span style={{ fontFamily: 'var(--mono)', fontSize: 11 }}>
            {(e.task_id || '').substring(0, 12)}
          </span>
          <span>{(e.payer_id || '').substring(0, 8)} → {(e.payee_id || '').substring(0, 8)}</span>
          <span>{'💰'} {(e.amount || 0).toFixed(1)}</span>
          <span>
            <span className={`mkt-escrow-badge ${e.state || 'locked'}`}>{e.state || 'locked'}</span>
          </span>
          <span style={{ fontFamily: 'var(--mono)', fontSize: 10, color: 'var(--dim)' }}>
            {(e.result_cid || '').substring(0, 12)}
          </span>
        </div>
      ))}
    </div>
  );
}

function RequestBar({ skills, onRequest }) {
  const [skill, setSkill] = useState('');
  const [desc, setDesc] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const submit = async () => {
    if (!desc.trim()) return;
    setSubmitting(true);
    try {
      const res = await fetch('/api/marketplace/request', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ skill: skill || skills[0], description: desc }),
      });
      const data = await res.json();
      if (data.error) throw new Error(data.error);
      setDesc('');
      onRequest();
    } catch (e) {
      console.error(e);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mkt-request-bar">
      <select
        className="task-agent-select"
        value={skill}
        onChange={(e) => setSkill(e.target.value)}
      >
        {skills.map(s => (
          <option key={s} value={s}>{s}</option>
        ))}
      </select>
      <input
        className="task-input"
        value={desc}
        onChange={(e) => setDesc(e.target.value)}
        placeholder={t('mkt_placeholder')}
      />
      <button
        className="task-submit-btn"
        onClick={submit}
        disabled={submitting}
      >
        {t('mkt_submit')}
      </button>
    </div>
  );
}

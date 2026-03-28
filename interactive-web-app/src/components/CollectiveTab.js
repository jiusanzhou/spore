import React from 'react';
import { t } from '../i18n';

export default function CollectiveTab({ state }) {
  const agents = state?.agents || [];
  if (!agents.length) {
    return <p className="empty">No collective data</p>;
  }

  const c = agents[0]?.collective || {};
  const avgEnergy = Math.round((c.average_energy || 0) * 100);
  const avgMorale = Math.round((c.average_morale || 0) * 100);

  return (
    <div>
      <div className="collective-header">
        <h2>{c.swarm_personality || 'Swarm'}</h2>
        <p>{c.swarm_purpose || ''}</p>
      </div>

      {c.narrative && (
        <div className="collective-section">
          <h3>{t('swarm_narrative')}</h3>
          <p style={{ fontSize: 13, color: 'var(--dim)', maxWidth: 600 }}>{c.narrative}</p>
        </div>
      )}

      <div className="collective-section">
        <h3>{t('emergent_roles')}</h3>
        {c.explorers?.length > 0 && <RoleGroup label={t('explorers')} names={c.explorers} cls="role-tag-explorer" />}
        {c.creators?.length > 0 && <RoleGroup label={t('creators')} names={c.creators} cls="role-tag-creator" />}
        {c.connectors?.length > 0 && <RoleGroup label={t('connectors')} names={c.connectors} cls="role-tag-connector" />}
      </div>

      <div className="collective-section">
        <h3>{t('mood')}</h3>
        <div className="mood-bars">
          <div className="progress-row">
            <label>{t('avg_energy')}</label>
            <div className="progress-bar">
              <div className="progress-fill" style={{ width: avgEnergy + '%', background: 'var(--amber)' }} />
            </div>
            <span style={{ fontSize: 11, color: 'var(--dim)', width: 32, textAlign: 'right' }}>{avgEnergy}%</span>
          </div>
          <div className="progress-row">
            <label>{t('avg_morale')}</label>
            <div className="progress-bar">
              <div className="progress-fill" style={{ width: avgMorale + '%', background: 'var(--green)' }} />
            </div>
            <span style={{ fontSize: 11, color: 'var(--dim)', width: 32, textAlign: 'right' }}>{avgMorale}%</span>
          </div>
        </div>
      </div>
    </div>
  );
}

function RoleGroup({ label, names, cls }) {
  return (
    <div className="role-group">
      <div className="role-group-label">{label}</div>
      <div className="role-tags">
        {names.map(n => (
          <span key={n} className={`skill-tag ${cls}`}>{n}</span>
        ))}
      </div>
    </div>
  );
}

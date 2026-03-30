import React, { useState, useEffect } from 'react';
import { t } from '../i18n';

export default function CatalogTab({ state }) {
  const [query, setQuery] = useState('');
  const [catalog, setCatalog] = useState(null);
  const [installing, setInstalling] = useState(null);
  const [selectedAgent, setSelectedAgent] = useState('');

  const agents = state?.agents || [];

  // Auto-select first agent
  useEffect(() => {
    if (!selectedAgent && agents.length > 0) {
      setSelectedAgent(agents[0].name);
    }
  }, [agents, selectedAgent]);

  // Fetch catalog for selected agent
  useEffect(() => {
    if (!selectedAgent) return;
    const url = `/api/agents/${selectedAgent}/catalog${query ? `?q=${encodeURIComponent(query)}` : ''}`;
    fetch(url)
      .then(r => r.json())
      .then(data => setCatalog(data))
      .catch(() => setCatalog(null));
  }, [selectedAgent, query]);

  const handleInstall = async (skillName) => {
    setInstalling(skillName);
    try {
      await fetch(`/api/agents/${selectedAgent}/catalog`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ skill_name: skillName }),
      });
    } catch (err) {
      console.error('Install failed:', err);
    }
    setInstalling(null);
  };

  const skills = catalog?.skills || catalog?.unique_skills || [];

  return (
    <div className="catalog-tab">
      <h3>{t('catalog_title')}</h3>

      <div className="catalog-controls">
        <select value={selectedAgent} onChange={e => setSelectedAgent(e.target.value)}>
          {agents.map(a => <option key={a.name} value={a.name}>{a.name}</option>)}
        </select>
        <input
          type="text"
          placeholder={t('catalog_search_placeholder')}
          value={query}
          onChange={e => setQuery(e.target.value)}
        />
      </div>

      {catalog?.total !== undefined && (
        <div className="catalog-stats">
          {t('catalog_total')}: {catalog.total}
          {catalog.installable !== undefined && ` (${catalog.installable} ${t('catalog_installable')})`}
        </div>
      )}

      {skills.length === 0 ? (
        <div className="empty">{t('catalog_empty')}</div>
      ) : (
        <div className="catalog-grid">
          {skills.map((skill, i) => (
            <div key={i} className="catalog-card">
              <div className="catalog-card-header">
                <span className="skill-name">{skill.name || skill.skill_name}</span>
                {skill.category && <span className="skill-category">{skill.category}</span>}
              </div>
              {skill.description && (
                <div className="skill-description">{skill.description}</div>
              )}
              <div className="catalog-card-footer">
                <span className="skill-provider">
                  {skill.provider_count > 1
                    ? `${skill.provider_count} providers`
                    : skill.agent_id || skill.provider_id || '—'
                  }
                </span>
                {skill.reputation > 0 && (
                  <span className="skill-rep">⭐ {skill.reputation.toFixed(1)}</span>
                )}
                {skill.has_cid && (
                  <button
                    className="install-btn"
                    disabled={installing === (skill.name || skill.skill_name)}
                    onClick={() => handleInstall(skill.name || skill.skill_name)}
                  >
                    {installing === (skill.name || skill.skill_name) ? '...' : '📥 Install'}
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

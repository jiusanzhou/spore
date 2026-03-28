import React, { useState, useEffect, useCallback } from 'react';
import RadarChart from './RadarChart';
import { t } from '../i18n';

export default function AgentCard({ agent, stats }) {
  const [expanded, setExpanded] = useState(false);
  const [drawerTab, setDrawerTab] = useState('awareness');
  const [contextData, setContextData] = useState(null);
  const [contextFilter, setContextFilter] = useState(null);
  const [contextLoading, setContextLoading] = useState(false);
  const [openEntries, setOpenEntries] = useState(new Set());

  const a = agent;
  const status = a.status || 'idle';
  const health = a.economy?.health || 'good';

  const toggleExpand = () => {
    setExpanded(prev => !prev);
    if (!expanded) setDrawerTab('awareness');
  };

  const handleDrawerTab = (tab) => {
    setDrawerTab(tab);
    if (tab === 'context' && !contextData) {
      loadContext();
    }
  };

  const loadContext = useCallback(async () => {
    setContextLoading(true);
    try {
      const res = await fetch('/api/agents/' + encodeURIComponent(a.name) + '/context');
      if (!res.ok) throw new Error(res.statusText);
      const data = await res.json();
      setContextData(data);
    } catch (e) {
      setContextData({ error: e.message, entries: [], stats: {} });
    } finally {
      setContextLoading(false);
    }
  }, [a.name]);

  useEffect(() => {
    if (drawerTab === 'context' && !contextData) {
      loadContext();
    }
  }, [drawerTab, contextData, loadContext]);

  const toggleEntry = (uri) => {
    setOpenEntries(prev => {
      const next = new Set(prev);
      if (next.has(uri)) next.delete(uri);
      else next.add(uri);
      return next;
    });
  };

  return (
    <div className="agent-card" data-expanded={expanded}>
      <div className="agent-card-header" onClick={toggleExpand}>
        <div>
          <span className="agent-name">{a.name}</span>
          <span className="agent-personality">{a.self?.personality || a.role || ''}</span>
        </div>
        <span className={`status-badge status-${status}`}>{t(status)}</span>
      </div>
      <div className="agent-card-body" onClick={toggleExpand}>
        <div className="radar-wrap">
          <RadarChart drives={a.drives} />
        </div>
        <div className="skills-economy">
          <div className="skill-tags">
            {(a.skills || []).map(s => (
              <span key={s} className="skill-tag">{s}</span>
            ))}
          </div>
          <div className="economy-line">
            <strong>{(a.economy?.balance || 0).toFixed(1)}</strong> {t('credits')}
            {' '}<span className={`health-badge health-${health}`}>{health}</span>
          </div>
        </div>
      </div>

      {expanded && (
        <div className="agent-drawer" style={{ display: 'block' }}>
          <div className="drawer-tabs">
            {['awareness', 'evolution', 'context'].map(tab => (
              <button
                key={tab}
                className={'drawer-tab' + (drawerTab === tab ? ' active' : '')}
                onClick={(e) => { e.stopPropagation(); handleDrawerTab(tab); }}
              >
                {t(tab === 'context' ? 'context_memory' : tab)}
              </button>
            ))}
          </div>

          {drawerTab === 'awareness' && <AwarenessPanel agent={a} />}
          {drawerTab === 'evolution' && <EvolutionPanel agent={a} stats={stats} />}
          {drawerTab === 'context' && (
            <ContextPanel
              data={contextData}
              loading={contextLoading}
              filter={contextFilter}
              setFilter={setContextFilter}
              openEntries={openEntries}
              toggleEntry={toggleEntry}
            />
          )}
        </div>
      )}
    </div>
  );
}

function AwarenessPanel({ agent }) {
  const s = agent.self || {};
  const mood = (s.mood || 0) * 100;
  const energy = (s.energy || 0) * 100;
  const morale = (s.morale || 0) * 100;

  return (
    <div className="drawer-panel active">
      <div className="awareness-grid">
        <div className="awareness-item">
          <label>{t('purpose')}</label>
          <div className="val">{s.purpose || '-'}</div>
        </div>
        <div className="awareness-item">
          <label>{t('decision_style')}</label>
          <div className="val">{s.decision_style || '-'}</div>
        </div>
        <div className="awareness-item awareness-full">
          <label>{t('narrative')}</label>
          <div className="val">{s.narrative || '-'}</div>
        </div>
      </div>
      <div style={{ marginTop: 12 }}>
        <ProgressRow label={t('mood')} value={mood} color="var(--accent)" />
        <ProgressRow label={t('energy')} value={energy} color="var(--amber)" />
        <ProgressRow label={t('morale')} value={morale} color="var(--green)" />
      </div>
      <div style={{ marginTop: 12, display: 'flex', gap: 24 }}>
        <div>
          <label style={{ fontSize: 10, color: 'var(--dim)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            {t('strengths')}
          </label>
          <div className="tag-list" style={{ marginTop: 4 }}>
            {(s.strengths || []).map(x => (
              <span key={x} className="tag-sm" style={{ background: 'rgba(16,185,129,0.1)', color: 'var(--green)' }}>{x}</span>
            ))}
          </div>
        </div>
        <div>
          <label style={{ fontSize: 10, color: 'var(--dim)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            {t('weaknesses')}
          </label>
          <div className="tag-list" style={{ marginTop: 4 }}>
            {(s.weaknesses || []).map(x => (
              <span key={x} className="tag-sm" style={{ background: 'rgba(239,68,68,0.1)', color: 'var(--red)' }}>{x}</span>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function EvolutionPanel({ agent, stats }) {
  let evo = {};
  if (typeof agent.evolution === 'string') {
    try { evo = JSON.parse(agent.evolution); } catch (e) {}
  } else if (typeof agent.evolution === 'object' && agent.evolution) {
    evo = agent.evolution;
  }

  const xp = evo.experience_count || agent.task_count || 0;
  const completed = stats?.total_tasks_completed || 0;
  const failed = stats?.total_tasks_failed || 0;
  const total = completed + failed;
  const rate = total > 0 ? Math.round((completed / total) * 100) : 0;
  const skills = evo.skill_confidence || {};

  return (
    <div className="drawer-panel active">
      <div className="evo-stats">
        <div className="evo-stat">{t('experience')}: <strong>{xp}</strong></div>
        <div className="evo-stat">{t('success_rate')}: <strong>{rate}%</strong></div>
      </div>
      {Object.keys(skills).length > 0 ? (
        Object.entries(skills).map(([k, v]) => {
          const pct = (typeof v === 'number' ? v : 0) * 100;
          return (
            <div key={k} className="confidence-row">
              <span className="skill-name">{k}</span>
              <div className="progress-bar">
                <div className="progress-fill" style={{ width: pct + '%', background: 'var(--cyan)' }} />
              </div>
              <span style={{ fontSize: 10, color: 'var(--dim)', width: 32, textAlign: 'right' }}>
                {Math.round(pct)}%
              </span>
            </div>
          );
        })
      ) : (
        <p style={{ fontSize: 12, color: 'var(--dim)' }}>{t('no_entries')}</p>
      )}
    </div>
  );
}

function ContextPanel({ data, loading, filter, setFilter, openEntries, toggleEntry }) {
  const cats = ['profile', 'entities', 'cases', 'events', 'preferences', 'patterns'];

  if (loading) return <p className="ctx-loading">{t('loading')}</p>;
  if (!data) return null;
  if (data.error) return <p className="ctx-loading">Failed to load: {data.error}</p>;

  const entries = data.entries || [];
  const stats = data.stats || {};
  const filtered = filter ? entries.filter(e => e.category === filter) : entries;

  return (
    <div className="drawer-panel active">
      <div className="ctx-filters">
        <button
          className={'ctx-filter-btn' + (!filter ? ' active' : '')}
          onClick={(e) => { e.stopPropagation(); setFilter(null); }}
        >
          All
        </button>
        {cats.map(c => (
          <button
            key={c}
            className={'ctx-filter-btn' + (filter === c ? ' active' : '')}
            onClick={(e) => { e.stopPropagation(); setFilter(c); }}
          >
            {c} ({stats[c] || 0})
          </button>
        ))}
      </div>
      <div className="ctx-entries">
        {filtered.length === 0 ? (
          <p className="ctx-loading">{t('no_entries')}</p>
        ) : (
          filtered.map((e, i) => (
            <div
              key={e.uri || i}
              className="ctx-entry"
              data-open={openEntries.has(e.uri)}
              onClick={(ev) => { ev.stopPropagation(); toggleEntry(e.uri); }}
            >
              <div className="ctx-entry-head">
                <span className="ctx-cat">{e.category}</span>
                <span className="ctx-l0">{e.l0}</span>
              </div>
              {e.l1 && openEntries.has(e.uri) && (
                <div className="ctx-l1" style={{ display: 'block' }}>{e.l1}</div>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function ProgressRow({ label, value, color }) {
  return (
    <div className="progress-row">
      <label>{label}</label>
      <div className="progress-bar">
        <div className="progress-fill" style={{ width: value + '%', background: color }} />
      </div>
      <span style={{ fontSize: 11, color: 'var(--dim)', width: 32, textAlign: 'right' }}>
        {Math.round(value)}%
      </span>
    </div>
  );
}

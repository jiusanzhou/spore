import React from 'react';
import { t } from '../i18n';

export default function Header({ state, theme, toggleTheme, lang, toggleLang }) {
  const s = state?.stats || {};
  const n = state?.network || {};

  return (
    <header className="header">
      <div className="header-left">
        <span className="header-title">{t('title')}</span>
        <span className="badge badge-online">{t('online')}</span>
        <span className="badge badge-peers">{n.peers || 0} {t('peers')}</span>
      </div>
      <div className="header-right">
        <div className="header-stats">
          <div className="stat">
            <span className="stat-val">{s.total_agents || 0}</span>
            <span className="stat-label">{t('agents')}</span>
          </div>
          <div className="stat">
            <span className="stat-val">{(s.total_tasks_completed || 0) + (s.total_tasks_failed || 0)}</span>
            <span className="stat-label">{t('tasks')}</span>
          </div>
          <div className="stat">
            <span className="stat-val">{fmtUptime(s.uptime_seconds)}</span>
            <span className="stat-label">{t('uptime')}</span>
          </div>
          <div className="stat">
            <span className="stat-val">{state?.content?.stats?.items || 0}</span>
            <span className="stat-label">{t('content')}</span>
          </div>
        </div>
        <button className="lang-toggle" onClick={toggleLang} title="切换语言 / Switch language">
          {lang === 'zh' ? 'EN' : '中'}
        </button>
        <button className="theme-toggle" onClick={toggleTheme} title="Toggle theme">
          {theme === 'light' ? '☀️' : '🌙'}
        </button>
      </div>
    </header>
  );
}

function fmtUptime(sec) {
  if (!sec) return '0s';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  if (h > 0) return h + 'h ' + m + 'm';
  if (m > 0) return m + 'm ' + s + 's';
  return s + 's';
}

import React from 'react';
import { t } from '../i18n';

const TABS = [
  { key: 'agents', label: 'tab_agents' },
  { key: 'marketplace', label: 'tab_marketplace' },
  { key: 'catalog', label: 'tab_catalog' },
  { key: 'collective', label: 'tab_collective' },
  { key: 'memory', label: 'tab_memory' },
  { key: 'changelog', label: 'tab_changelog' },
  { key: 'feedback', label: 'tab_feedback' },
  { key: 'activity', label: 'tab_activity' },
  { key: 'journal', label: 'tab_journal' },
  { key: 'synthesis', label: 'tab_synthesis' },
];

export default function TabBar({ activeTab, onTabChange }) {
  return (
    <nav className="tab-bar">
      {TABS.map(tab => (
        <button
          key={tab.key}
          className={'tab-btn' + (activeTab === tab.key ? ' active' : '')}
          onClick={() => onTabChange(tab.key)}
        >
          {t(tab.label)}
        </button>
      ))}
    </nav>
  );
}

import React from 'react';
import AgentCard from './AgentCard';
import { t } from '../i18n';

export default function AgentsTab({ state }) {
  const agents = state?.agents || [];

  if (!agents.length) {
    return <p className="empty">{t('no_agents')}</p>;
  }

  return (
    <div className="agents-grid">
      {agents.map(a => (
        <AgentCard key={a.name} agent={a} stats={state?.stats} />
      ))}
    </div>
  );
}

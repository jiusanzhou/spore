import React, { useState, useCallback } from 'react';
import useSSE from './hooks/useSSE';
import useTheme from './hooks/useTheme';
import { getLang, setLang } from './i18n';
import Header from './components/Header';
import TabBar from './components/TabBar';
import TaskBar from './components/TaskBar';
import AgentsTab from './components/AgentsTab';
import MarketplaceTab from './components/MarketplaceTab';
import CollectiveTab from './components/CollectiveTab';
import MemoryTab from './components/MemoryTab';
import ActivityTab from './components/ActivityTab';
import JournalTab from './components/JournalTab';
import SynthesisTab from './components/SynthesisTab';
import './App.css';

function App() {
  const { state, connected, lastUpdate } = useSSE('/api/events');
  const { theme, toggleTheme } = useTheme();
  const [activeTab, setActiveTab] = useState('agents');
  const [lang, setLangState] = useState(getLang);

  const toggleLang = useCallback(() => {
    const next = lang === 'zh' ? 'en' : 'zh';
    setLang(next);
    setLangState(next);
  }, [lang]);

  return (
    <div className="app">
      <Header
        state={state}
        theme={theme}
        toggleTheme={toggleTheme}
        lang={lang}
        toggleLang={toggleLang}
      />
      <TabBar activeTab={activeTab} onTabChange={setActiveTab} />
      <TaskBar agents={state?.agents} />

      <section className="tab-content active">
        {activeTab === 'agents' && <AgentsTab state={state} />}
        {activeTab === 'marketplace' && <MarketplaceTab state={state} />}
        {activeTab === 'collective' && <CollectiveTab state={state} />}
        {activeTab === 'memory' && <MemoryTab state={state} />}
        {activeTab === 'activity' && <ActivityTab state={state} />}
        {activeTab === 'journal' && <JournalTab state={state} />}
        {activeTab === 'synthesis' && <SynthesisTab state={state} />}
      </section>

      <footer className="footer">
        <span className={`sse-dot ${connected ? 'sse-connected' : 'sse-disconnected'}`} />
        <span>{connected ? 'Connected' : 'Reconnecting\u2026'}</span>
        <span style={{ marginLeft: 'auto' }}>
          {lastUpdate && 'Updated ' + lastUpdate.toLocaleTimeString()}
        </span>
      </footer>
    </div>
  );
}

export default App;

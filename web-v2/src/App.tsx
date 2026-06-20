import { useState } from 'react'
import { useSSE } from './lib/sse'
import { useTheme } from './lib/theme'
import { AppShell } from './components/AppShell'
import { NetworkTab } from './components/tabs/NetworkTab'
import { AgentsTab } from './components/tabs/AgentsTab'
import { MarketplaceTab } from './components/tabs/MarketplaceTab'
import { CatalogTab } from './components/tabs/CatalogTab'
import { MemoryTab } from './components/tabs/MemoryTab'
import { CollectiveTab } from './components/tabs/CollectiveTab'
import { SynthesisTab } from './components/tabs/SynthesisTab'
import { ActivityTab } from './components/tabs/ActivityTab'
import { JournalTab } from './components/tabs/JournalTab'
import { ChangelogTab } from './components/tabs/ChangelogTab'
import { FeedbackTab } from './components/tabs/FeedbackTab'

export type TabId =
  | 'network'
  | 'agents'
  | 'marketplace'
  | 'catalog'
  | 'collective'
  | 'memory'
  | 'synthesis'
  | 'activity'
  | 'journal'
  | 'changelog'
  | 'feedback'

function App() {
  const { state, connected, lastUpdate } = useSSE('/api/events')
  const { theme, toggleTheme } = useTheme()
  const [tab, setTab] = useState<TabId>('network')

  return (
    <AppShell
      state={state}
      connected={connected}
      lastUpdate={lastUpdate}
      theme={theme}
      onToggleTheme={toggleTheme}
      tab={tab}
      onTabChange={setTab}
    >
      {tab === 'network' && <NetworkTab state={state} />}
      {tab === 'agents' && <AgentsTab state={state} />}
      {tab === 'marketplace' && <MarketplaceTab state={state} />}
      {tab === 'catalog' && <CatalogTab state={state} />}
      {tab === 'collective' && <CollectiveTab state={state} />}
      {tab === 'memory' && <MemoryTab state={state} />}
      {tab === 'synthesis' && <SynthesisTab state={state} />}
      {tab === 'activity' && <ActivityTab state={state} />}
      {tab === 'journal' && <JournalTab state={state} />}
      {tab === 'changelog' && <ChangelogTab state={state} />}
      {tab === 'feedback' && <FeedbackTab state={state} />}
    </AppShell>
  )
}

export default App

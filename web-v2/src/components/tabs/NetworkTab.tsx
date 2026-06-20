import { useEffect, useMemo, useRef } from 'react'
import * as d3 from 'd3'
import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { formatBytes } from '../../lib/utils'

interface Node extends d3.SimulationNodeDatum {
  id: string
  kind: 'self' | 'peer' | 'content'
  label: string
  size: number
}

interface Link extends d3.SimulationLinkDatum<Node> {
  kind: 'p2p' | 'content'
}

export function NetworkTab({ state }: { state: SwarmState | null }) {
  const transport = state?.network?.transport ?? 'local'
  const peers = state?.network?.peers ?? 0
  const agents = state?.agents ?? []
  const contentItems = state?.content?.items ?? []

  return (
    <div>
      <PageHeader
        title="Network"
        description="Swarm topology — agents, peers, and content flow through the P2P fabric."
      />

      <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Transport" value={transport.toUpperCase()} accent={transport === 'p2p' ? 'cyan' : 'muted'} />
        <Stat label="Peers" value={peers} accent="cyan" />
        <Stat label="Agents" value={agents.length} accent="accent" />
        <Stat label="Content refs" value={contentItems.length} accent="green" />
      </div>

      {transport === 'local' && peers === 0 && agents.length <= 1 ? (
        <EmptyState
          icon="🌐"
          title="No P2P fabric"
          description="This agent is running on local transport. Start with `--network-transport libp2p` and connect peers to see the swarm topology."
        />
      ) : (
        <Card className="!p-0 overflow-hidden">
          <ForceGraph state={state} />
        </Card>
      )}

      {contentItems.length > 0 ? (
        <div className="mt-6">
          <h2 className="mb-3 text-sm font-semibold text-[var(--color-dim)]">Recent content</h2>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2 lg:grid-cols-3">
            {contentItems.slice(0, 9).map((c, i) => (
              <Card key={(c.cid as string) ?? i} className="!p-3">
                <div className="flex items-center justify-between gap-2 text-[11px]">
                  <span className="rounded-full bg-[var(--color-cyan-soft)] px-2 py-0.5 text-[var(--color-cyan)]">
                    {(c.type as string) ?? 'content'}
                  </span>
                  <span className="text-[var(--color-muted)]">{c.size ? formatBytes(Number(c.size)) : '—'}</span>
                </div>
                <div className="mt-2 truncate text-[12px] text-[var(--color-fg)]">
                  {(c.summary as string) ?? '—'}
                </div>
                <div className="mt-1 truncate font-mono text-[10px] text-[var(--color-muted)]">
                  {(c.cid as string)?.slice(0, 16) ?? ''}
                </div>
              </Card>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function Stat({
  label,
  value,
  accent,
}: {
  label: string
  value: string | number
  accent: 'cyan' | 'accent' | 'green' | 'muted'
}) {
  const colorMap = {
    cyan: 'text-[var(--color-cyan)]',
    accent: 'text-[var(--color-accent)]',
    green: 'text-[var(--color-green)]',
    muted: 'text-[var(--color-dim)]',
  }
  return (
    <Card>
      <div className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${colorMap[accent]}`}>{value}</div>
    </Card>
  )
}

function ForceGraph({ state }: { state: SwarmState | null }) {
  const ref = useRef<SVGSVGElement | null>(null)

  const { nodes, links } = useMemo(() => buildGraph(state), [state])

  useEffect(() => {
    if (!ref.current) return
    const svg = d3.select(ref.current)
    const width = ref.current.clientWidth
    const height = 420

    svg.selectAll('*').remove()

    const sim = d3
      .forceSimulation<Node>(nodes)
      .force(
        'link',
        d3
          .forceLink<Node, Link>(links)
          .id((d) => d.id)
          .distance((d) => (d.kind === 'content' ? 50 : 90))
          .strength(0.3),
      )
      .force('charge', d3.forceManyBody().strength(-160))
      .force('center', d3.forceCenter(width / 2, height / 2))
      .force(
        'collide',
        d3.forceCollide<Node>().radius((d) => d.size + 4),
      )

    const linkSel = svg
      .append('g')
      .attr('stroke-opacity', 0.5)
      .selectAll('line')
      .data(links)
      .join('line')
      .attr('stroke', (d) => (d.kind === 'p2p' ? 'var(--color-cyan)' : 'var(--color-accent)'))
      .attr('stroke-width', (d) => (d.kind === 'p2p' ? 1.2 : 0.8))
      .attr('stroke-dasharray', (d) => (d.kind === 'content' ? '2,3' : null))

    const nodeSel = svg
      .append('g')
      .selectAll('g')
      .data(nodes)
      .join('g')
      .style('cursor', 'pointer')

    nodeSel
      .append('circle')
      .attr('r', (d) => d.size)
      .attr('fill', (d) =>
        d.kind === 'self'
          ? 'var(--color-accent)'
          : d.kind === 'peer'
          ? 'var(--color-cyan)'
          : 'var(--color-surface)',
      )
      .attr('stroke', (d) => (d.kind === 'content' ? 'var(--color-accent)' : 'transparent'))
      .attr('stroke-width', 1.5)

    nodeSel
      .append('text')
      .text((d) => d.label)
      .attr('font-size', 10)
      .attr('fill', 'var(--color-fg)')
      .attr('text-anchor', 'middle')
      .attr('dy', (d) => d.size + 12)

    sim.on('tick', () => {
      linkSel
        .attr('x1', (d) => (d.source as Node).x ?? 0)
        .attr('y1', (d) => (d.source as Node).y ?? 0)
        .attr('x2', (d) => (d.target as Node).x ?? 0)
        .attr('y2', (d) => (d.target as Node).y ?? 0)
      nodeSel.attr('transform', (d) => `translate(${d.x ?? 0},${d.y ?? 0})`)
    })

    return () => {
      sim.stop()
    }
  }, [nodes, links])

  return <svg ref={ref} width="100%" height="420" className="block" />
}

function buildGraph(state: SwarmState | null): { nodes: Node[]; links: Link[] } {
  const nodes: Node[] = []
  const links: Link[] = []
  const agents = state?.agents ?? []
  const peerCount = state?.network?.peers ?? 0
  const content = state?.content?.items ?? []

  for (const a of agents) {
    nodes.push({ id: `agent:${a.name}`, kind: 'self', label: a.name, size: 12 })
  }

  for (let i = 0; i < peerCount; i++) {
    const id = `peer:${i}`
    nodes.push({ id, kind: 'peer', label: `peer-${i + 1}`, size: 8 })
    if (agents[0]) {
      links.push({ source: `agent:${agents[0].name}`, target: id, kind: 'p2p' })
    }
  }

  for (const c of content.slice(0, 8)) {
    const cid = (c.cid as string) ?? Math.random().toString(36).slice(2)
    const id = `content:${cid}`
    nodes.push({
      id,
      kind: 'content',
      label: ((c.type as string) ?? 'content').slice(0, 10),
      size: 5,
    })
    const ownerName = (c.agent_id as string) ?? agents[0]?.name
    if (ownerName) {
      const ownerNode = nodes.find((n) => n.id === `agent:${ownerName}` || n.id.startsWith('agent:'))
      if (ownerNode) {
        links.push({ source: ownerNode.id, target: id, kind: 'content' })
      }
    }
  }

  return { nodes, links }
}

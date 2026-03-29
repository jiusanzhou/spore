import React from 'react';
import { t } from '../i18n';

export default function MemoryTab({ state }) {
  const content = state?.content || {};
  const stats = content.stats || {};
  const items = content.items || [];

  // Deduplicate by summary, keep latest
  const deduped = new Map();
  items.forEach(item => {
    const key = item.summary || item.cid;
    const existing = deduped.get(key);
    if (!existing || new Date(item.timestamp) > new Date(existing.timestamp)) {
      deduped.set(key, item);
    }
  });
  const sorted = Array.from(deduped.values()).sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));

  const copyCID = (text) => {
    if (text && navigator.clipboard) {
      navigator.clipboard.writeText(text).then(() => {
        // Could add toast here
      });
    }
  };

  return (
    <div>
      <div className="memory-stats">
        <div className="mem-stat-card">
          <div className="val">{stats.items || 0}</div>
          <div className="lbl">{t('total_items')}</div>
        </div>
        <div className="mem-stat-card">
          <div className="val">{fmtBytes(stats.total_bytes)}</div>
          <div className="lbl">{t('total_bytes')}</div>
        </div>
        <div className="mem-stat-card">
          <div className="val">{stats.persistent ? <span style={{ color: 'var(--green)' }}>✓</span> : '✗'}</div>
          <div className="lbl">{t('persistent')}</div>
        </div>
        <div className="mem-stat-card">
          <div className="val">{stats.cache_items || 0}</div>
          <div className="lbl">{t('cache_items')}</div>
        </div>
      </div>

      <div className="memory-table-wrap">
        <table className="memory-table">
          <thead>
            <tr>
              <th>{t('col_agent')}</th>
              <th>{t('col_summary')}</th>
              <th>SHA-256</th>
              <th>IPFS CID</th>
              <th>{t('col_size')}</th>
              <th>{t('col_time')}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((item, i) => {
              const sha = (item.cid || '').substring(0, 12);
              const ipfs = item.ipfs_cid || '';
              const ipfsShort = ipfs.length > 20 ? ipfs.substring(0, 20) + '…' : ipfs;
              const viewUrl = '/api/content/' + encodeURIComponent(item.cid || '') + '?format=html';

              return (
                <tr key={item.cid || i}>
                  <td>{item.agent_id}</td>
                  <td>
                    <a href={viewUrl} target="_blank" rel="noopener noreferrer" className="content-link">
                      {item.summary || item.type || 'content'}
                    </a>
                  </td>
                  <td
                    className="sha-cell"
                    title={item.cid || ''}
                    onClick={(e) => { e.stopPropagation(); copyCID(item.cid); }}
                  >
                    {sha}
                  </td>
                  <td
                    className="cid-cell"
                    title={ipfs}
                    onClick={(e) => { e.stopPropagation(); copyCID(ipfs); }}
                  >
                    {ipfsShort}
                  </td>
                  <td>{fmtBytes(item.size)}</td>
                  <td>{relTime(item.timestamp)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function relTime(ts) {
  if (!ts) return '';
  const diff = (Date.now() - new Date(ts).getTime()) / 1000;
  if (diff < 0) return 'just now';
  if (diff < 60) return Math.floor(diff) + 's ago';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  return Math.floor(diff / 86400) + 'd ago';
}

function fmtBytes(b) {
  if (!b) return '0 B';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  return (b / 1048576).toFixed(1) + ' MB';
}

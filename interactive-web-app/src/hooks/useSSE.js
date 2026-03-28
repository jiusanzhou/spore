import { useState, useEffect, useRef, useCallback } from 'react';

export default function useSSE(url) {
  const [state, setState] = useState(null);
  const [connected, setConnected] = useState(false);
  const [lastUpdate, setLastUpdate] = useState(null);
  const retryDelay = useRef(1000);
  const evtSource = useRef(null);

  const connect = useCallback(() => {
    if (evtSource.current) {
      try { evtSource.current.close(); } catch (e) {}
    }

    const es = new EventSource(url);
    evtSource.current = es;

    es.onopen = () => {
      retryDelay.current = 1000;
      setConnected(true);
    };

    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data);
        setState(data);
        setLastUpdate(new Date());
      } catch (err) {
        console.error('SSE parse error', err);
      }
    };

    es.onerror = () => {
      setConnected(false);
      es.close();
      setTimeout(() => {
        retryDelay.current = Math.min(retryDelay.current * 2, 30000);
        connect();
      }, retryDelay.current);
    };
  }, [url]);

  useEffect(() => {
    connect();
    return () => {
      if (evtSource.current) {
        evtSource.current.close();
      }
    };
  }, [connect]);

  return { state, connected, lastUpdate };
}

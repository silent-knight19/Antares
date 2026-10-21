import { useEffect, useState } from 'react';
import type { InfoEvent } from './types';

export type StreamState = 'connecting' | 'live' | 'reconnecting';

export function useInfoStream() {
  const [fields, setFields] = useState<Record<string, string>>({});
  const [state, setState] = useState<StreamState>('connecting');

  useEffect(() => {
    const events = new EventSource('/api/events/stream');
    events.onopen = () => setState('live');
    events.onerror = () => setState('reconnecting');
    events.addEventListener('info', (event) => {
      try {
        const data = JSON.parse((event as MessageEvent).data) as InfoEvent;
        setFields(data.fields ?? {});
        setState('live');
      } catch {
        setState('reconnecting');
      }
    });
    events.addEventListener('status', () => setState('reconnecting'));
    return () => events.close();
  }, []);

  return { fields, state };
}

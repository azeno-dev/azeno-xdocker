import { useCallback, useEffect, useRef, useState } from 'react';
import { get, Job } from './api';

/** Loads `path` (re-loads when it changes) and optionally polls. */
export function useFetch<T>(path: string | null, opts: { poll?: number } = {}) {
  const [data, setData] = useState<T | undefined>();
  const [error, setError] = useState<string>('');
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);

  const load = useCallback(
    async (quiet = false) => {
      if (!path) return;
      const my = ++seq.current;
      if (!quiet) setLoading(true);
      try {
        const d = await get<T>(path);
        if (my === seq.current) {
          setData(d);
          setError('');
        }
      } catch (e: any) {
        if (my === seq.current) setError(e.message || String(e));
      } finally {
        if (my === seq.current) setLoading(false);
      }
    },
    [path],
  );

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!opts.poll || !path) return;
    const t = setInterval(() => {
      if (document.visibilityState === 'visible') load(true);
    }, opts.poll);
    return () => clearInterval(t);
  }, [opts.poll, path, load]);

  return { data, error, loading, reload: load, setData };
}

/** Polls a job until it finishes. */
export function useJob(id: number | null, onDone?: (j: Job) => void) {
  const [job, setJob] = useState<Job | null>(null);
  const done = useRef(onDone);
  done.current = onDone;
  useEffect(() => {
    if (!id) {
      setJob(null);
      return;
    }
    let stop = false;
    const tick = async () => {
      try {
        const r = await get<{ job: Job }>(`/jobs/${id}`);
        if (stop) return;
        setJob(r.job);
        if (['done', 'failed', 'cancelled'].includes(r.job.status)) {
          done.current?.(r.job);
          return;
        }
      } catch {
        /* keep polling */
      }
      if (!stop) setTimeout(tick, 800);
    };
    tick();
    return () => {
      stop = true;
    };
  }, [id]);
  return job;
}

export function useDebounced<T>(value: T, ms = 300) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

/** A remembered per-browser value (wrapped in try/catch for private mode). */
export function useStored<T>(key: string, initial: T) {
  const [v, setV] = useState<T>(() => {
    try {
      const s = localStorage.getItem('xdocker.' + key);
      return s === null ? initial : (JSON.parse(s) as T);
    } catch {
      return initial;
    }
  });
  const set = useCallback(
    (nv: T) => {
      setV(nv);
      try {
        localStorage.setItem('xdocker.' + key, JSON.stringify(nv));
      } catch {
        /* ignore */
      }
    },
    [key],
  );
  return [v, set] as const;
}

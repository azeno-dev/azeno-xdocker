import { createContext, ReactNode, useCallback, useContext, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Job } from '../lib/api';
import { useJob } from '../lib/hooks';
import { useToast } from './ui';

type Track = (id: number, onDone?: (j: Job) => void) => void;
const Ctx = createContext<Track>(() => {});

/** `useTrackJob()(id)` shows a toast when the job ends. */
export const useTrackJob = () => useContext(Ctx);

export function JobsProvider({ children }: { children: ReactNode }) {
  const [ids, setIds] = useState<number[]>([]);
  const callbacks = useRef(new Map<number, (j: Job) => void>());
  const track = useCallback<Track>((id, onDone) => {
    if (onDone) callbacks.current.set(id, onDone);
    setIds((xs) => (xs.includes(id) ? xs : [...xs, id]));
  }, []);
  const finished = useCallback((j: Job) => {
    callbacks.current.get(j.id)?.(j);
    callbacks.current.delete(j.id);
    setIds((xs) => xs.filter((x) => x !== j.id));
  }, []);
  return (
    <Ctx.Provider value={track}>
      {children}
      {ids.map((id) => <Watcher key={id} id={id} onDone={finished} />)}
    </Ctx.Provider>
  );
}

function Watcher({ id, onDone }: { id: number; onDone: (j: Job) => void }) {
  const toast = useToast();
  useJob(id, (j) => {
    toast(
      <span>
        <strong>{j.title}</strong>: {j.status === 'done' ? j.summary : `${j.status}: ${j.summary}`} <Link to={`/jobs/${j.id}`}>Details</Link>
      </span>,
      j.status !== 'done',
    );
    onDone(j);
  });
  return null;
}

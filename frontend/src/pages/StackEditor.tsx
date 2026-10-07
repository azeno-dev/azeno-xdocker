import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { ErrorNote, useToast } from '../components/ui';
import { ApiError, get, PortConflict, post, put } from '../lib/api';
import { useDebounced } from '../lib/hooks';

const TEMPLATE = `services:
  app:
    image: nginx:1.27-alpine
    restart: unless-stopped
    ports:
      - "8081:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro
`;

export function StackEditorPage() {
  const { name: routeName } = useParams();
  const isNew = !routeName;
  const { isAdmin } = useAuth();
  const nav = useNavigate();
  const toast = useToast();
  const track = useTrackJob();
  const [name, setName] = useState(routeName || '');
  const [yaml, setYaml] = useState(isNew ? TEMPLATE : '');
  const [env, setEnv] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [conflicts, setConflicts] = useState<Record<string, PortConflict[]>>({});
  const [logs, setLogs] = useState<string | null>(null);

  useEffect(() => {
    if (isNew) return;
    get<{ yaml: string; env: string }>(`/stacks/${routeName}`).then((r) => { setYaml(r.yaml); setEnv(r.env); }).catch((e) => setErr(e.message));
  }, [routeName, isNew]);

  const dy = useDebounced(yaml, 600);
  const de = useDebounced(env, 600);
  useEffect(() => {
    const n = name || 'new';
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(n) || !dy) return;
    post<{ conflicts: Record<string, PortConflict[]> }>(`/stacks/${n}/check`, { yaml: dy, env: de }).then((r) => setConflicts(r.conflicts || {})).catch(() => {});
  }, [dy, de, name]);

  const save = async (deploy: boolean, force = false) => {
    setErr('');
    if (!/^[a-z0-9][a-z0-9_-]{0,62}$/.test(name)) {
      setErr('Stack name: lower-case letters, digits, - and _ (must start with a letter or digit).');
      return;
    }
    setBusy(true);
    try {
      const r = await put<{ jobId?: number }>(`/stacks/${name}`, { yaml, env, deploy, force });
      toast(deploy ? `Saved; deploying ${name}…` : `Saved ${name}`);
      if (r.jobId) track(r.jobId);
      if (isNew) nav(`/stacks/${name}`, { replace: true });
    } catch (e: any) {
      if (e instanceof ApiError && e.body?.conflicts && confirm(`${e.message}\n\nDeploy anyway?`)) {
        setBusy(false);
        return save(deploy, true);
      }
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const showLogs = async () => {
    try {
      setLogs(await get<string>(`/stacks/${name}/logs?tail=300`));
    } catch (e: any) {
      setLogs(e.message);
    }
  };

  const conflictList = Object.entries(conflicts);
  return (
    <>
      <div className="page-head">
        <div style={{ flex: 1 }}>
          <div className="crumb"><Link to="/stacks">Stacks</Link> › {isNew ? 'New' : 'Edit'}</div>
          <h1>{isNew ? 'New stack' : name}</h1>
        </div>
        {!isNew && <button className="btn" onClick={showLogs}>Logs</button>}
        {isAdmin && <>
          <button className="btn" disabled={busy} onClick={() => save(false)}>Save</button>
          <button className="btn primary" disabled={busy} onClick={() => save(true)}><Icon name="play" size={16} />Save &amp; deploy</button>
        </>}
      </div>
      <ErrorNote error={err} />
      {conflictList.length > 0 && (
        <div className="note warn"><Icon name="warn" />
          <span><strong>Port conflicts:</strong> {conflictList.map(([p, cs]) => `${p} is used by ${cs.map((c) => c.name).join(', ')}`).join('; ')}. <Link to="/ports">Find a free port</Link></span>
        </div>
      )}
      {isNew && (
        <label className="field" style={{ maxWidth: 360 }}>Stack name
          <input className="input mono" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="my-app" />
          <span className="hint">Becomes the compose project name and the folder name.</span>
        </label>
      )}
      <div className="grid-2" style={{ gridTemplateColumns: 'minmax(0, 2fr) minmax(0, 1fr)', flex: 1 }}>
        <label className="field">compose.yaml
          <textarea className="textarea" spellCheck={false} value={yaml} onChange={(e) => setYaml(e.target.value)} style={{ minHeight: 420, flex: 1 }} readOnly={!isAdmin}
            onKeyDown={(e) => {
              if (e.key === 'Tab') {
                e.preventDefault();
                const t = e.currentTarget;
                const s = t.selectionStart;
                setYaml(yaml.slice(0, s) + '  ' + yaml.slice(t.selectionEnd));
                requestAnimationFrame(() => (t.selectionStart = t.selectionEnd = s + 2));
              }
            }} />
        </label>
        <label className="field">.env (optional)
          <textarea className="textarea" spellCheck={false} value={env} onChange={(e) => setEnv(e.target.value)} style={{ minHeight: 200 }} placeholder="KEY=value" readOnly={!isAdmin} />
          <span className="hint">The file is validated with <code>docker compose config</code> before saving. Relative paths like <code>./data</code> are inside the stack folder.</span>
        </label>
      </div>
      {logs !== null && <div className="logbox" style={{ maxHeight: 360 }}>{logs || 'No log output.'}</div>}
    </>
  );
}

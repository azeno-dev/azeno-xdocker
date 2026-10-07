import { createContext, FormEvent, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { AuthState, get, onUnauthorized, post, setCsrf, User } from '../lib/api';
import { Icon } from './Icon';

interface AuthCtx {
  user: User;
  isAdmin: boolean;
  version: string;
  logout: () => Promise<void>;
}
const Ctx = createContext<AuthCtx | null>(null);

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error('useAuth outside AuthGate');
  return c;
}

/** Shows the setup or sign-in form until there is a session. */
export function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState | null>(null);
  const [loadErr, setLoadErr] = useState('');

  const refresh = useCallback(async () => {
    try {
      const s = await get<AuthState>('/auth/state');
      setCsrf(s.csrf || '');
      setState(s);
      setLoadErr('');
    } catch (e: any) {
      setLoadErr(e.message);
    }
  }, []);

  useEffect(() => {
    refresh();
    return onUnauthorized(() => {
      setCsrf('');
      setState((s) => (s ? { ...s, user: undefined } : s));
    });
  }, [refresh]);

  if (loadErr) {
    return (
      <div className="login-wrap">
        <div className="login">
          <h1>xDocker</h1>
          <p className="error-text">Cannot reach the xDocker server: {loadErr}</p>
          <button className="btn" onClick={refresh}>Try again</button>
        </div>
      </div>
    );
  }
  if (!state) return null;
  if (!state.user) return <SignIn setup={state.setupRequired} version={state.version} onDone={(s) => { setCsrf(s.csrf || ''); setState(s); }} />;

  const logout = async () => {
    await post('/auth/logout').catch(() => {});
    setCsrf('');
    setState({ ...state, user: undefined });
  };
  return <Ctx.Provider value={{ user: state.user, isAdmin: state.user.role === 'admin', version: state.version, logout }}>{children}</Ctx.Provider>;
}

function SignIn({ setup, version, onDone }: { setup: boolean; version: string; onDone: (s: AuthState) => void }) {
  const [username, setUsername] = useState(setup ? 'admin' : '');
  const [password, setPassword] = useState('');
  const [password2, setPassword2] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr('');
    if (setup && password !== password2) {
      setErr('The passwords do not match.');
      return;
    }
    setBusy(true);
    try {
      onDone(await post<AuthState>(setup ? '/auth/setup' : '/auth/login', { username, password }));
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <form className="login" onSubmit={submit}>
        <div className="brand" style={{ padding: 0 }}>
          <span className="brand-mark"><Icon name="cube" size={16} /></span>
          xDocker
        </div>
        <div>
          <h1 style={{ fontSize: 20 }}>{setup ? 'Create the admin account' : 'Sign in'}</h1>
          {setup && <p className="muted" style={{ margin: '6px 0 0', fontSize: 13 }}>This is the first start. The account you create here can manage Docker on this NAS.</p>}
        </div>
        <label className="field">Username
          <input className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </label>
        <label className="field">Password
          <input className="input" type="password" autoComplete={setup ? 'new-password' : 'current-password'} value={password} onChange={(e) => setPassword(e.target.value)} required minLength={setup ? 8 : undefined} />
          {setup && <span className="hint">At least 8 characters.</span>}
        </label>
        {setup && (
          <label className="field">Repeat password
            <input className="input" type="password" autoComplete="new-password" value={password2} onChange={(e) => setPassword2(e.target.value)} required />
          </label>
        )}
        {err && <div className="error-text" role="alert">{err}</div>}
        <button className="btn primary" disabled={busy}>{setup ? 'Create account' : 'Sign in'}</button>
        <div className="muted" style={{ fontSize: 12, textAlign: 'center' }}>xDocker {version}</div>
      </form>
    </div>
  );
}

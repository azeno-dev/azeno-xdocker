import { useState } from 'react';
import { useAuth } from '../components/auth';
import { ErrorNote, Modal, useToast } from '../components/ui';
import { del, post, put, User } from '../lib/api';
import { date } from '../lib/format';
import { useFetch } from '../lib/hooks';

export function UsersPage() {
  const { user: me, isAdmin } = useAuth();
  const toast = useToast();
  const { data, error, reload } = useFetch<User[]>(isAdmin ? '/users' : null);
  const [edit, setEdit] = useState<User | 'new' | null>(null);
  if (!isAdmin) return <p>Only admins can manage users.</p>;
  const remove = async (u: User) => {
    if (!confirm(`Delete user ${u.username}?`)) return;
    try {
      await del(`/users/${u.id}`);
      reload();
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  return (
    <>
      <div className="page-head"><h1>Users &amp; access</h1><button className="btn primary" onClick={() => setEdit('new')}>Add user</button></div>
      <div className="hint-line"><strong>admin</strong> can change everything; <strong>viewer</strong> can look but not start, stop or delete anything.</div>
      <ErrorNote error={error} />
      <div className="table-wrap">
        <table className="t">
          <thead><tr><th>User</th><th>Role</th><th>Created</th><th></th></tr></thead>
          <tbody>
            {(data || []).map((u) => (
              <tr key={u.id}>
                <td><strong>{u.username}</strong>{u.id === me.id && <span className="badge muted" style={{ marginLeft: 6 }}>you</span>}</td>
                <td><span className={'badge ' + (u.role === 'admin' ? 'info' : 'muted')}>{u.role}</span></td>
                <td>{date(u.createdAt)}</td>
                <td><div className="btn-row" style={{ justifyContent: 'flex-end' }}>
                  <button className="btn sm" onClick={() => setEdit(u)}>Edit</button>
                  {u.id !== me.id && <button className="btn sm" style={{ color: 'var(--danger)' }} onClick={() => remove(u)}>Delete</button>}
                </div></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {edit && <UserDialog u={edit === 'new' ? null : edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload(); }} />}
    </>
  );
}

function UserDialog({ u, onClose, onSaved }: { u: User | null; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({ username: u?.username || '', password: '', role: u?.role || 'viewer' });
  const [err, setErr] = useState('');
  const save = async () => {
    try {
      if (u) await put(`/users/${u.id}`, f);
      else await post('/users', f);
      onSaved();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <Modal title={u ? `Edit ${u.username}` : 'Add user'} onClose={onClose} footer={<><span className="spacer" /><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></>}>
      {!u && <label className="field">Username<input className="input" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} /></label>}
      <label className="field">{u ? 'New password (empty = unchanged)' : 'Password'}<input className="input" type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} /></label>
      <label className="field">Role
        <select className="select" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value as any })}><option value="viewer">viewer</option><option value="admin">admin</option></select>
      </label>
      {err && <div className="error-text">{err}</div>}
    </Modal>
  );
}

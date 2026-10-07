import { useEffect, useMemo, useState } from 'react';
import { useAuth } from '../components/auth';
import { ErrorNote, SwitchRow, useToast } from '../components/ui';
import { post, put, Settings } from '../lib/api';
import { useFetch } from '../lib/hooks';

interface SettingsResp { settings: Settings; stacksDir: string; dockerHost: string; hostPortsChecked: boolean; version: string }

export function SettingsPage() {
  const { isAdmin, user } = useAuth();
  const toast = useToast();
  const { data, error, reload } = useFetch<SettingsResp>('/settings');
  const [s, setS] = useState<Settings | null>(null);
  const [err, setErr] = useState('');
  const [pw, setPw] = useState('');
  useEffect(() => { if (data) setS({ ...data.settings, smtpPassword: '' }); }, [data]);
  const timezones = useMemo(() => {
    try { return (Intl as any).supportedValuesOf('timeZone') as string[]; } catch { return ['UTC']; }
  }, []);
  if (!s) return <ErrorNote error={error} />;
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setS({ ...s, [k]: v });

  const save = async () => {
    setErr('');
    try {
      await put('/settings', s);
      toast('Settings saved');
      reload();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const test = async (email: boolean, webhook: boolean) => {
    try {
      const r = await post<{ errors: string[] | null }>('/settings/test-notify', { email, webhook });
      if (r.errors?.length) toast(r.errors.join('\n'), true);
      else toast('Test notification sent');
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const changePw = async () => {
    try {
      await put('/me/password', { password: pw });
      setPw('');
      toast('Password changed');
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const ro = !isAdmin;

  return (
    <>
      <div className="page-head"><h1>Settings</h1>{isAdmin && <button className="btn primary" onClick={save}>Save</button>}</div>
      <ErrorNote error={err} />
      <div className="grid-2">
        <section className="card">
          <h2>General</h2>
          <label className="field">Default time zone
            <select className="select" value={s.timezone} disabled={ro} onChange={(e) => set('timezone', e.target.value)}>
              <option value="">Server default (TZ)</option>
              {timezones.map((z) => <option key={z}>{z}</option>)}
            </select>
            <span className="hint">Used by policies without their own time zone, for schedules and month boundaries.</span>
          </label>
          <label className="field">Ports reserved by DSM
            <textarea className="textarea" rows={3} value={s.reservedPorts} disabled={ro} onChange={(e) => set('reservedPorts', e.target.value)} />
            <span className="hint">Comma-separated, e.g. <code>5000/tcp DSM, 514/udp syslog</code>.</span>
          </label>
          <label className="field">Keep job history for (days)
            <input className="input" style={{ width: 120 }} inputMode="numeric" value={s.jobHistoryDays} disabled={ro} onChange={(e) => set('jobHistoryDays', Number(e.target.value) || 0)} />
          </label>
          <SwitchRow label="Garbage collection: also delete untagged manifests" checked={s.gcDeleteUntagged} onChange={(v) => set('gcDeleteUntagged', v)} disabled={ro}
            hint="Off by default: with registry:2 this can remove platforms of multi-arch images." />
        </section>
        <section className="card">
          <h2>Notifications</h2>
          <div className="field-row">
            <label className="field">SMTP server<input className="input" value={s.smtpHost} disabled={ro} onChange={(e) => set('smtpHost', e.target.value)} placeholder="smtp.example.com" /></label>
            <label className="field">Port<input className="input" inputMode="numeric" value={s.smtpPort} disabled={ro} onChange={(e) => set('smtpPort', Number(e.target.value) || 0)} /></label>
            <label className="field">Username<input className="input" autoComplete="off" value={s.smtpUser} disabled={ro} onChange={(e) => set('smtpUser', e.target.value)} /></label>
            <label className="field">Password<input className="input" type="password" autoComplete="new-password" value={s.smtpPassword || ''} disabled={ro} onChange={(e) => set('smtpPassword', e.target.value)} placeholder={s.hasSmtpPassword ? '(unchanged)' : ''} /></label>
            <label className="field">From<input className="input" value={s.smtpFrom} disabled={ro} onChange={(e) => set('smtpFrom', e.target.value)} placeholder="nas@example.com" /></label>
            <label className="field">To<input className="input" value={s.smtpTo} disabled={ro} onChange={(e) => set('smtpTo', e.target.value)} placeholder="you@example.com, other@example.com" /></label>
          </div>
          <label className="field">Webhook URL<input className="input mono" value={s.webhookUrl} disabled={ro} onChange={(e) => set('webhookUrl', e.target.value)} placeholder="https://…" /><span className="hint">Receives a JSON POST with title, text, status and details.</span></label>
          {isAdmin && <div className="btn-row"><button className="btn sm" onClick={() => test(true, false)}>Send test email</button><button className="btn sm" onClick={() => test(false, true)}>Send test webhook</button><span className="hint-line">Save first.</span></div>}
        </section>
        <section className="card">
          <h2>Environment</h2>
          <dl className="kv">
            <dt>Docker</dt><dd className="mono">{data?.dockerHost}</dd>
            <dt>Stacks folder</dt><dd className="mono">{data?.stacksDir}</dd>
            <dt>Host port check</dt><dd>{data?.hostPortsChecked ? 'on' : 'off (host /proc not mounted)'}</dd>
            <dt>Version</dt><dd>{data?.version}</dd>
          </dl>
        </section>
        <section className="card">
          <h2>My account</h2>
          <div className="muted">{user.username} · {user.role}</div>
          <div className="btn-row" style={{ alignItems: 'flex-end' }}>
            <label className="field" style={{ flex: 1 }}>New password<input className="input" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} /></label>
            <button className="btn" disabled={pw.length < 8} onClick={changePw}>Change password</button>
          </div>
        </section>
      </div>
    </>
  );
}

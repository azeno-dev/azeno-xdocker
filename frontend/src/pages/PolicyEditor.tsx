import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { ErrorNote, Seg, Stepper, SwitchRow, useToast } from '../components/ui';
import { del, Decision, get, PlanResult, Policy, PolicyPreview, post, put, Registry } from '../lib/api';
import { bytes, date } from '../lib/format';
import { useDebounced, useFetch } from '../lib/hooks';
import { describePolicy } from './Policies';

const DEFAULT: Policy = {
  id: 0, name: '', enabled: false, target: 'local', registryId: 0, repoPattern: '', tagInclude: '', tagExclude: 'latest',
  period: 'month', keepPerPeriod: 1, keepMode: 'newest', keepLastN: 3, minAgeDays: 7, maxAgeDays: 0,
  dateSource: 'created', dateRegex: '', dateLayout: '', skipInUse: true, cron: '0 3 * * 0', timezone: '',
  dryRun: true, gcAfter: true, notifyEmail: false, notifyWebhook: false, lastRunAt: 0, lastJobId: 0,
};

const CRON_PRESETS: [string, string][] = [
  ['0 3 * * *', 'Every day at 03:00'],
  ['0 3 * * 0', 'Every Sunday at 03:00'],
  ['0 3 1 * *', 'On the 1st of every month at 03:00'],
];

export function PolicyEditorPage() {
  const { id } = useParams();
  const isNew = !id;
  const { isAdmin } = useAuth();
  const nav = useNavigate();
  const toast = useToast();
  const track = useTrackJob();
  const [p, setP] = useState<Policy>(DEFAULT);
  const [loaded, setLoaded] = useState(isNew);
  const [err, setErr] = useState('');
  const { data: regs } = useFetch<Registry[]>('/registries');
  const set = <K extends keyof Policy>(k: K, v: Policy[K]) => setP((x) => ({ ...x, [k]: v }));

  useEffect(() => {
    if (isNew) return;
    get<Policy>(`/policies/${id}`).then((r) => { setP(r); setLoaded(true); }).catch((e) => setErr(e.message));
  }, [id, isNew]);
  useEffect(() => {
    if (isNew && regs?.length && !p.registryId) set('registryId', regs[0].id);
  }, [regs, isNew, p.registryId]);

  // Live preview: recomputed by the server's retention engine (the same code
  // the scheduled run uses) whenever a rule changes.
  const previewKey = JSON.stringify({ ...p, name: p.name || 'preview', enabled: false, notifyEmail: false, notifyWebhook: false });
  const dKey = useDebounced(previewKey, 450);
  const [pv, setPv] = useState<PolicyPreview | null>(null);
  const [pvErr, setPvErr] = useState('');
  const [pvBusy, setPvBusy] = useState(false);
  useEffect(() => {
    if (!loaded) return;
    let stale = false;
    setPvBusy(true);
    post<PolicyPreview>('/policies/preview', JSON.parse(dKey))
      .then((r) => { if (!stale) { setPv(r); setPvErr(''); } })
      .catch((e) => { if (!stale) setPvErr(e.message); })
      .finally(() => { if (!stale) setPvBusy(false); });
    return () => { stale = true; };
  }, [dKey, loaded]);

  const [runs, setRuns] = useState<{ runs: string[]; timezone: string } | null>(null);
  const dCron = useDebounced(p.cron + '|' + p.timezone, 400);
  useEffect(() => {
    const [cron, tz] = dCron.split('|');
    get<{ runs: string[]; timezone: string }>(`/policies/next-runs?cron=${encodeURIComponent(cron)}&tz=${encodeURIComponent(tz)}`)
      .then(setRuns).catch(() => setRuns(null));
  }, [dCron]);

  const save = async () => {
    setErr('');
    try {
      if (isNew) {
        const r = await post<{ id: number }>('/policies', p);
        toast('Policy saved');
        nav(`/policies/${r.id}`, { replace: true });
      } else {
        await put(`/policies/${id}`, p);
        toast('Policy saved');
      }
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const runNow = async (dry: boolean) => {
    try {
      const r = await post<{ jobId: number }>(`/policies/${id}/run?dry=${dry ? 1 : 0}`);
      track(r.jobId);
      toast(dry ? 'Dry run started' : 'Run started');
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const remove = async () => {
    if (!confirm(`Delete policy ${p.name}?`)) return;
    try {
      await del(`/policies/${id}`);
      nav('/policies');
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  const timezones = useMemo(() => {
    try {
      return (Intl as any).supportedValuesOf('timeZone') as string[];
    } catch {
      return ['Europe/Bucharest', 'UTC'];
    }
  }, []);
  const needsRegistry = p.target !== 'local';
  const total = (r?: PlanResult) => (r ? { del: r.deleteCount, keep: r.keepCount, size: r.deleteSize } : { del: 0, keep: 0, size: 0 });
  const t1 = total(pv?.local), t2 = total(pv?.registry);

  return (
    <>
      <div className="page-head">
        <div style={{ flex: 1 }}>
          <div className="crumb"><Link to="/policies">Retention policies</Link> › {isNew ? 'New' : 'Edit'}</div>
          <h1>{p.name || (isNew ? 'New policy' : '…')}</h1>
        </div>
        {!isNew && isAdmin && <button className="btn ghost" style={{ color: 'var(--danger)' }} onClick={remove}>Delete</button>}
        {!isNew && isAdmin && <button className="btn outline" onClick={() => runNow(true)}>Dry run now</button>}
        {isAdmin && <button className="btn primary" onClick={save}>Save policy</button>}
      </div>
      <ErrorNote error={err} />
      <div className="split" style={{ alignItems: 'flex-start' }}>
        <div className="rest" style={{ gap: 14 }}>
          <section className="card">
            <h2><span className="step-num">1</span>Scope</h2>
            <div className="field-row">
              <label className="field">Policy name<input className="input" value={p.name} onChange={(e) => set('name', e.target.value)} placeholder="azeno/api monthly" /></label>
              <div className="field">Apply to
                <Seg label="Apply to" value={p.target} onChange={(v) => set('target', v)} options={[['local', 'Local images'], ['registry', 'Registry'], ['both', 'Both']]} />
              </div>
              <label className="field">Repositories<input className="input mono" value={p.repoPattern} onChange={(e) => set('repoPattern', e.target.value)} placeholder="azeno/*  (empty = all)" />
                <span className="hint">Wildcard or /regex/. A registry host prefix on local images is ignored.</span></label>
              {needsRegistry ? (
                <label className="field">Registry
                  <select className="select" value={p.registryId} onChange={(e) => set('registryId', Number(e.target.value))}>
                    {!regs?.length && <option value={0}>No registry configured</option>}
                    {regs?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                  </select>
                </label>
              ) : <div />}
              <label className="field">Protected tags (never deleted)<input className="input mono" value={p.tagExclude} onChange={(e) => set('tagExclude', e.target.value)} placeholder="latest, stable, /^v\d+$/" /><span className="hint">Comma-separated; wildcards and /regex/ allowed.</span></label>
              <label className="field">Only these tags (optional)<input className="input mono" value={p.tagInclude} onChange={(e) => set('tagInclude', e.target.value)} placeholder="2026.*  (empty = all tags)" /></label>
            </div>
          </section>

          <section className="card">
            <h2><span className="step-num">2</span>What to keep</h2>
            <div className="field-row">
              <div className="field">Group images by
                <Seg label="Group images by" value={p.period} onChange={(v) => set('period', v)} options={[['day', 'Day'], ['week', 'Week'], ['month', 'Month'], ['year', 'Year']]} />
              </div>
              <div className="field">Which one to keep in each group
                <Seg label="Which to keep" value={p.keepMode} onChange={(v) => set('keepMode', v)} options={[['newest', 'Newest'], ['oldest', 'Oldest']]} />
              </div>
              <div className="field">Keep per group<Stepper label="Keep per group" value={p.keepPerPeriod} min={1} max={50} onChange={(v) => set('keepPerPeriod', v)} /></div>
              <div className="field">Always keep the last<Stepper label="Always keep the last" value={p.keepLastN} min={0} max={100} onChange={(v) => set('keepLastN', v)} unit="images" /></div>
              <div className="field">Minimum age<Stepper label="Minimum age" value={p.minAgeDays} min={0} max={365} onChange={(v) => set('minAgeDays', v)} unit="days" /></div>
              <div className="field">Maximum age (0 = no limit)<Stepper label="Maximum age" value={p.maxAgeDays} min={0} max={3650} onChange={(v) => set('maxAgeDays', v)} unit="days" /></div>
              <div className="field">Image date from
                <Seg label="Image date from" value={p.dateSource} onChange={(v) => set('dateSource', v)} options={[['created', 'Created date'], ['tag', 'Tag name']]} />
              </div>
              <SwitchRow label="Skip images used by containers" checked={p.skipInUse} onChange={(v) => set('skipInUse', v)} />
              {p.dateSource === 'tag' && <>
                <label className="field">Date regex (group 1)<input className="input mono" value={p.dateRegex} onChange={(e) => set('dateRegex', e.target.value)} placeholder="(\d{4}\.\d{2}\.\d{2})" /></label>
                <label className="field">Date layout (Go)<input className="input mono" value={p.dateLayout} onChange={(e) => set('dateLayout', e.target.value)} placeholder="2006.01.02" /><span className="hint">Use 2006 for year, 01 month, 02 day.</span></label>
              </>}
            </div>
            <div className="note info">{describePolicy(p)}</div>
          </section>

          <section className="card">
            <h2><span className="step-num">3</span>Schedule &amp; safety</h2>
            <div className="field-row">
              <label className="field">Runs (cron)
                <input className="input mono" value={p.cron} onChange={(e) => set('cron', e.target.value)} />
                <span className="btn-row" style={{ gap: 6 }}>{CRON_PRESETS.map(([c, l]) => <button key={c} type="button" className="chip" style={{ height: 26, fontSize: 12 }} aria-pressed={p.cron === c} onClick={() => set('cron', c)}>{l}</button>)}</span>
              </label>
              <label className="field">Time zone
                <select className="select" value={p.timezone} onChange={(e) => set('timezone', e.target.value)}>
                  <option value="">Default (Settings)</option>
                  {timezones.map((z) => <option key={z} value={z}>{z}</option>)}
                </select>
                <span className="hint">{runs ? 'Next: ' + runs.runs.map((r) => new Date(r).toLocaleString(undefined, { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', timeZone: runs.timezone === 'Local' ? undefined : runs.timezone })).join(' · ') : 'Invalid schedule'}</span>
              </label>
              <SwitchRow label="Enabled (runs on schedule)" checked={p.enabled} onChange={(v) => set('enabled', v)} />
              <SwitchRow label="Dry run (report only, delete nothing)" checked={p.dryRun} onChange={(v) => set('dryRun', v)} hint="Keep this on for a week and check the reports first." />
              <SwitchRow label="Registry garbage collection afterwards" checked={p.gcAfter} onChange={(v) => set('gcAfter', v)} disabled={!needsRegistry} />
              <div className="field" style={{ fontWeight: 600 }}>Notify after each run
                <div className="btn-row">
                  <label className="check"><input type="checkbox" checked={p.notifyEmail} onChange={(e) => set('notifyEmail', e.target.checked)} />Email</label>
                  <label className="check"><input type="checkbox" checked={p.notifyWebhook} onChange={(e) => set('notifyWebhook', e.target.checked)} />Webhook</label>
                  <Link to="/settings" style={{ fontSize: 12, fontWeight: 400 }}>Configure</Link>
                </div>
              </div>
            </div>
          </section>
        </div>

        <aside className="side card flush" aria-label="Live preview" style={{ width: 440, position: 'sticky', top: 0, maxHeight: 'calc(100vh - 140px)' }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div className="card-head"><h2>Live preview</h2>{pvBusy && <Icon name="refresh" size={14} className="spin" />}</div>
            <div className="grid-4" style={{ gridTemplateColumns: 'repeat(3, minmax(0,1fr))', gap: 8 }}>
              <div className="tile del"><div className="big">{t1.del + t2.del}</div><div className="small">to delete</div></div>
              <div className="tile keep"><div className="big">{t1.keep + t2.keep}</div><div className="small">to keep</div></div>
              <div className="tile info"><div className="big" style={{ fontSize: 18 }}>{bytes(t1.size + t2.size)}</div><div className="small">freed</div></div>
            </div>
            {p.dryRun && <div className="badge warn" style={{ whiteSpace: 'normal' }}>Dry run is on: scheduled runs only report this list.</div>}
            <ErrorNote error={pvErr} />
            {pv?.warnings.slice(0, 3).map((w) => <div key={w} className="hint-line">{w}</div>)}
          </div>
          <div style={{ overflow: 'auto', flex: 1, paddingBottom: 10 }}>
            {pv?.local && <PreviewList title="Local images" r={pv.local} />}
            {pv?.registry && <PreviewList title={'Registry ' + (pv.registryName || '')} r={pv.registry} />}
            {pv && !pv.local?.decisions?.length && !pv.registry?.decisions?.length && <div className="empty">No images match this scope.</div>}
          </div>
        </aside>
      </div>
    </>
  );
}

function PreviewList({ title, r }: { title: string; r: PlanResult }) {
  const groups = useMemo(() => {
    const out: { key: string; items: Decision[] }[] = [];
    for (const d of r.decisions || []) {
      const key = `${d.repo} · ${d.bucket}`;
      const last = out[out.length - 1];
      if (last && last.key === key) last.items.push(d);
      else out.push({ key, items: [d] });
    }
    return out;
  }, [r]);
  if (!r.decisions?.length) return null;
  return (
    <>
      <div className="preview-group" style={{ color: 'var(--text)' }}>{title}</div>
      {groups.map((g) => (
        <div key={g.key}>
          <div className="preview-group">{g.key}</div>
          {g.items.map((d) => (
            <div className="preview-row" key={d.ref + d.digest}>
              <span className={'mono' + (d.action === 'delete' ? ' del' : '')} title={`${d.ref} · ${date(d.date)} · ${bytes(d.size)}`} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.tag}</span>
              <span className={'badge ' + (d.action === 'keep' ? 'ok' : 'danger')} style={{ justifySelf: 'start' }}>{d.action === 'keep' ? 'Keep' : 'Delete'}</span>
              <span className="muted" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={d.reason}>{d.reason}</span>
            </div>
          ))}
        </div>
      ))}
    </>
  );
}

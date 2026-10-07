import { createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Icon } from './Icon';

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} className="switch" disabled={disabled}
      onClick={() => onChange(!checked)} />
  );
}

export function SwitchRow({ label, checked, onChange, hint, disabled }: { label: string; checked: boolean; onChange: (v: boolean) => void; hint?: string; disabled?: boolean }) {
  return (
    <div className="switch-row">
      <span>
        {label}
        {hint && <div className="hint-line" style={{ fontWeight: 400 }}>{hint}</div>}
      </span>
      <Switch checked={checked} onChange={onChange} label={label} disabled={disabled} />
    </div>
  );
}

export function Seg<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map(([v, l]) => (
        <button key={v} type="button" aria-pressed={value === v} onClick={() => onChange(v)}>{l}</button>
      ))}
    </div>
  );
}

export function Stepper({ value, onChange, min = 0, max = 999, label, unit }: { value: number; onChange: (v: number) => void; min?: number; max?: number; label: string; unit?: string }) {
  const clamp = (n: number) => Math.max(min, Math.min(max, isNaN(n) ? min : n));
  return (
    <div className="btn-row" style={{ gap: 6 }}>
      <div className="stepper">
        <button type="button" aria-label={'Decrease ' + label} onClick={() => onChange(clamp(value - 1))}>−</button>
        <input aria-label={label} inputMode="numeric" value={value} onChange={(e) => onChange(clamp(parseInt(e.target.value, 10)))} />
        <button type="button" aria-label={'Increase ' + label} onClick={() => onChange(clamp(value + 1))}>+</button>
      </div>
      {unit && <span className="muted" style={{ fontSize: 13 }}>{unit}</span>}
    </div>
  );
}

export function Modal({ title, onClose, children, footer, wide, icon }: { title: ReactNode; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean; icon?: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const first = ref.current?.querySelector<HTMLElement>('input, select, textarea, button:not([aria-label="Close"])');
    first?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      prev?.focus();
    };
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={'dialog' + (wide ? ' wide' : '')} role="dialog" aria-modal="true" aria-label={typeof title === 'string' ? title : undefined} ref={ref}>
        <div className="dialog-head">
          {icon}
          <h2>{title}</h2>
          <button type="button" className="btn ghost icon" aria-label="Close" onClick={onClose}><Icon name="close" size={16} /></button>
        </div>
        <div className="dialog-body">{children}</div>
        {footer && <div className="dialog-foot">{footer}</div>}
      </div>
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function ErrorNote({ error }: { error?: string }) {
  if (!error) return null;
  return (
    <div className="note danger" role="alert">
      <Icon name="warn" size={18} style={{ color: 'var(--danger)' }} />
      <span style={{ whiteSpace: 'pre-wrap' }}>{error}</span>
    </div>
  );
}

export function StateBadge({ state }: { state: string }) {
  const cls = state === 'running' ? 'ok' : state === 'exited' || state === 'dead' ? 'muted' : state === 'paused' || state === 'restarting' ? 'warn' : 'muted';
  return <span className={'badge ' + cls}>{state === 'running' && <span className="dot ok" style={{ width: 6, height: 6 }} />}{state}</span>;
}

export function JobStatusBadge({ status }: { status: string }) {
  const cls = status === 'done' ? 'ok' : status === 'failed' ? 'danger' : status === 'running' || status === 'queued' ? 'info' : 'muted';
  return <span className={'badge ' + cls}>{status}</span>;
}

// ---------- toasts ----------

interface Toast { id: number; text: ReactNode; error?: boolean }
const ToastCtx = createContext<(text: ReactNode, error?: boolean) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((text: ReactNode, error = false) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, text, error }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), error ? 9000 : 5000);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={'toast' + (t.error ? ' error' : '')}>
            <Icon name={t.error ? 'warn' : 'check'} size={18} />
            <div style={{ flex: 1 }}>{t.text}</div>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

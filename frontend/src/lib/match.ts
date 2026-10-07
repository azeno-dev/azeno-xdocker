// Client-side version of the backend's search syntax (internal/match):
// plain text = case-insensitive substring, * and ? = wildcards, /re/ = regex.
export function match(q: string): (s: string) => boolean {
  q = q.trim();
  if (!q) return () => true;
  if (q.length >= 2 && q.startsWith('/') && q.endsWith('/')) {
    try {
      const re = new RegExp(q.slice(1, -1), 'i');
      return (s) => re.test(s);
    } catch {
      return () => false;
    }
  }
  if (/[*?]/.test(q)) {
    const src = q.split('').map((c) => (c === '*' ? '.*' : c === '?' ? '.' : c.replace(/[.+^${}()|[\]\\]/g, '\\$&'))).join('');
    const re = new RegExp('^' + src + (q.includes(':') ? '$' : ''), 'i');
    return (s) => re.test(s);
  }
  const lq = q.toLowerCase();
  return (s) => (s || '').toLowerCase().includes(lq);
}

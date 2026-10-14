export function formatCode(input: string): string {
  const d = input.replace(/\D/g, '').slice(0, 9);
  return [d.slice(0, 3), d.slice(3, 6), d.slice(6, 9)].filter(Boolean).join('-');
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

export function formatTime(iso: string): string {
  if (!iso || iso.startsWith('0001-')) return '';
  return new Date(iso).toLocaleString();
}

export function formatDuration(fromIso: string, toIso?: string): string {
  if (!fromIso || fromIso.startsWith('0001-')) return '';
  const end = toIso && !toIso.startsWith('0001-') ? new Date(toIso).getTime() : Date.now();
  let s = Math.max(0, Math.round((end - new Date(fromIso).getTime()) / 1000));
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  return h ? `${h}h ${m}m` : m ? `${m}m ${s}s` : `${s}s`;
}

export function osLabel(os: string): string {
  return ({ windows: 'Windows', darwin: 'macOS', linux: 'Linux' } as Record<string, string>)[os] ?? os;
}

export type ClientOS = 'windows' | 'darwin' | 'linux' | 'other';

export function detectOS(): ClientOS {
  const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
  const p = (nav.userAgentData?.platform || navigator.platform || navigator.userAgent).toLowerCase();
  if (/android|iphone|ipad|ipod|cros/.test(navigator.userAgent.toLowerCase())) return 'other';
  if (p.includes('win')) return 'windows';
  if (p.includes('mac')) return 'darwin';
  if (p.includes('linux')) return 'linux';
  return 'other';
}

export async function detectArch(): Promise<'amd64' | 'arm64'> {
  const nav = navigator as Navigator & {
    userAgentData?: { getHighEntropyValues?: (h: string[]) => Promise<{ architecture?: string; bitness?: string }> };
  };
  try {
    const v = await nav.userAgentData?.getHighEntropyValues?.(['architecture', 'bitness']);
    if (v?.architecture === 'arm') return 'arm64';
  } catch {
    // Not available; fall through.
  }
  return /aarch64|arm64/i.test(navigator.userAgent) ? 'arm64' : 'amd64';
}

export const stateLabels: Record<string, string> = {
  pending: 'Waiting for user',
  awaiting_consent: 'Waiting for approval',
  active: 'Connected',
  ended: 'Ended',
  expired: 'Expired',
};

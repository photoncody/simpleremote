// TypeScript mirror of internal/proto. Keep in sync with the Go definitions.

export interface Envelope {
  type: string;
  data?: unknown;
}

export interface ICEServer {
  urls: string[];
  username?: string;
  credential?: string;
}

export interface Hello {
  session_id: string;
  org_name: string;
  tech_name: string;
  tech_email?: string;
  ice_servers: ICEServer[];
  reboot_reconnect?: boolean;
}

export interface AgentInfo {
  hostname: string;
  os: string;
  arch: string;
  username: string;
  version: string;
  elevated: boolean;
}

export type SessionState = 'pending' | 'awaiting_consent' | 'active' | 'ended' | 'expired';

export interface Status {
  state: SessionState;
  agent_connected: boolean;
  agent?: AgentInfo;
  end_reason?: string;
  persist?: boolean;
}

export interface Display {
  id: number;
  name: string;
  x: number;
  y: number;
  width: number;
  height: number;
  primary: boolean;
}

export interface Control {
  t: string;
  displays?: Display[];
  current?: number;
  id?: number;
  seq?: number;
  quality?: number;
  text?: string;
  on?: boolean;
  elevated?: boolean;
}

export interface FileEntry {
  name: string;
  dir: boolean;
  size: number;
  mtime: number;
}

export interface FileMsg {
  t: string;
  id: number;
  name?: string;
  path?: string;
  parent?: string;
  size?: number;
  entries?: FileEntry[];
  message?: string;
}

export const Channel = {
  Video: 'video',
  Input: 'input',
  Control: 'control',
  Files: 'files',
} as const;

// ---- video channel ----

export const VIDEO_TILE = 0x54; // 'T'
export const VIDEO_FRAME_END = 0x46; // 'F'
const TILE_HEADER = 1 + 4 + 2 * 6;

export type VideoMsg =
  | { kind: 'tile'; seq: number; x: number; y: number; w: number; h: number; screenW: number; screenH: number; jpeg: Uint8Array }
  | { kind: 'end'; seq: number };

export function parseVideo(buf: ArrayBuffer): VideoMsg | null {
  const v = new DataView(buf);
  if (buf.byteLength < 5) return null;
  const kind = v.getUint8(0);
  const seq = v.getUint32(1);
  if (kind === VIDEO_FRAME_END) return { kind: 'end', seq };
  if (kind !== VIDEO_TILE || buf.byteLength < TILE_HEADER) return null;
  return {
    kind: 'tile',
    seq,
    x: v.getUint16(5),
    y: v.getUint16(7),
    w: v.getUint16(9),
    h: v.getUint16(11),
    screenW: v.getUint16(13),
    screenH: v.getUint16(15),
    jpeg: new Uint8Array(buf, TILE_HEADER),
  };
}

// ---- files channel ----

export function fileChunk(id: number, data: Uint8Array): Uint8Array {
  const out = new Uint8Array(4 + data.byteLength);
  new DataView(out.buffer).setUint32(0, id);
  out.set(data, 4);
  return out;
}

export function parseFileChunk(buf: ArrayBuffer): { id: number; data: Uint8Array } | null {
  if (buf.byteLength < 4) return null;
  return { id: new DataView(buf).getUint32(0), data: new Uint8Array(buf, 4) };
}

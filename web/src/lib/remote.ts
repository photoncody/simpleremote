// RemoteSession drives one support session from the tech's browser:
// signaling over WebSocket, WebRTC data channels to the agent, screen
// rendering onto a canvas, and forwarding pointer and keyboard input.

import {
  Channel,
  parseFileChunk,
  parseVideo,
  fileChunk,
  type Control,
  type Display,
  type Envelope,
  type FileEntry,
  type FileMsg,
  type Hello,
  type Status,
} from './proto';

export type ConnState = 'signaling' | 'waiting' | 'connecting' | 'connected' | 'reconnecting' | 'ended';

export interface Transfer {
  id: number;
  name: string;
  direction: 'up' | 'down';
  size: number;
  done: number;
  state: 'active' | 'done' | 'error' | 'cancelled';
  message?: string;
}

export interface Listing {
  path: string;
  parent: string;
  entries: FileEntry[];
}

export interface RemoteEvents {
  hello?(h: Hello): void;
  status?(s: Status): void;
  conn?(c: ConnState): void;
  displays?(list: Display[], current: number): void;
  chat?(text: string, from: 'tech' | 'user'): void;
  clipboard?(text: string): void;
  listing?(l: Listing): void;
  transfer?(t: Transfer): void;
  ended?(reason: string): void;
  error?(msg: string): void;
  agentState?(s: { elevated: boolean }): void;
  stats?(s: { fps: number; kbps: number }): void;
}

const CHUNK = 64 * 1024;
const HIGH_WATER = 1 << 20;

export class RemoteSession {
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  // Candidates that arrive before the agent's answer is installed.
  private remoteSet = false;
  private pendingIce: RTCIceCandidateInit[] = [];
  private ch: Partial<Record<string, RTCDataChannel>> = {};
  private hello: Hello | null = null;
  private status: Status | null = null;
  private ctx: CanvasRenderingContext2D;
  private drawChain: Promise<void> = Promise.resolve();
  private pressed = new Set<string>();
  private pendingMove: { x: number; y: number } | null = null;
  private moveScheduled = false;
  private nextFileId = 1;
  private downloads = new Map<number, { t: Transfer; parts: Uint8Array[] }>();
  private uploads = new Map<number, Transfer>();
  private closed = false;
  private wsRetry = 0;
  private frames = 0;
  private bytes = 0;
  private statsTimer = 0;
  private detach: (() => void) | null = null;
  clipboardSync = false;
  private awaitingPaste = false;

  constructor(
    private sessionId: string,
    private canvas: HTMLCanvasElement,
    private on: RemoteEvents,
  ) {
    this.ctx = canvas.getContext('2d', { alpha: false })!;
  }

  start() {
    this.openSignaling();
    this.detach = this.attachInput();
    this.statsTimer = window.setInterval(() => {
      this.on.stats?.({ fps: this.frames, kbps: Math.round((this.bytes * 8) / 1000) });
      this.frames = 0;
      this.bytes = 0;
    }, 1000);
  }

  // Close the viewer without ending the session (e.g. navigating away).
  close() {
    this.closed = true;
    this.releaseKeys();
    this.detach?.();
    clearInterval(this.statsTimer);
    this.pc?.close();
    this.ws?.close();
  }

  // End the session for both sides.
  end() {
    this.signal('end', { reason: 'ended by tech' });
    this.close();
  }

  // ---- signaling ----

  private openSignaling() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/ws/tech/${encodeURIComponent(this.sessionId)}`);
    this.ws = ws;
    this.on.conn?.('signaling');
    ws.onopen = () => (this.wsRetry = 0);
    ws.onmessage = (e) => this.onSignal(JSON.parse(e.data) as Envelope);
    ws.onclose = () => {
      if (this.closed) return;
      // The server may be restarting or the network blipped; retry.
      this.wsRetry++;
      if (this.wsRetry > 10) {
        this.on.error?.('Lost connection to the server.');
        return;
      }
      setTimeout(() => !this.closed && this.openSignaling(), Math.min(1000 * this.wsRetry, 5000));
    };
  }

  private signal(type: string, data?: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ type, data }));
  }

  audit(kind: string, detail: string) {
    this.signal('event', { kind, detail });
  }

  requestElevation() {
    this.signal('elevate_request', {});
  }

  private onSignal(env: Envelope) {
    switch (env.type) {
      case 'hello':
        this.hello = env.data as Hello;
        this.on.hello?.(this.hello);
        break;
      case 'status': {
        const prev = this.status;
        this.status = env.data as Status;
        this.on.status?.(this.status);
        const ready = this.status.state === 'active' && this.status.agent_connected;
        if (!ready) {
          if (this.pc) {
            // Agent dropped (e.g. relaunching as admin); wait for it.
            this.teardownRTC();
            this.on.conn?.('reconnecting');
          } else {
            this.on.conn?.('waiting');
          }
        } else if (!this.pc || (prev && !prev.agent_connected)) {
          this.startRTC();
        }
        break;
      }
      case 'answer': {
        const pc = this.pc;
        if (!pc) break;
        pc.setRemoteDescription(env.data as RTCSessionDescriptionInit)
          .then(() => {
            if (pc !== this.pc) return;
            this.remoteSet = true;
            for (const c of this.pendingIce.splice(0)) pc.addIceCandidate(c).catch(() => {});
          })
          .catch((e) => this.on.error?.(String(e)));
        break;
      }
      case 'ice':
        if (!env.data || !this.pc) break;
        // addIceCandidate fails until the answer is installed, so hold
        // early candidates instead of dropping them.
        if (this.remoteSet) this.pc.addIceCandidate(env.data as RTCIceCandidateInit).catch(() => {});
        else this.pendingIce.push(env.data as RTCIceCandidateInit);
        break;
      case 'end':
        this.on.conn?.('ended');
        this.on.ended?.((env.data as { reason?: string })?.reason ?? 'Session ended');
        this.close();
        break;
      case 'error':
        this.on.error?.((env.data as { message: string }).message);
        break;
    }
  }

  // ---- WebRTC ----

  private teardownRTC() {
    this.remoteSet = false;
    this.pendingIce = [];
    this.pc?.close();
    this.pc = null;
    this.ch = {};
    this.releaseKeys();
  }

  private async startRTC() {
    this.teardownRTC();
    this.on.conn?.('connecting');
    const pc = new RTCPeerConnection({ iceServers: this.hello?.ice_servers ?? [] });
    this.pc = pc;

    const video = pc.createDataChannel(Channel.Video, { ordered: true });
    video.binaryType = 'arraybuffer';
    video.onmessage = (e) => this.onVideo(e.data as ArrayBuffer);

    const input = pc.createDataChannel(Channel.Input, { ordered: true });
    const control = pc.createDataChannel(Channel.Control, { ordered: true });
    control.onmessage = (e) => this.onControl(JSON.parse(e.data) as Control);
    control.onopen = () => {
      this.on.conn?.('connected');
      this.sendControl({ t: 'clip_sync', on: this.clipboardSync });
      this.sendControl({ t: 'refresh' });
    };

    const files = pc.createDataChannel(Channel.Files, { ordered: true });
    files.binaryType = 'arraybuffer';
    files.bufferedAmountLowThreshold = HIGH_WATER / 2;
    files.onmessage = (e) => this.onFiles(e.data);

    this.ch = { video, input, control, files };

    pc.onicecandidate = (e) => {
      if (e.candidate) this.signal('ice', e.candidate.toJSON());
    };
    pc.onconnectionstatechange = () => {
      if (pc !== this.pc) return;
      if (pc.connectionState === 'failed') {
        this.on.error?.('Could not establish a direct or relayed connection. Check the TURN server configuration.');
      } else if (pc.connectionState === 'disconnected') {
        this.on.conn?.('reconnecting');
      } else if (pc.connectionState === 'connected') {
        this.on.conn?.('connected');
      }
    };

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    this.signal('offer', { type: offer.type, sdp: offer.sdp });
  }

  // ---- screen ----

  private onVideo(buf: ArrayBuffer) {
    this.bytes += buf.byteLength;
    const m = parseVideo(buf);
    if (!m) return;
    if (m.kind === 'end') {
      this.drawChain = this.drawChain.then(() => {
        this.frames++;
        this.sendControl({ t: 'ack', seq: m.seq });
      });
      return;
    }
    const blob = new Blob([m.jpeg as BlobPart], { type: 'image/jpeg' });
    const decoded = createImageBitmap(blob);
    // Draw in arrival order even though decoding is asynchronous.
    this.drawChain = this.drawChain.then(async () => {
      let bmp: ImageBitmap;
      try {
        bmp = await decoded;
      } catch {
        return;
      }
      if (this.canvas.width !== m.screenW || this.canvas.height !== m.screenH) {
        this.canvas.width = m.screenW;
        this.canvas.height = m.screenH;
      }
      this.ctx.drawImage(bmp, m.x, m.y);
      bmp.close();
    });
  }

  // ---- control ----

  sendControl(c: Control) {
    const ch = this.ch.control;
    if (ch?.readyState === 'open') ch.send(JSON.stringify(c));
  }

  selectDisplay(id: number) {
    this.sendControl({ t: 'display', id });
  }

  setQuality(q: number) {
    this.sendControl({ t: 'quality', quality: q });
  }

  setClipboardSync(on: boolean) {
    this.clipboardSync = on;
    this.sendControl({ t: 'clip_sync', on });
  }

  sendClipboard(text: string) {
    this.sendControl({ t: 'clip', text });
  }

  sendChat(text: string) {
    this.sendControl({ t: 'chat', text });
    this.on.chat?.(text, 'tech');
  }

  private onControl(c: Control) {
    switch (c.t) {
      case 'displays':
        this.on.displays?.(c.displays ?? [], c.current ?? 0);
        break;
      case 'chat':
        if (c.text) this.on.chat?.(c.text, 'user');
        break;
      case 'clip':
        if (c.text !== undefined) this.on.clipboard?.(c.text);
        break;
      case 'state':
        this.on.agentState?.({ elevated: !!c.elevated });
        break;
      case 'notice':
        if (c.text) this.on.error?.(c.text);
        break;
    }
  }

  // ---- input ----

  private sendInput(msg: object) {
    const ch = this.ch.input;
    if (ch?.readyState === 'open') ch.send(JSON.stringify(msg));
  }

  sendKeys(codes: string[]) {
    for (const c of codes) this.sendInput({ t: 'kd', c });
    for (const c of [...codes].reverse()) this.sendInput({ t: 'ku', c });
  }

  private releaseKeys() {
    for (const c of this.pressed) this.sendInput({ t: 'ku', c });
    this.pressed.clear();
  }

  private point(e: MouseEvent) {
    const r = this.canvas.getBoundingClientRect();
    return {
      x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
      y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
    };
  }

  // Bring the remote modifier keys in line with the event's flags. Browsers
  // can miss modifier key events (e.g. Alt+Tab away, or Shift held before the
  // canvas got focus), which would otherwise leave keys stuck or missing.
  private syncModifiers(e: KeyboardEvent | MouseEvent) {
    const mods: [boolean, string, string][] = [
      [e.shiftKey, 'ShiftLeft', 'ShiftRight'],
      [e.ctrlKey, 'ControlLeft', 'ControlRight'],
      [e.altKey, 'AltLeft', 'AltRight'],
      [e.metaKey, 'MetaLeft', 'MetaRight'],
    ];
    const self = e instanceof KeyboardEvent ? e.code : '';
    for (const [on, left, right] of mods) {
      if (self === left || self === right) continue;
      const held = this.pressed.has(left) || this.pressed.has(right);
      if (on && !held) {
        this.pressed.add(left);
        this.sendInput({ t: 'kd', c: left });
      } else if (!on && held) {
        for (const code of [left, right]) {
          if (this.pressed.delete(code)) this.sendInput({ t: 'ku', c: code });
        }
      }
    }
  }

  private attachInput(): () => void {
    const c = this.canvas;
    const onMove = (e: PointerEvent) => {
      this.pendingMove = this.point(e);
      if (this.moveScheduled) return;
      this.moveScheduled = true;
      requestAnimationFrame(() => {
        this.moveScheduled = false;
        if (this.pendingMove) this.sendInput({ t: 'mm', ...this.pendingMove });
        this.pendingMove = null;
      });
    };
    const onDown = (e: PointerEvent) => {
      c.focus();
      e.preventDefault();
      c.setPointerCapture(e.pointerId);
      this.syncModifiers(e);
      this.sendInput({ t: 'mm', ...this.point(e) });
      this.sendInput({ t: 'md', b: e.button });
    };
    const onUp = (e: PointerEvent) => {
      e.preventDefault();
      this.sendInput({ t: 'mm', ...this.point(e) });
      this.sendInput({ t: 'mu', b: e.button });
    };
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const scale = e.deltaMode === 1 ? 40 : e.deltaMode === 2 ? 800 : 1;
      this.sendInput({ t: 'wh', dx: e.deltaX * scale, dy: e.deltaY * scale });
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (!e.code) return;
      this.syncModifiers(e);
      // Let Ctrl/Cmd+V fire a paste event first so the remote clipboard
      // gets the tech's text before the keystroke arrives.
      if (this.clipboardSync && e.code === 'KeyV' && (e.ctrlKey || e.metaKey)) {
        this.awaitingPaste = true;
        setTimeout(() => {
          if (this.awaitingPaste) {
            this.awaitingPaste = false;
            this.sendKeys(['KeyV']);
          }
        }, 300);
        return;
      }
      e.preventDefault();
      this.pressed.add(e.code);
      this.sendInput({ t: 'kd', c: e.code });
    };
    const onKeyUp = (e: KeyboardEvent) => {
      if (!e.code) return;
      e.preventDefault();
      if (e.code === 'KeyV' && !this.pressed.has('KeyV')) return;
      this.pressed.delete(e.code);
      this.sendInput({ t: 'ku', c: e.code });
    };
    const onPaste = (e: ClipboardEvent) => {
      if (document.activeElement !== c || !this.awaitingPaste) return;
      e.preventDefault();
      this.awaitingPaste = false;
      const text = e.clipboardData?.getData('text/plain');
      if (text) this.sendClipboard(text);
      // Give the agent a moment to set its clipboard.
      setTimeout(() => this.sendKeys(['KeyV']), 60);
    };
    const onBlur = () => this.releaseKeys();
    const noMenu = (e: Event) => e.preventDefault();

    c.addEventListener('pointermove', onMove);
    c.addEventListener('pointerdown', onDown);
    c.addEventListener('pointerup', onUp);
    c.addEventListener('wheel', onWheel, { passive: false });
    c.addEventListener('keydown', onKeyDown);
    c.addEventListener('keyup', onKeyUp);
    c.addEventListener('blur', onBlur);
    c.addEventListener('contextmenu', noMenu);
    document.addEventListener('paste', onPaste);
    window.addEventListener('blur', onBlur);
    return () => {
      c.removeEventListener('pointermove', onMove);
      c.removeEventListener('pointerdown', onDown);
      c.removeEventListener('pointerup', onUp);
      c.removeEventListener('wheel', onWheel);
      c.removeEventListener('keydown', onKeyDown);
      c.removeEventListener('keyup', onKeyUp);
      c.removeEventListener('blur', onBlur);
      c.removeEventListener('contextmenu', noMenu);
      document.removeEventListener('paste', onPaste);
      window.removeEventListener('blur', onBlur);
    };
  }

  // ---- files ----

  private sendFileMsg(m: FileMsg) {
    const ch = this.ch.files;
    if (ch?.readyState === 'open') ch.send(JSON.stringify(m));
  }

  listDir(path: string) {
    this.sendFileMsg({ t: 'list', id: 0, path });
  }

  private waitDrain(ch: RTCDataChannel): Promise<void> {
    if (ch.bufferedAmount < HIGH_WATER) return Promise.resolve();
    return new Promise((resolve) => {
      const done = () => {
        ch.removeEventListener('bufferedamountlow', done);
        ch.removeEventListener('close', done);
        resolve();
      };
      ch.addEventListener('bufferedamountlow', done);
      ch.addEventListener('close', done);
    });
  }

  async upload(file: File) {
    const ch = this.ch.files;
    if (ch?.readyState !== 'open') {
      this.on.error?.('Not connected.');
      return;
    }
    const id = this.nextFileId++;
    const t: Transfer = { id, name: file.name, direction: 'up', size: file.size, done: 0, state: 'active' };
    this.uploads.set(id, t);
    this.on.transfer?.({ ...t });
    this.sendFileMsg({ t: 'put_begin', id, name: file.name, size: file.size });
    for (let off = 0; off < file.size; off += CHUNK) {
      if (t.state !== 'active' || ch.readyState !== 'open') return;
      const data = new Uint8Array(await file.slice(off, off + CHUNK).arrayBuffer());
      await this.waitDrain(ch);
      ch.send(fileChunk(id, data) as Uint8Array<ArrayBuffer>);
      t.done = Math.min(file.size, off + data.byteLength);
      this.on.transfer?.({ ...t });
    }
    this.sendFileMsg({ t: 'put_end', id });
    this.audit('file_sent', `${file.name} (${file.size} bytes)`);
  }

  download(path: string) {
    const id = this.nextFileId++;
    const name = path.split(/[\\/]/).pop() || 'download';
    const t: Transfer = { id, name, direction: 'down', size: 0, done: 0, state: 'active' };
    this.downloads.set(id, { t, parts: [] });
    this.on.transfer?.({ ...t });
    this.sendFileMsg({ t: 'get', id, path });
  }

  cancel(id: number) {
    const d = this.downloads.get(id);
    const u = this.uploads.get(id);
    const t = d?.t ?? u;
    if (!t || t.state !== 'active') return;
    t.state = 'cancelled';
    this.sendFileMsg({ t: 'cancel', id });
    this.on.transfer?.({ ...t });
    this.downloads.delete(id);
  }

  private onFiles(data: string | ArrayBuffer) {
    if (typeof data !== 'string') {
      const c = parseFileChunk(data);
      const d = c && this.downloads.get(c.id);
      if (!d || !c) return;
      d.parts.push(new Uint8Array(c.data));
      d.t.done += c.data.byteLength;
      this.on.transfer?.({ ...d.t });
      return;
    }
    const m = JSON.parse(data) as FileMsg;
    switch (m.t) {
      case 'listing':
        this.on.listing?.({ path: m.path ?? '', parent: m.parent ?? '', entries: m.entries ?? [] });
        break;
      case 'get_begin': {
        const d = this.downloads.get(m.id);
        if (d) {
          d.t.size = m.size ?? 0;
          d.t.name = m.name ?? d.t.name;
          this.on.transfer?.({ ...d.t });
        }
        break;
      }
      case 'get_end': {
        const d = this.downloads.get(m.id);
        if (!d) break;
        this.downloads.delete(m.id);
        d.t.state = 'done';
        this.on.transfer?.({ ...d.t });
        const url = URL.createObjectURL(new Blob(d.parts as BlobPart[]));
        const a = document.createElement('a');
        a.href = url;
        a.download = d.t.name;
        a.click();
        setTimeout(() => URL.revokeObjectURL(url), 60_000);
        this.audit('file_received', `${d.t.name} (${d.t.size} bytes)`);
        break;
      }
      case 'put_done': {
        const u = this.uploads.get(m.id);
        if (u) {
          u.state = 'done';
          u.message = m.path ? `Saved to ${m.path}` : undefined;
          this.on.transfer?.({ ...u });
          this.uploads.delete(m.id);
        }
        break;
      }
      case 'error': {
        const t = this.downloads.get(m.id)?.t ?? this.uploads.get(m.id);
        if (t) {
          t.state = 'error';
          t.message = m.message;
          this.on.transfer?.({ ...t });
          this.downloads.delete(m.id);
          this.uploads.delete(m.id);
        } else if (m.message) {
          this.on.error?.(m.message);
        }
        break;
      }
    }
  }
}

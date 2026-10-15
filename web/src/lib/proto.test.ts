import { describe, expect, it } from 'vitest';
import { fileChunk, parseFileChunk, parseVideo } from './proto';

// Mirrors proto.AppendTile in Go.
function tile(seq: number, vals: number[], jpeg: number[]): ArrayBuffer {
  const buf = new ArrayBuffer(17 + jpeg.length);
  const v = new DataView(buf);
  v.setUint8(0, 0x54);
  v.setUint32(1, seq);
  vals.forEach((n, i) => v.setUint16(5 + i * 2, n));
  new Uint8Array(buf, 17).set(jpeg);
  return buf;
}

describe('parseVideo', () => {
  it('decodes tiles', () => {
    const m = parseVideo(tile(7, [128, 256, 64, 32, 1920, 1080], [0xff, 0xd8]));
    expect(m).toMatchObject({ kind: 'tile', seq: 7, x: 128, y: 256, w: 64, h: 32, screenW: 1920, screenH: 1080 });
    expect(m?.kind === 'tile' && Array.from(m.jpeg)).toEqual([0xff, 0xd8]);
  });

  it('decodes frame end', () => {
    const buf = new ArrayBuffer(5);
    new DataView(buf).setUint8(0, 0x46);
    new DataView(buf).setUint32(1, 42);
    expect(parseVideo(buf)).toEqual({ kind: 'end', seq: 42 });
  });

  it('rejects garbage', () => {
    expect(parseVideo(new ArrayBuffer(2))).toBeNull();
    expect(parseVideo(new Uint8Array([0x00, 0, 0, 0, 1, 0, 0]).buffer)).toBeNull();
  });
});

describe('file chunks', () => {
  it('round trips', () => {
    const c = fileChunk(513, new Uint8Array([1, 2, 3]));
    const p = parseFileChunk(c.buffer as ArrayBuffer);
    expect(p?.id).toBe(513);
    expect(Array.from(p!.data)).toEqual([1, 2, 3]);
  });
});

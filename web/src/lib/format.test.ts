import { describe, expect, it } from 'vitest';
import { formatBytes, formatCode, formatDuration } from './format';

describe('format', () => {
  it('formats codes as they are typed', () => {
    expect(formatCode('1')).toBe('1');
    expect(formatCode('1234')).toBe('123-4');
    expect(formatCode(' 123 456 789 0')).toBe('123-456-789');
  });

  it('formats bytes', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(25 * 1024 * 1024)).toBe('25 MB');
  });

  it('formats durations', () => {
    expect(formatDuration('2026-01-01T00:00:00Z', '2026-01-01T01:02:03Z')).toBe('1h 2m');
    expect(formatDuration('2026-01-01T00:00:00Z', '2026-01-01T00:00:09Z')).toBe('9s');
    expect(formatDuration('0001-01-01T00:00:00Z')).toBe('');
  });
});

// A 2D canvas context for happy-dom, which has none. The components under
// test draw into it and export it; what the tests pin is the value on the
// wire, so every method is a spy and `toDataURL` answers a fixed PNG.
import { vi } from 'vitest';

export const PNG_B64 = 'QUJDRA==';

export function mockCanvas2d() {
  const ctx = {
    setTransform: vi.fn(),
    clearRect: vi.fn(),
    scale: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    fillText: vi.fn(),
    drawImage: vi.fn(),
    measureText: vi.fn(() => ({ width: 40 })),
    lineWidth: 0,
    lineCap: '',
    lineJoin: '',
    strokeStyle: '',
    fillStyle: '',
    font: '',
    textBaseline: '',
  };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => ctx as unknown as CanvasRenderingContext2D);
  vi.spyOn(HTMLCanvasElement.prototype, 'toDataURL').mockImplementation(() => `data:image/png;base64,${PNG_B64}`);
  return ctx;
}

import { afterEach, describe, expect, it, vi } from 'vitest';
import { useFocusReturn } from './focus';

function button(id: string): HTMLButtonElement {
  const b = document.createElement('button');
  b.id = id;
  // jsdom has no layout: give the button a box, so it counts as visible
  b.getClientRects = () => [new DOMRect(0, 0, 10, 10)] as unknown as DOMRectList;
  document.body.appendChild(b);
  return b;
}

describe('useFocusReturn', () => {
  afterEach(() => {
    vi.useRealTimers();
    document.body.innerHTML = '';
  });

  it('gives the focus back to the opener when the dialog closes', () => {
    vi.useFakeTimers();
    const opener = button('opener');
    const f = useFocusReturn();
    opener.focus();
    f.onOpen();
    opener.blur();
    f.onClose(new Event('close'));
    vi.runAllTimers();
    expect(document.activeElement).toBe(opener);
  });

  // a fast user (or test) moves on before the dialog's close animation ends: the late return must
  // not take the focus away from what they focused meanwhile
  it('leaves the focus where it went while the dialog was closing', () => {
    vi.useFakeTimers();
    const opener = button('opener');
    const other = button('other');
    const f = useFocusReturn();
    opener.focus();
    f.onOpen();
    f.onClose(new Event('close'));
    other.focus();
    vi.runAllTimers();
    expect(document.activeElement).toBe(other);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { refocusAfterSwap, useFocusReturn } from './focus';

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

  // the dialog's close ends just as the user presses Undo in a row: the swap places the focus, not the
  // dialog's return to the page start (its opener is gone)
  it('leaves the focus to an Undo swap that is still pending', () => {
    vi.useFakeTimers();
    const main = document.createElement('main');
    main.innerHTML = '<h1>Devices</h1>';
    document.body.appendChild(main);
    const group = document.createElement('span');
    group.setAttribute('data-focus-group', '');
    document.body.appendChild(group);
    const undo = button('undo');
    undo.setAttribute('data-focus-swap', '');
    group.appendChild(undo);

    const opener = button('opener');
    const f = useFocusReturn();
    opener.focus();
    f.onOpen();
    opener.remove();
    f.onClose(new Event('close'));

    undo.focus();
    undo.addEventListener('click', (e) => refocusAfterSwap(e));
    undo.click();
    undo.remove();
    const remove = button('remove');
    remove.setAttribute('data-focus-swap', '');
    group.appendChild(remove);

    vi.runAllTimers();
    expect(document.activeElement).toBe(remove);
  });
});

// Where the keyboard focus goes when the control that had it disappears (plan §2.17 accessibility:
// keyboard access to every action in dialogs and drawers; WCAG 2.4.3 focus order). Without this the
// focus falls to <body> and a keyboard user starts again at the top of the page.

function visible(el: HTMLElement): boolean {
  return el.isConnected && el.getClientRects().length > 0 && !(el as HTMLButtonElement).disabled;
}

/** Focuses the page's heading (or the main region) when nothing better is left. */
export function focusPageStart(): void {
  const target = document.querySelector<HTMLElement>('main h1') ?? document.querySelector<HTMLElement>('main');
  if (!target) return;
  if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1');
  target.focus({ preventScroll: false });
}

/**
 * Gives the focus back to the element that had it before a dialog or drawer opened, or, when that
 * element is gone or hidden (the sticky bar's button, a Remove that became Undo), to the start of the
 * page. Does nothing while another dialog is open: its own focus trap holds the focus.
 */
export function returnFocus(opener: HTMLElement | null): void {
  if (document.querySelector('[role="dialog"][data-state="open"]')) return;
  if (opener && visible(opener)) opener.focus();
  else focusPageStart();
}

/**
 * The open and close handlers of a dialog's focus scope (Reka's DialogContent @open-auto-focus and
 * @close-auto-focus): remember who opened it, and return the focus on close as returnFocus says.
 */
export function useFocusReturn() {
  let opener: HTMLElement | null = null;
  return {
    onOpen() {
      const a = document.activeElement;
      opener = a instanceof HTMLElement && a !== document.body ? a : null;
    },
    onClose(e: Event) {
      e.preventDefault();
      const target = opener;
      opener = null;
      // after the dialog is gone and the page under it updated (Reka focuses in a timeout, too)
      setTimeout(() => returnFocus(target));
    },
  };
}

/**
 * After the next render, focuses the element the selector names (the control that took the place
 * of the one just used, in another row), or the start of the page when there is none.
 */
export function focusAfterUpdate(selector: string): void {
  setTimeout(() => {
    const el = document.querySelector<HTMLElement>(selector);
    if (el && visible(el)) el.focus();
    else focusPageStart();
  });
}

/**
 * For a button that is replaced when it is used (Remove ↔ Undo, Delete ↔ Undo, Adopt ↔ Undo): after
 * the swap, move the focus to the control that took its place, inside the closest
 * `[data-focus-group]` — the element marked `data-focus-swap`, else its first enabled button.
 */
export function refocusAfterSwap(e: Event): void {
  const group = (e.currentTarget as HTMLElement | null)?.closest<HTMLElement>('[data-focus-group]');
  if (!group) return;
  // after the update the caller is about to cause (it may change the state after this call)
  setTimeout(() => {
    const active = document.activeElement;
    if (active && active !== document.body && active.isConnected) return;
    if (!group.isConnected) return focusPageStart();
    const candidates = [...group.querySelectorAll<HTMLElement>('[data-focus-swap], button, a[href]')].filter(visible);
    const target = candidates.find((el) => el.hasAttribute('data-focus-swap')) ?? candidates[0];
    if (target) target.focus();
    else focusPageStart();
  });
}

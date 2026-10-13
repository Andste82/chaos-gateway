// The shared components of plan §2.17 (visual system): what they render and the accessibility
// they promise (labels, aria-pressed, role="switch", aria-live, keyboard in dialogs).
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';
import { noteGatewayDate, resetGatewayClock } from '../../lib/clock';
import { useToastStore } from '../../stores/toasts';
import AccessChip from './AccessChip.vue';
import AppBanner from './AppBanner.vue';
import CountdownBanner from './CountdownBanner.vue';
import EmptyState from './EmptyState.vue';
import FaultChip from './FaultChip.vue';
import ModalDialog from './ModalDialog.vue';
import SectionCard from './SectionCard.vue';
import SegmentedControl from './SegmentedControl.vue';
import SideDrawer from './SideDrawer.vue';
import StatusDot from './StatusDot.vue';
import StickyActionBar from './StickyActionBar.vue';
import SwitchToggle from './SwitchToggle.vue';
import ToastHost from './ToastHost.vue';

beforeEach(() => setActivePinia(createPinia()));
afterEach(() => {
  vi.useRealTimers();
  resetGatewayClock();
  document.body.innerHTML = '';
});

describe('chips and dots', () => {
  it('a status dot always carries its label', () => {
    const w = mount(StatusDot, { props: { tone: 'offline', label: 'Offline' } });
    expect(w.text()).toBe('Offline');
    expect(w.attributes('data-tone')).toBe('offline');
    expect(w.get('[aria-hidden="true"]').attributes('style')).toContain('var(--color-offline)');
  });

  it('a fault chip is orange and mono, and shows a cut text whole on hover', () => {
    const w = mount(FaultChip, { props: { text: '↑↓ 200 ms ±50' } });
    expect(w.text()).toBe('↑↓ 200 ms ±50');
    expect(w.classes()).toEqual(expect.arrayContaining(['mono', 'text-fault-text', 'bg-fault-tint', 'truncate']));
    expect(w.attributes('title')).toBe('↑↓ 200 ms ±50');
  });

  it('an access chip is blue for Allow and red for everything that blocks', () => {
    const allow = mount(AccessChip, { props: { action: 'allow' } });
    expect(allow.text()).toBe('Allow');
    expect(allow.classes()).toContain('text-accent-text');
    for (const [action, label] of [
      ['drop', 'Drop'],
      ['reject', 'Reject'],
      ['reset', 'TCP reset'],
    ] as const) {
      const w = mount(AccessChip, { props: { action } });
      expect(w.text()).toBe(label);
      expect(w.classes()).toContain('text-block-text');
    }
  });
});

describe('controls', () => {
  it('a segmented control is a group of buttons with aria-pressed', async () => {
    const value = ref('all');
    const w = mount(
      defineComponent(() => () =>
        h(SegmentedControl, {
          options: [
            { value: 'all', label: 'All', count: 3 },
            { value: 'online', label: 'Online', count: 2 },
          ],
          label: 'Filter',
          variant: 'chips',
          modelValue: value.value,
          'onUpdate:modelValue': (v: string) => (value.value = v),
        }),
      ),
    );
    expect(w.get('[role="group"]').attributes('aria-label')).toBe('Filter');
    const buttons = w.findAll('button');
    expect(buttons.map((b) => b.attributes('aria-pressed'))).toEqual(['true', 'false']);
    expect(buttons[1]!.text()).toBe('Online 2');
    await buttons[1]!.trigger('click');
    expect(value.value).toBe('online');
    expect(w.findAll('button').map((b) => b.attributes('aria-pressed'))).toEqual(['false', 'true']);
  });

  it('a switch has role="switch" with its state and its label', async () => {
    const on = ref(true);
    const w = mount(
      defineComponent(() => () => h(SwitchToggle, { label: 'DHCP', modelValue: on.value, 'onUpdate:modelValue': (v: boolean) => (on.value = v) })),
    );
    const sw = w.get('[role="switch"]');
    expect(sw.attributes('aria-checked')).toBe('true');
    expect(sw.attributes('aria-label')).toBe('DHCP');
    await sw.trigger('click');
    expect(on.value).toBe(false);
    expect(w.get('[role="switch"]').attributes('aria-checked')).toBe('false');
  });
});

describe('containers', () => {
  it('a card is a section labelled by its title', () => {
    const w = mount(SectionCard, { props: { title: 'Active faults' }, slots: { default: 'body', actions: 'link' } });
    const id = w.get('h2').attributes('id');
    expect(w.get('section').attributes('aria-labelledby')).toBe(id);
    expect(w.text()).toContain('Active faults');
    expect(w.text()).toContain('link');
  });

  it('an empty state is one sentence and the primary action', () => {
    const w = mount(EmptyState, { props: { text: 'No devices yet.' }, slots: { default: '<button>Add</button>' } });
    expect(w.get('p').text()).toBe('No devices yet.');
    expect(w.find('button').exists()).toBe(true);
    expect(w.classes()).toContain('border-dashed');
  });

  it('a sticky action bar is a labelled region with its actions', () => {
    const w = mount(StickyActionBar, {
      props: { label: 'Unapplied changes' },
      slots: { default: '3 changes in 2 rules · not applied yet', actions: '<button>Discard</button><button>Preview & apply</button>' },
    });
    expect(w.attributes('role')).toBe('region');
    expect(w.attributes('aria-label')).toBe('Unapplied changes');
    expect(w.classes()).toContain('sticky');
    expect(w.findAll('button')).toHaveLength(2);
  });

  it('a banner is an alert; safe mode is red', () => {
    const w = mount(AppBanner, { props: { kind: 'safe-mode', title: 'Safe mode' }, slots: { default: 'reason' } });
    expect(w.attributes('role')).toBe('alert');
    expect(w.classes()).toContain('text-block-text');
    expect(w.text()).toContain('reason');
  });
});

describe('countdown banner', () => {
  it('counts down and says when the time is up', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 1, 12, 0, 0));
    const deadline = new Date(2026, 0, 1, 12, 0, 3).toISOString();
    const w = mount(CountdownBanner, { props: { deadline, text: 'Revision 7 waits' }, slots: { default: '<button>Confirm</button>' } });
    const timer = w.get('[role="timer"]');
    expect(timer.text()).toBe('0:03 left');
    expect(w.emitted('expired')).toBeUndefined();
    vi.advanceTimersByTime(1000);
    await nextTick();
    expect(w.get('[role="timer"]').text()).toBe('0:02 left');
    vi.advanceTimersByTime(2500);
    await nextTick();
    expect(w.get('[role="timer"]').text()).toBe('0:00 left');
    expect(w.emitted('expired')).toHaveLength(1);
    expect(w.get('[data-testid="countdown-announcement"]').text()).toBe('The time to confirm is up.');
    expect(w.text()).toContain('Revision 7 waits');
    w.unmount();
  });

  it('screen readers hear the milestones, not every second', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 1, 12, 0, 0));
    const deadline = new Date(2026, 0, 1, 12, 0, 45).toISOString();
    const w = mount(CountdownBanner, { props: { deadline, text: 'Revision 7 waits' } });
    expect(w.get('[role="timer"]').attributes('aria-live')).toBe('off');
    const live = w.get('[data-testid="countdown-announcement"]');
    expect(live.attributes('aria-live')).toBe('polite');
    expect(live.text()).toBe('');
    vi.advanceTimersByTime(10_000);
    await nextTick();
    expect(live.text()).toBe('');
    vi.advanceTimersByTime(5_000);
    await nextTick();
    expect(live.text()).toBe('30 seconds left to confirm.');
    vi.advanceTimersByTime(10_000);
    await nextTick();
    expect(live.text()).toBe('30 seconds left to confirm.');
    vi.advanceTimersByTime(10_000);
    await nextTick();
    expect(live.text()).toBe('10 seconds left to confirm.');
    w.unmount();
  });

  it("runs on the gateway's clock, not the browser's", async () => {
    vi.useFakeTimers();
    // the browser is 90 s ahead of the gateway; the gateway gives 60 s to confirm
    vi.setSystemTime(new Date(Date.UTC(2026, 0, 1, 12, 1, 30)));
    noteGatewayDate('Thu, 01 Jan 2026 12:00:00 GMT', Date.now(), Date.now());
    const deadline = new Date(Date.UTC(2026, 0, 1, 12, 1, 0)).toISOString();
    const w = mount(CountdownBanner, { props: { deadline, text: 'Revision 1 waits' } });
    expect(w.emitted('expired')).toBeUndefined();
    expect(w.get('[role="timer"]').text()).toMatch(/^(0:59|1:00) left$/);
    vi.advanceTimersByTime(61_000);
    await nextTick();
    expect(w.emitted('expired')).toHaveLength(1);
    w.unmount();
  });
});

describe('dialogs', () => {
  it('a modal dialog is labelled by its title and closes with Escape', async () => {
    const open = ref(true);
    mount(
      defineComponent(() => () =>
        h(
          ModalDialog,
          { title: 'Adopt device', open: open.value, 'onUpdate:open': (v: boolean) => (open.value = v) },
          { default: () => h('input', { 'aria-label': 'Name' }) },
        ),
      ),
      { attachTo: document.body },
    );
    await flushPromises();
    const dialog = document.querySelector('[role="dialog"]')!;
    expect(dialog).not.toBeNull();
    const title = document.getElementById(dialog.getAttribute('aria-labelledby')!);
    expect(title?.textContent).toBe('Adopt device');
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flushPromises();
    expect(open.value).toBe(false);
  });

  it('a side drawer carries its badge ("dry run · nothing changed yet")', async () => {
    mount(SideDrawer, { props: { title: 'Preview', badge: 'dry run · nothing changed yet', open: true }, attachTo: document.body });
    await flushPromises();
    const dialog = document.querySelector('[role="dialog"]')!;
    expect(dialog.textContent).toContain('dry run · nothing changed yet');
    expect(dialog.className).toContain('right-0');
  });
});

describe('toasts', () => {
  it('show what the store holds, in a live region, and can be dismissed', async () => {
    mount(ToastHost, { attachTo: document.body });
    const store = useToastStore();
    store.push('success', 'Revision 43 applied and verified in 87 ms');
    await flushPromises();
    expect(document.body.textContent).toContain('Revision 43 applied and verified in 87 ms');
    // the viewport is a labelled region (Reka UI announces each toast in a live region of its own)
    expect(document.querySelector('[role="region"] ol li')?.textContent).toContain('Revision 43 applied');
    (document.querySelector('[aria-label="Dismiss"]') as HTMLElement).click();
    await flushPromises();
    expect(store.toasts).toHaveLength(0);
  });
});

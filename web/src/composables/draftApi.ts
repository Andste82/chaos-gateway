// The `</> API` part of the scenario editors: while a dialog is open, the API panel holds the request its
// "Add to the changes" leads to (the merge patch of the preview, with the base revision in `If-Match`).
import { watch, type Ref } from 'vue';
import type { Scenario } from '../api/generated/model';
import { useApiPanelStore } from '../stores/apiPanel';
import { useDraftStore } from '../stores/draft';

/**
 * Keeps `apiPanel.draft` at the request that saving `value()` as scenario `id()` would lead to, while
 * `open` is true, and clears it when the dialog closes.
 */
export function useScenarioApi(open: Ref<boolean>, title: () => string, id: () => string, value: () => Scenario): void {
  const draft = useDraftStore();
  const apiPanel = useApiPanelStore();
  watch(
    [open, title, id, value],
    () => {
      if (!open.value) {
        apiPanel.draft = undefined;
        return;
      }
      apiPanel.draft = {
        title: `${title()} (configuration)`,
        calls: [
          {
            method: 'POST',
            path: '/revisions',
            headers: { 'Content-Type': 'application/merge-patch+json', 'If-Match': `"${draft.base}"` },
            body: { scenarios: { [id()]: draft.objectPatch('scenario', id(), value()) } },
          },
        ],
      };
    },
    { deep: true, immediate: true },
  );
}

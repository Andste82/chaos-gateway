<script setup lang="ts">
// Rotate a WireGuard client's keys (plan §2.2.1, P3-M14-01). With keys the gateway generates, the
// rotation raises the key generation: the gateway makes a new key pair (and a new preshared key)
// when the revision is applied, and the client needs its new configuration afterwards. With the
// client's own key, the rotation is its new public key (the gateway refuses a generation for it).
// Either goes into the configuration draft, like the other client actions.
import { computed, ref, useId, watch } from 'vue';
import type { WireGuardClient } from '../../api/generated/model';
import { generatesKeys, rotatedKeyError, withRotatedKeys } from '../../lib/networks';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{ client?: WireGuardClient }>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ rotate: [client: WireGuardClient] }>();
const uid = useId();

const publicKey = ref('');
const touched = ref(false);
watch(open, (o) => {
  if (o) {
    publicKey.value = '';
    touched.value = false;
  }
});

const generated = computed(() => !!props.client && generatesKeys(props.client));
const error = computed(() => (props.client && !generated.value ? rotatedKeyError(props.client, publicKey.value) : undefined));

function submit() {
  touched.value = true;
  if (!props.client || error.value) return;
  emit('rotate', withRotatedKeys(props.client, publicKey.value));
  open.value = false;
}
</script>

<template>
  <ModalDialog v-model:open="open" :title="`Rotate the keys of ${client?.name ?? 'the client'}`" width="520px">
    <form class="flex flex-col gap-3" novalidate data-testid="rotate-keys" @submit.prevent="submit">
      <template v-if="generated">
        <p class="m-0 text-sm">
          The gateway makes a new key pair{{ client?.key?.preshared_key ? ' and a new preshared key' : '' }} when the change is applied. From then
          on the client's current configuration no longer connects: give it its new configuration (QR code or download).
        </p>
      </template>
      <template v-else>
        <p class="m-0 text-sm">The client has its own key: enter the public key of its new key pair. The gateway never holds its private key.</p>
        <div>
          <label class="label" :for="`${uid}-key`">New public key</label>
          <input
            :id="`${uid}-key`"
            v-model="publicKey"
            class="input mono"
            autocomplete="off"
            :aria-invalid="touched && !!error"
            :aria-describedby="touched && error ? `${uid}-key-error` : undefined"
          />
          <p v-if="touched && error" :id="`${uid}-key-error`" class="field-error">{{ error }}</p>
        </div>
      </template>
      <p class="m-0 text-[13px] text-text-2">The rotation goes into the unapplied changes; nothing changes before the preview and the apply.</p>
      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary">Rotate keys</button>
      </div>
    </form>
  </ModalDialog>
</template>

<script setup lang="ts">
// The fields of a TLS case (plan §2.8, §2.17 "Add fault", M23b): the case the TLS responder plays (a certificate the
// device must reject, or a handshake that fails), the connections it selects (ports, a destination, the server names of the
// ClientHello) and whether it cuts connections that exist, with what each case needs on the device (the test CA) and the
// download of that CA. The "Add fault" dialog and the `tls` step of the step dialog share them. Each message stands under
// its field (`errors` by field name).
import { computed, useId } from 'vue';
import { interceptLabel, tlsCases, type TlsForm } from '../../lib/tlsForm';
import TestCaLink from '../ui/TestCaLink.vue';
import TrafficFields from './TrafficFields.vue';

const props = defineProps<{
  errors: Partial<Record<string, string>>;
  /** Whether the device the case is aimed at trusts the test CA (`trusts_test_ca`), when it is one device and that is known. */
  trust?: boolean;
  /** The device's name, for the note about the test CA. */
  deviceName?: string;
}>();
const form = defineModel<TlsForm>({ required: true });
const uid = useId();
const info = computed(() => tlsCases.find((c) => c.value === form.value.case));
const intercept = computed(() => form.value.case === 'intercept');
/** What the case needs of the device, in the words of plan §2.8. */
const trustNote = computed(() => {
  if (!info.value?.testCa) return undefined;
  if (props.trust === true) return `${props.deviceName ?? 'The device'} trusts the test CA: it must reject this certificate with the specific error.`;
  if (props.trust === false) return `${props.deviceName ?? 'The device'} does not trust the test CA: it rejects this certificate as an unknown issuer, like every other case.`;
  return 'Only a device that trusts the test CA (a development firmware) can tell this case from an unknown issuer; any other rejects it as such.';
});
const groups = ['Certificate', 'Handshake'] as const;
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="tls-fields">
    <div>
      <label class="label" :for="fid('case')">Case</label>
      <select
        :id="fid('case')"
        v-model="form.case"
        class="input"
        :aria-invalid="!!errors.tlsCase"
        :aria-describedby="errors.tlsCase ? eid('case') : fid('hint')"
      >
        <optgroup v-for="g in groups" :key="g" :label="g">
          <option v-for="c in tlsCases.filter((x) => x.group === g)" :key="c.value" :value="c.value">{{ c.label }}</option>
        </optgroup>
        <option v-if="intercept" value="intercept">{{ interceptLabel }}</option>
      </select>
      <p v-if="errors.tlsCase" :id="eid('case')" class="field-error">{{ errors.tlsCase }}</p>
      <div v-else :id="fid('hint')" class="mt-1 flex flex-col gap-1 text-[13px] text-text-2">
        <p v-if="info" class="m-0" data-testid="tls-hint">{{ info.hint }}</p>
        <p v-else-if="intercept" class="m-0" data-testid="tls-hint">
          The interception proxy answers instead of the server. Its settings and HTTP rules are not edited here: saving keeps them as they are.
        </p>
        <p v-if="trustNote" class="m-0" data-testid="tls-trust">{{ trustNote }}</p>
      </div>
      <p class="m-0 mt-1 text-[13px]"><TestCaLink /></p>
    </div>

    <TrafficFields v-model="form.traffic" :errors="errors" no-protocol ports-placeholder="443, 8883" />

    <div>
      <label class="label" :for="fid('sni')">Server names</label>
      <input
        :id="fid('sni')"
        v-model="form.sni"
        class="input mono"
        placeholder="every connection"
        :aria-invalid="!!errors.tlsSni"
        :aria-describedby="errors.tlsSni ? eid('sni') : fid('sni-hint')"
      />
      <p v-if="errors.tlsSni" :id="eid('sni')" class="field-error">{{ errors.tlsSni }}</p>
      <p v-else :id="fid('sni-hint')" class="m-0 mt-1 text-[13px] text-text-2">
        Only connections whose ClientHello asks for one of these names (<span class="mono">broker.example.com</span> or <span class="mono">*.example.com</span>), separated by commas; empty: every selected connection. Without ports the case selects 443 and 8883.
      </p>
    </div>

    <label class="flex items-start gap-2 text-sm">
      <input v-model="form.cutExisting" type="checkbox" class="mt-1" :aria-invalid="!!errors.tlsCut" />
      <span>
        Also cut existing connections
        <span class="block text-[13px] text-text-2">Without it only new connections meet the case. With it a connection that is open already is selected at its next packet and reset; the device can reconnect.</span>
      </span>
    </label>
    <p v-if="errors.tlsCut" class="field-error m-0">{{ errors.tlsCut }}</p>
  </div>
</template>

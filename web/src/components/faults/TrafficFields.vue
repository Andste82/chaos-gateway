<script setup lang="ts">
// The selector of traffic (plan §2.4 `TrafficMatch`): protocol, ports and destination (anywhere, the uplink, a
// network, an address or prefix, a host name). The fault dialog's custom traffic and the steps and checks of a
// scenario share it. Each message stands under its field (`errors` by field name).
import { computed, useId } from 'vue';
import { useNames } from '../../composables/gateway';
import type { TrafficForm } from '../../lib/trafficForm';

defineProps<{
  errors: Partial<Record<string, string>>;
  /** Leave the destination out (a selector that has ports only). */
  noDestination?: boolean;
  /** Leave the protocol out (a selector that is TCP by its kind: a TLS case). */
  noProtocol?: boolean;
  /** What an empty ports field means, when it is not "any port" (a TLS case: 443 and 8883). */
  portsPlaceholder?: string;
}>();
const form = defineModel<TrafficForm>({ required: true });
const uid = useId();
const { names, networks } = useNames();
const nets = computed(() => (networks.data.value ?? []).map((n) => ({ id: n.id, name: names.value.network?.(n.id) ?? n.id })));

/** Another kind of destination starts with its first network, or an empty address or name. */
function destKindChanged() {
  form.value.destination = form.value.destKind === 'network' ? (nets.value[0]?.id ?? '') : '';
}
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="grid gap-3" :class="noProtocol ? 'grid-cols-3' : 'grid-cols-4'" data-testid="traffic-fields">
    <div v-if="!noProtocol">
      <label class="label" :for="fid('protocol')">Protocol</label>
      <select :id="fid('protocol')" v-model="form.protocol" class="input">
        <option value="any">Any</option>
        <option value="tcp">TCP</option>
        <option value="udp">UDP</option>
        <option value="icmp">ICMP</option>
      </select>
    </div>
    <div>
      <label class="label" :for="fid('ports')">Ports</label>
      <input
        :id="fid('ports')"
        v-model="form.ports"
        class="input mono"
        :placeholder="portsPlaceholder ?? 'any'"
        :aria-invalid="!!errors.ports"
        :aria-describedby="errors.ports ? eid('ports') : undefined"
      />
      <p v-if="errors.ports" :id="eid('ports')" class="field-error">{{ errors.ports }}</p>
    </div>
    <template v-if="!noDestination">
      <div>
        <label class="label" :for="fid('destKind')">Destination</label>
        <select :id="fid('destKind')" v-model="form.destKind" class="input" @change="destKindChanged">
          <option value="any">Anywhere</option>
          <option value="uplink">Uplink (Internet)</option>
          <option value="network">A network</option>
          <option value="cidr">An address or prefix</option>
          <option value="hostname">A host name (DNS-derived)</option>
        </select>
      </div>
      <div v-if="form.destKind === 'network' || form.destKind === 'cidr' || form.destKind === 'hostname'">
        <label class="label" :for="fid('destination')">{{ { network: 'Network', cidr: 'Address or prefix', hostname: 'Host name' }[form.destKind] }}</label>
        <select
          v-if="form.destKind === 'network'"
          :id="fid('destination')"
          v-model="form.destination"
          class="input"
          :aria-invalid="!!errors.destination"
          :aria-describedby="errors.destination ? eid('destination') : undefined"
        >
          <option v-for="n in nets" :key="n.id" :value="n.id">{{ n.name }}</option>
          <option v-if="form.destination && !nets.some((n) => n.id === form.destination)" :value="form.destination">{{ form.destination }}</option>
        </select>
        <input
          v-else
          :id="fid('destination')"
          v-model="form.destination"
          class="input mono"
          :placeholder="form.destKind === 'cidr' ? '203.0.113.0/24' : 'broker.example.com'"
          :aria-invalid="!!errors.destination"
          :aria-describedby="errors.destination ? eid('destination') : undefined"
        />
        <p v-if="errors.destination" :id="eid('destination')" class="field-error">{{ errors.destination }}</p>
      </div>
    </template>
  </div>
</template>

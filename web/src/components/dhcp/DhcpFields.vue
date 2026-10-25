<script setup lang="ts">
// The fields of a DHCP test action (plan §2.7, §2.17 "Device detail" and "Networks", M23b): the action, and what it needs
// (the lease time of a short lease, the address a device is moved to, the options to replace). The DHCP dialog of the device
// detail and the network card and the `dhcp` step of the step dialog share them. A network has no "Force a new IP" (a
// reservation is a device's). Each message stands under its field (`errors` by field name).
import { computed, useId } from 'vue';
import { dhcpActions, dhcpActionsFor, type DhcpForm, type DhcpScope } from '../../lib/dhcpForm';

const props = defineProps<{
  errors: Partial<Record<string, string>>;
  /** What the action is aimed at; undefined (a step whose target is neither) offers every action. */
  scope?: DhcpScope;
  /** The action cannot be changed: an action that is edited keeps its key. */
  locked?: boolean;
}>();
const form = defineModel<DhcpForm>({ required: true });
const uid = useId();
const actions = computed(() => (props.scope ? dhcpActionsFor(props.scope) : dhcpActions));
const hint = computed(() => dhcpActions.find((a) => a.value === form.value.action)?.hint);
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="dhcp-fields">
    <div>
      <label class="label" :for="fid('action')">Action</label>
      <select
        :id="fid('action')"
        v-model="form.action"
        class="input"
        :disabled="locked"
        :aria-invalid="!!errors.dhcpAction"
        :aria-describedby="errors.dhcpAction ? eid('action') : fid('hint')"
      >
        <option v-for="a in actions" :key="a.value" :value="a.value">{{ a.label }}</option>
      </select>
      <p v-if="errors.dhcpAction" :id="eid('action')" class="field-error">{{ errors.dhcpAction }}</p>
      <p v-else :id="fid('hint')" class="m-0 mt-1 text-[13px] text-text-2" data-testid="dhcp-hint">{{ hint }}</p>
    </div>

    <div v-if="form.action === 'short_lease'" class="max-w-[200px]">
      <label class="label" :for="fid('lease')">Lease time</label>
      <input
        :id="fid('lease')"
        v-model="form.leaseTime"
        class="input mono"
        placeholder="30s"
        :aria-invalid="!!errors.leaseTime"
        :aria-describedby="errors.leaseTime ? eid('lease') : undefined"
      />
      <p v-if="errors.leaseTime" :id="eid('lease')" class="field-error">{{ errors.leaseTime }}</p>
    </div>

    <div v-if="form.action === 'force_new_ip'" class="max-w-[260px]">
      <label class="label" :for="fid('ip')">New address</label>
      <input
        :id="fid('ip')"
        v-model="form.ip"
        class="input mono"
        placeholder="chosen by the gateway"
        :aria-invalid="!!errors.ip"
        :aria-describedby="errors.ip ? eid('ip') : fid('ip-hint')"
      />
      <p v-if="errors.ip" :id="eid('ip')" class="field-error">{{ errors.ip }}</p>
      <p v-else :id="fid('ip-hint')" class="m-0 mt-1 text-[13px] text-text-2">
        Empty: the gateway takes a free address from the end of the last pool, and shows it once the action is applied.
      </p>
    </div>

    <template v-if="form.action === 'set_options'">
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" :for="fid('router')">Router (option 3)</label>
          <input
            :id="fid('router')"
            v-model="form.router"
            class="input mono"
            placeholder="unchanged"
            :aria-invalid="!!errors.router"
            :aria-describedby="errors.router ? eid('router') : undefined"
          />
          <p v-if="errors.router" :id="eid('router')" class="field-error">{{ errors.router }}</p>
        </div>
        <div>
          <label class="label" :for="fid('dns')">DNS servers (option 6)</label>
          <input
            :id="fid('dns')"
            v-model="form.dnsServers"
            class="input mono"
            placeholder="unchanged"
            :aria-invalid="!!errors.dhcpDns"
            :aria-describedby="errors.dhcpDns ? eid('dns') : undefined"
          />
          <p v-if="errors.dhcpDns" :id="eid('dns')" class="field-error">{{ errors.dhcpDns }}</p>
        </div>
        <div>
          <label class="label" :for="fid('domain')">Domain (option 15)</label>
          <input
            :id="fid('domain')"
            v-model="form.domain"
            class="input mono"
            :disabled="form.removeDomain"
            placeholder="unchanged"
            :aria-invalid="!!errors.domain"
            :aria-describedby="errors.domain ? eid('domain') : undefined"
          />
          <p v-if="errors.domain" :id="eid('domain')" class="field-error">{{ errors.domain }}</p>
          <label class="mt-1.5 flex items-center gap-2 text-[13px] text-text-2">
            <input v-model="form.removeDomain" type="checkbox" />
            Take the domain away
          </label>
        </div>
        <div>
          <label class="label" :for="fid('ntp')">NTP servers (option 42)</label>
          <input
            :id="fid('ntp')"
            v-model="form.ntp"
            class="input mono"
            placeholder="unchanged"
            :aria-invalid="!!errors.ntp"
            :aria-describedby="errors.ntp ? eid('ntp') : undefined"
          />
          <p v-if="errors.ntp" :id="eid('ntp')" class="field-error">{{ errors.ntp }}</p>
        </div>
      </div>
      <div>
        <label class="label" :for="fid('custom')">Custom options (code and value per line)</label>
        <textarea
          :id="fid('custom')"
          v-model="form.custom"
          class="input mono !h-auto py-2"
          rows="2"
          placeholder="66 tftp.lab"
          :aria-invalid="!!errors.custom"
          :aria-describedby="errors.custom ? eid('custom') : undefined"
        />
        <p v-if="errors.custom" :id="eid('custom')" class="field-error">{{ errors.custom }}</p>
      </div>
      <p v-if="errors.options" class="field-error m-0">{{ errors.options }}</p>
      <p v-else class="m-0 text-[13px] text-text-2">A field left empty keeps the option the network is configured with.</p>
    </template>
  </div>
</template>

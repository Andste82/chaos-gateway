<script setup lang="ts">
// The download of the test CA (plan §2.8, §2.17, M23b): the certificate (PEM) of the CA that signs the expired, not-yet-valid
// and hostname-mismatch certificates of the TLS responder, to install on development firmware. A plain link to
// `GET /tls/ca`, which answers with a `Content-Disposition: attachment`, so the page stays where it is and the browser saves
// the file (no `download` attribute, as for a capture: the file name is the gateway's). The unknown CA is never offered.
import { testCaUrl } from '../../lib/tlsForm';

defineProps<{ button?: boolean; label?: string }>();
</script>

<template>
  <a
    :href="testCaUrl"
    :class="button ? 'btn' : 'text-[13px]'"
    title="The certificate to install on a development firmware, so that it can tell the expired, not-yet-valid and hostname-mismatch cases from an unknown issuer"
    data-testid="test-ca-download"
    >{{ label ?? 'Download the test CA' }}</a
  >
</template>

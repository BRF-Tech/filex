<script setup lang="ts">
/**
 * The red warning for ONLYOFFICE without a JWT secret (lib/onlyofficeSecret).
 *
 * ⚠ Persistent by design (owner, 2026-09-22): there is no close button and no
 * "don't show again" — it goes away when, and only when, a secret is set. It
 * is drawn on the Panel and on External services; the Panel's copy links to
 * the page where the secret is entered.
 *
 * Reads the external-services store and fetches it once when nothing has
 * loaded it yet. A failure to load (a tenant admin has no such page) draws
 * nothing rather than a warning it cannot back up.
 */
import { computed, onMounted } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ShieldAlert } from 'lucide-vue-next';

import { useExternalServicesStore } from '@/stores/external';
import { onlyofficeWithoutSecret } from '@/lib/onlyofficeSecret';

const props = defineProps<{
  /** No link to External services — on that page itself. */
  noLink?: boolean;
}>();

const { t } = useI18n();
const ext = useExternalServicesStore();

onMounted(() => {
  if (!ext.items.length && !ext.loading) void ext.fetch().catch(() => {});
});

const show = computed(() => onlyofficeWithoutSecret(ext.items));
const withLink = computed(() => !props.noLink);
</script>

<template>
  <div v-if="show" class="oo-nosecret" role="alert" data-testid="onlyoffice-no-secret">
    <ShieldAlert class="oo-nosecret__icon" aria-hidden="true" />
    <div class="oo-nosecret__text">
      <p class="oo-nosecret__title">{{ t('external.noSecret.title') }}</p>
      <!-- ⚠ The variable's NAME is not in the translatable sentence: it is a
           `{env}` slot drawn as <code>, the way every other environment
           variable filex names on screen is (login.noProviders,
           editor.missingPath). A name a translator can retype is a name a
           translator can get wrong. -->
      <i18n-t keypath="external.noSecret.body" tag="p"><template #env><code>JWT_SECRET</code></template></i18n-t>
      <RouterLink v-if="withLink" :to="{ name: 'external' }" class="oo-nosecret__link">
        {{ t('external.noSecret.action') }}
      </RouterLink>
    </div>
  </div>
</template>

<style scoped>
/* The palette's danger colour, not a frozen Tailwind red: the warning moves
   with the theme the operator picked, like the rest of the panel. */
.oo-nosecret {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--fe-danger);
  border-radius: var(--fe-radius-lg, 12px);
  background: color-mix(in srgb, var(--fe-danger) 9%, var(--fe-bg-elev, transparent));
  color: var(--fe-text);
  font-size: 14px;
  line-height: 1.5;
}
.oo-nosecret__icon {
  flex: none;
  width: 20px;
  height: 20px;
  margin-top: 2px;
  color: var(--fe-danger);
}
.oo-nosecret__text {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.oo-nosecret__title {
  font-weight: 600;
  color: var(--fe-danger);
}
.oo-nosecret__link {
  align-self: flex-start;
  font-weight: 600;
  color: var(--fe-danger);
  text-decoration: underline;
}
</style>

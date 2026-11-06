<script setup lang="ts">
/**
 * What stands under every setting that turns a protection off (the owner,
 * 2026-11-06: "Bu ve bunun gibi güvenlik kapatma ayarlarının güvenli olmadığını
 * ve önerilmediğini, ne yaptığınızı biliyorsanız lütfen uygulayın gibi bir yazı
 * ile altına açıklamalı şekilde koyalım").
 *
 * ONE sentence, here, for every such setting: not safe, not recommended, apply
 * it only if you know what you are doing. A setting brings only the second
 * half, what it exposes (`risk`). Where a setting declares itself - a driver's
 * field (`security_off`), a provider's field, an instance setting - the form
 * draws this by itself; the desktop app says the same two parts in its own
 * pages (desktop/src/tls-trust.ts).
 */
import { useI18n } from 'vue-i18n';
import { ShieldAlert } from 'lucide-vue-next';

defineProps<{ risk: string }>();

const { t } = useI18n();
</script>

<template>
  <p class="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-300" data-testid="security-off-note">
    <ShieldAlert class="mt-px h-3.5 w-3.5 shrink-0" aria-hidden="true" />
    <span><span class="font-medium">{{ t('securityOff.note') }}</span> {{ risk }}</span>
  </p>
</template>

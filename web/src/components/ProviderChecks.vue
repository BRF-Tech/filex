<script setup lang="ts">
/**
 * The last test of a sign-in provider, step by step: what was reached, what
 * was not and why, and what cannot be known without a person signing in.
 * ⚠ Shown on the card, not in a toast: the step that failed is the one the
 * operator has to read and act on. One drawing for every page that tests a
 * provider. Every step's sentence is the SERVER's (`text`, backend
 * auth/probe_say.go): the page words nothing (0.54 - the panel used to build
 * the sentences from the step ids, and an API reader got only the ids).
 */
import { useI18n } from 'vue-i18n';
import type { AuthProviderCheck, AuthProviderTestResult } from '@/api/types';
import CodeText from '@/components/ui/CodeText.vue';

defineProps<{ result: AuthProviderTestResult; testid: string }>();
const { t } = useI18n();
const text = (c: AuthProviderCheck) => c.text || `${c.id}: ${c.status}`;
</script>

<template>
  <div
    class="rounded-lg border p-3 text-sm"
    :class="result.ok
      ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-900/50 dark:bg-emerald-950/30'
      : 'border-rose-200 bg-rose-50 dark:border-rose-900/50 dark:bg-rose-950/30'"
    role="status"
    :data-testid="testid"
  >
    <p class="font-medium">
      {{ result.ok ? t('authProviders.testOk') : t('authProviders.testFail') }}
    </p>
    <ul class="mt-2 space-y-1">
      <li
        v-for="(c, i) in result.checks"
        :key="i"
        class="flex items-start gap-2"
        :data-testid="`auth-provider-check-${c.id}`"
        :data-status="c.status"
      >
        <span aria-hidden="true" class="w-4 shrink-0 font-semibold">{{ c.status === 'ok' ? '✓' : c.status === 'fail' ? '✗' : '-' }}</span>
        <span>
          <!-- A failed step the server worded (the fix, with the commands it
               marked) draws those as <code>. -->
          <CodeText v-if="c.status === 'fail' && c.params?.hint" :text="c.params.hint" />
          <template v-else>{{ text(c) }}</template>
          <span v-if="c.status === 'fail' && c.params?.detail" class="mt-0.5 block break-all font-mono text-[11px] opacity-70">
            {{ t('authProviders.technicalDetailIs', { detail: c.params.detail }) }}
          </span>
        </span>
      </li>
    </ul>
  </div>
</template>

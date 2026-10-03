<script setup lang="ts">
/**
 * A server sentence with its backtick-marked runs (commands, paths, config
 * lines) drawn as <code> — selected whole on a click, so the reader copies
 * exactly what the server wrote (lib/codeRuns).
 *
 * The code runs are left-to-right whatever the page's direction; the text runs
 * are isolated like every other server sentence on an RTL screen
 * (core lib/direction `foreignText`, the same call api/client extractError
 * makes).
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { foreignText } from '@brftech/filex-core';

import { codeRuns } from '@/lib/codeRuns';

const props = defineProps<{ text: string }>();
const { locale } = useI18n();
const runs = computed(() => codeRuns(props.text).map((r) => (r.code ? r : { ...r, text: foreignText(String(locale.value), r.text) })));
</script>

<!-- ⚠ One line on purpose: a line break between the runs would put a space
     where the server wrote none (next to a quote, before a full stop).
     A long command wraps at its spaces first and inside a word only when a
     word alone is wider than the line (overflow-wrap: anywhere, measured at
     958 px: `break-all` cut /etc/sudoers.d mid-word with room at a space). -->
<template>
  <span><template v-for="(r, i) in runs" :key="i"><code v-if="r.code" dir="ltr" class="select-all [overflow-wrap:anywhere] rounded bg-zinc-900/5 px-1 font-mono text-[0.92em] dark:bg-white/10" data-testid="code-run">{{ r.text }}</code><template v-else>{{ r.text }}</template></template></span>
</template>

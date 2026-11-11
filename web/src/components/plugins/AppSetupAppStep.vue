<script setup lang="ts">
/**
 * AppSetupAppStep — the app's OWN setup screen inside the setup wizard: a
 * view of placement `setup`, drawn by filex like every surface (the same
 * conversation, renderer and footer as a modal view - core usePluginSurface),
 * its events posted to the admin's setup route instead of a person's view
 * route. The step is done when the server says so (`app_done`): the app
 * answered `done: true` on its last screen.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { CheckCircle2 } from 'lucide-vue-next';
import {
  SurfaceConversation,
  SurfaceFooterButtons,
  usePluginSurface,
  type PluginSurface,
} from '@brftech/filex-core';

import { AppPluginsApi } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{
  pluginId: number;
  plugin: string;
  view: string;
  /** The server already holds the app's part (a wizard opened again). */
  done: boolean;
}>();

const emit = defineEmits<{ (e: 'done'): void }>();

const { t, locale } = useI18n();
const appDone = ref(props.done);
const opening = ref(!props.done);
const openError = ref('');
const toast = ref('');

/**
 * The conversation, with the admin's setup route as its server. ⚠ The answer
 * that finishes the app's part may carry a last screen; `done` is what the
 * server's `app_done` says, never a guess from the surface.
 */
const conv = usePluginSurface(
  {
    api: {
      async pluginViewEvent(_plugin, _view, body) {
        const r = await AppPluginsApi.setupView(props.pluginId, {
          event: body.event,
          action_id: body.action_id,
          state: body.state,
          data: body.data as Record<string, unknown> | undefined,
        });
        if (r.app_done) {
          const last: PluginSurface = { ...(r.surface ?? { nodes: [] }), done: true };
          return { surface: last };
        }
        // An answer with no screen reads as the conversation's failure line.
        return { surface: r.surface as PluginSurface };
      },
    },
    plugin: props.plugin,
    view: props.view,
    locale: () => locale.value,
    errorText: () => t('errors.generic'),
  },
  {
    onOp: () => {},
    onDone: () => {
      appDone.value = true;
      emit('done');
    },
    onToast: (m) => {
      toast.value = m;
    },
  },
);

const showing = computed(() => !appDone.value && !!conv.current.value);

onMounted(async () => {
  if (appDone.value) return;
  try {
    const r = await AppPluginsApi.setupView(props.pluginId);
    if (r.app_done) {
      appDone.value = true;
      emit('done');
      return;
    }
    conv.setSurface(r.surface);
  } catch (e: unknown) {
    openError.value = extractError(e, t('errors.loadFailed'));
  } finally {
    opening.value = false;
  }
});

function again() {
  appDone.value = false;
  opening.value = true;
  openError.value = '';
  void AppPluginsApi.setupView(props.pluginId)
    .then((r) => conv.setSurface(r.surface))
    .catch((e: unknown) => {
      openError.value = extractError(e, t('errors.loadFailed'));
    })
    .finally(() => {
      opening.value = false;
    });
}
</script>

<template>
  <div class="space-y-3" data-testid="app-setup-app-step">
    <div v-if="opening" class="text-center text-zinc-500"><Spinner /></div>
    <p v-else-if="openError" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ openError }}</p>
    <template v-else-if="showing">
      <div class="fe fx-setup-surface" data-testid="app-setup-surface">
        <SurfaceConversation :conv="conv" :locale="locale as any" :plugin="plugin" :toast="toast" />
        <div class="mt-3 flex flex-wrap justify-end gap-2">
          <SurfaceFooterButtons :conv="conv" :locale="locale as any" testid-prefix="app-setup-action" />
        </div>
      </div>
    </template>
    <div v-else-if="appDone" class="flex items-start gap-2 text-sm" data-testid="app-setup-app-done">
      <CheckCircle2 class="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" aria-hidden="true" />
      <div class="space-y-1">
        <p>{{ t('appPlugins.setup.appDone') }}</p>
        <button type="button" class="text-xs text-brand-600 hover:underline dark:text-brand-400" data-testid="app-setup-app-again" @click="again">
          {{ t('appPlugins.setup.appAgain') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ⚠ The wrapper carries `.fe` for the surface components' tokens and rules,
 * and with them the explorer ROOT's box: a 420px-tall column at the full
 * height of its parent. Measured 2026-11-11 in the shots of the wizard: a
 * one-question step drew as a frame two thirds empty, its text against the
 * border. The step is as tall as what the app asks. */
.fe.fx-setup-surface {
  min-height: 0;
  height: auto;
  padding: var(--fe-gap);
}
</style>

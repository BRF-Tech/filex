<script setup lang="ts">
/**
 * AppPluginInstallWizard — install (or upgrade) an app in three steps.
 *
 *   1. Source: a GitHub repository (`owner/name` + ref), the two files
 *      (module + manifest, optionally a signature), or a URL pair + SHA-256.
 *   2. Review: the same body with `?dry_run=1` — the server reads the
 *      manifest and hands back every permission with its label and the
 *      reason the manifest gives. Nothing is installed until the operator
 *      ticks "I understand".
 *   3. Install: the body again, with `permissions` = exactly the manifest's
 *      list. The server refuses anything less (`permissions_incomplete`), so
 *      the wizard never lets a subset through.
 *
 * Upgrade is the same dialog against `…/{id}/upgrade`: same bodies, same
 * review, one extra refusal (`permissions_changed`) when the new version
 * asks for more than the installed one was granted.
 *
 * ⚠ What the dry run knows is said AT THE REVIEW (release-candidate sweep,
 * 2026-09-21): an app of the same name already installed (with "upgrade it
 * instead", same source, same review), and the engines it asks for that this
 * server lacks. Both used to surface only on "Install" — or never.
 * A refused fetch is a sentence in the reader's language that says what to
 * check, not the server's English (`filex-app.json not found in … http 404
 * from raw.githubusercontent.com`), and one source tab's error does not
 * follow the operator onto another tab.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, ArrowUpFromLine, Check, ShieldCheck, Upload } from 'lucide-vue-next';

import {
  AppPluginsApi,
  appPluginError,
  type AppPlugin,
  type AppPluginDryRun,
  type AppPluginInstallSource,
} from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { pluginLabelOf, type PluginText } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Modal from '@/components/ui/Modal.vue';
import Badge from '@/components/ui/Badge.vue';
import AppPluginLanguages from './AppPluginLanguages.vue';

type Source = 'github' | 'file' | 'url';
type Step = 'source' | 'review' | 'done';

const props = defineProps<{
  modelValue: boolean;
  requiresSignature: boolean;
  /** Set → the dialog upgrades this app instead of installing a new one. */
  upgrade?: AppPlugin | null;
  /** The installed apps: what "upgrade it instead" switches to, by id. */
  installed?: AppPlugin[];
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  (e: 'installed', plugin: AppPlugin): void;
  (e: 'upgraded', plugin: AppPlugin): void;
}>();

const { t, locale } = useI18n();

const step = ref<Step>('source');
const source = ref<Source>('github');
const repo = ref('');
const gitRef = ref('');
const wasmFile = ref<File | null>(null);
const manifestFile = ref<File | null>(null);
const signature = ref('');
const url = ref('');
const manifestUrl = ref('');
const sha256 = ref('');

const review = ref<AppPluginDryRun | null>(null);
const understood = ref(false);
const busy = ref(false);
const failure = ref('');
const missing = ref<string[]>([]);
/**
 * Set when the review found the app already installed and the operator
 * chose "upgrade it instead": from then on this dialog IS that upgrade.
 */
const switchedTo = ref<AppPlugin | null>(null);

/** The app being upgraded, whichever way this dialog came to upgrade it. */
const upgradeTarget = computed<AppPlugin | null>(() => props.upgrade ?? switchedTo.value);
const isUpgrade = computed(() => !!upgradeTarget.value);
const title = computed(() =>
  isUpgrade.value
    ? t('appPlugins.wizard.upgradeTitle', { name: upgradeTarget.value?.name ?? '' })
    : t('appPlugins.wizard.title'),
);

function reset() {
  step.value = 'source';
  source.value = 'github';
  repo.value = '';
  gitRef.value = '';
  wasmFile.value = null;
  manifestFile.value = null;
  signature.value = '';
  url.value = '';
  manifestUrl.value = '';
  sha256.value = '';
  review.value = null;
  understood.value = false;
  busy.value = false;
  failure.value = '';
  missing.value = [];
  switchedTo.value = null;
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) reset();
  },
);

function close() {
  emit('update:modelValue', false);
}

// ⚠ An error belongs to the source it was about. Left in place, a bad
// repository's refusal stayed on screen over the Upload and URL tabs, as if
// they had failed too (release-candidate sweep, 2026-09-21).
watch(source, () => {
  failure.value = '';
  missing.value = [];
});

function onWasm(e: Event) {
  wasmFile.value = (e.target as HTMLInputElement).files?.[0] ?? null;
}
function onManifest(e: Event) {
  manifestFile.value = (e.target as HTMLInputElement).files?.[0] ?? null;
}

/** The body for the chosen source, or the message saying what is missing. */
function buildSource(): AppPluginInstallSource | string {
  if (source.value === 'github') {
    const r = repo.value.trim().replace(/^https?:\/\/github\.com\//i, '').replace(/\.git$/i, '').replace(/\/+$/, '');
    if (!/^[\w.-]+\/[\w.-]+$/.test(r)) return t('appPlugins.wizard.errRepo');
    return { kind: 'github', repo: r, ref: gitRef.value.trim() || undefined };
  }
  // ⚠ The module is optional in both file and URL form: a LANGUAGE PACK is
  // its manifest alone. Which one this is, the server decides from the
  // manifest (wire.Manifest.IsLanguagePack) and says so on the review —
  // deciding it here would be a second copy of that rule.
  if (source.value === 'file') {
    if (!manifestFile.value) return t('appPlugins.wizard.errFiles');
    return { kind: 'upload', wasm: wasmFile.value, manifest: manifestFile.value, signature: signature.value.trim() || undefined };
  }
  const u = url.value.trim();
  const m = manifestUrl.value.trim();
  const h = sha256.value.trim().toLowerCase();
  // A module address needs its hash; a pack's (optional) hash pins its manifest.
  if (!m || (u && !/^[0-9a-f]{64}$/.test(h)) || (h && !/^[0-9a-f]{64}$/.test(h))) return t('appPlugins.wizard.errUrl');
  return { kind: 'url', url: u || undefined, manifest_url: m, sha256: h || undefined };
}

const FETCH_REASONS = [
  'bad_repo',
  'manifest_not_found',
  'module_not_found',
  'unreachable',
  'http_status',
  'bad_url',
  'missing_url',
  'too_large',
];

/**
 * Map a refused install onto the sentence written for it, in the reader's
 * language. ⚠ `err.message` is the server's English, for the log: it is only
 * ever a parameter of `manifest_invalid` (the manifest's own complaint),
 * never the sentence itself.
 */
function explain(e: unknown): string {
  const err = appPluginError(e);
  missing.value = err?.missing ?? [];
  const known = [
    'permissions_incomplete',
    'permissions_changed',
    'sha256_mismatch',
    'sha256_required',
    'manifest_invalid',
    'signature_required',
    'signature_invalid',
    'name_taken',
    'describe_mismatch',
    'too_large',
    'demo_refused',
    'not_found',
  ];
  if (err?.code === 'fetch_failed') {
    const reason = FETCH_REASONS.includes(err.reason) ? err.reason : 'unreachable';
    return t(`appPlugins.wizard.errors.fetch.${reason}`, {
      where: err.where || '—',
      refs: err.refs.join(', ') || '—',
      status: err.status || '—',
    });
  }
  if (err && known.includes(err.code)) {
    return t(`appPlugins.wizard.errors.${err.code}`, {
      missing: err.missing.join(', ') || '—',
      message: err.message || '—',
    });
  }
  return extractError(e, t('errors.generic'));
}

/** Step 1 → 2: the dry run. */
async function toReview() {
  failure.value = '';
  const src = buildSource();
  if (typeof src === 'string') {
    failure.value = src;
    return;
  }
  busy.value = true;
  try {
    const target = upgradeTarget.value;
    review.value = target
      ? await AppPluginsApi.upgradeDryRun(target.id, src)
      : await AppPluginsApi.dryRun(src);
    understood.value = false;
    step.value = 'review';
  } catch (e: unknown) {
    failure.value = explain(e);
  } finally {
    busy.value = false;
  }
}

/**
 * The installed app the review collided with (by the id the dry run named),
 * when this dialog is still an install. Null once it has switched to
 * upgrading it.
 */
const collision = computed<AppPlugin | null>(() => {
  const inst = review.value?.installed;
  if (!inst || isUpgrade.value) return null;
  return (props.installed ?? []).find((p) => p.id === inst.id) ?? null;
});

/** "Upgrade it instead": the same source, reviewed again as an upgrade. */
async function upgradeInstead() {
  if (!collision.value) return;
  switchedTo.value = collision.value;
  await toReview();
}

/** The permissions the wizard grants: exactly the manifest's list. */
const grant = computed<string[]>(() => review.value?.manifest.permissions ?? []);

/**
 * Reason for one permission, in the reader's language: the dry run's words,
 * else the manifest's, else none.
 *
 * ⚠⚠ Both sources are the APP's words and arrive as every language at once —
 * `{"en": "…", "tr": "…"}` (wire.Text) — so both go through `pluginLabelOf`.
 * This returned the dry run's value untouched while it was typed `string`,
 * and the review printed every reason as raw JSON, Turkish and all
 * (2026-09-21; e2e/shots/apps.mjs now refuses to photograph that screen).
 */
function reasonOf(id: string, fromReview: PluginText | string | undefined): string {
  return (
    pluginLabelOf(fromReview, locale.value) ||
    pluginLabelOf(review.value?.manifest.permission_reasons?.[id], locale.value)
  );
}

// An install whose name is taken cannot go through: the review says so and
// offers the upgrade instead of letting "Install" be the one to find out.
const canInstall = computed(
  () => understood.value && !busy.value && review.value !== null && !(review.value.installed && !isUpgrade.value),
);

/** Step 2 → 3: the real thing. */
async function install() {
  if (!canInstall.value) return;
  failure.value = '';
  const src = buildSource();
  if (typeof src === 'string') {
    failure.value = src;
    return;
  }
  busy.value = true;
  try {
    const target = upgradeTarget.value;
    if (target) {
      const p = await AppPluginsApi.upgrade(target.id, src, grant.value);
      step.value = 'done';
      emit('upgraded', p);
    } else {
      const p = await AppPluginsApi.install(src, grant.value);
      step.value = 'done';
      emit('installed', p);
    }
  } catch (e: unknown) {
    failure.value = explain(e);
  } finally {
    busy.value = false;
  }
}

const manifest = computed(() => review.value?.manifest ?? null);
const manifestLabel = computed(() => pluginLabelOf(manifest.value?.label, locale.value) || manifest.value?.name || '');
const manifestDescription = computed(() => pluginLabelOf(manifest.value?.description, locale.value));
</script>

<template>
  <Modal :model-value="modelValue" :title="title" size="lg" @update:model-value="(v: boolean) => !v && close()">
    <div class="space-y-4" data-testid="app-plugin-wizard">
      <!-- Step strip -->
      <ol class="flex items-center gap-2 text-xs text-zinc-500">
        <li
          v-for="(s, i) in (['source', 'review', 'done'] as Step[])"
          :key="s"
          class="flex items-center gap-1"
          :class="step === s && 'font-semibold text-brand-600 dark:text-brand-400'"
        >
          <span class="inline-flex h-5 w-5 items-center justify-center rounded-full border text-[11px]"
            :class="step === s ? 'border-brand-600 dark:border-brand-400' : 'border-zinc-300 dark:border-zinc-700'"
          >{{ i + 1 }}</span>
          {{ t(`appPlugins.wizard.steps.${s}`) }}
        </li>
      </ol>

      <!-- 1. Source -->
      <form v-if="step === 'source'" class="space-y-4" @submit.prevent="toReview">
        <div class="flex gap-2">
          <Button
            v-for="s in (['github', 'file', 'url'] as const)"
            :key="s"
            type="button"
            size="sm"
            :variant="source === s ? 'primary' : 'outline'"
            :data-testid="`app-plugin-source-${s}`"
            @click="source = s"
          >
            {{ t(`appPlugins.wizard.source.${s}`) }}
          </Button>
        </div>

        <template v-if="source === 'github'">
          <Input v-model="repo" :label="t('appPlugins.wizard.repo')" placeholder="BRF-Tech/filex-sign" monospace data-testid="app-plugin-repo" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('appPlugins.wizard.repoHint') }}</p>
          <Input v-model="gitRef" :label="t('appPlugins.wizard.ref')" placeholder="v1.0.0" monospace />
        </template>

        <template v-else-if="source === 'file'">
          <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">{{ t('appPlugins.wizard.wasm') }}</label>
          <input
            type="file"
            accept=".wasm,application/wasm"
            class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            data-testid="app-plugin-wasm"
            @change="onWasm"
          />
          <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">{{ t('appPlugins.wizard.manifest') }}</label>
          <input
            type="file"
            accept=".json,application/json"
            class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            data-testid="app-plugin-manifest"
            @change="onManifest"
          />
          <Input v-model="signature" :label="t('appPlugins.wizard.signature')" :required="requiresSignature" monospace data-testid="app-plugin-signature" />
          <p class="-mt-2 text-xs text-zinc-500">
            {{ requiresSignature ? t('appPlugins.wizard.signatureRequired') : t('appPlugins.wizard.signatureHint') }}
          </p>
        </template>

        <template v-else>
          <Input v-model="url" :label="t('appPlugins.wizard.url')" placeholder="https://example.com/sign.wasm" monospace />
          <Input v-model="manifestUrl" :label="t('appPlugins.wizard.manifestUrl')" placeholder="https://example.com/filex-app.json" monospace />
          <Input v-model="sha256" :label="t('appPlugins.wizard.sha256')" placeholder="a1b2c3…" monospace />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('appPlugins.wizard.shaHint') }}</p>
        </template>

        <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="app-plugin-wizard-error">
          {{ failure }}
        </p>

        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="close">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="busy" data-testid="app-plugin-review">
            <ShieldCheck class="h-4 w-4" />
            {{ t('appPlugins.wizard.next') }}
          </Button>
        </div>
      </form>

      <!-- 2. Review -->
      <div v-else-if="step === 'review' && review && manifest" class="space-y-4">
        <div class="rounded-lg border border-zinc-200 p-3 text-sm dark:border-zinc-800" data-testid="app-plugin-summary">
          <div class="flex flex-wrap items-baseline gap-2">
            <span class="font-semibold">{{ manifestLabel }}</span>
            <span class="font-mono text-xs text-zinc-500">{{ manifest.name }} · v{{ manifest.version }}</span>
          </div>
          <p v-if="manifestDescription" class="mt-1 text-zinc-600 dark:text-zinc-400">{{ manifestDescription }}</p>
          <p v-if="review.kind === 'language_pack'" class="mt-1 text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-is-language-pack">
            <Badge tone="brand" size="xs">{{ t('appPlugins.kind.languagePack') }}</Badge>
            {{ t('appPlugins.wizard.languagePackNote') }}
          </p>
          <div v-if="review.languages?.length" class="mt-2">
            <h4 class="text-xs font-semibold text-zinc-600 dark:text-zinc-300">{{ t('appPlugins.lang.heading') }}</h4>
            <AppPluginLanguages :languages="review.languages" />
          </div>
          <dl class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3">
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.actions') }}</dt>
            <dd class="sm:col-span-2">{{ manifest.actions?.length ?? 0 }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.views') }}</dt>
            <dd class="sm:col-span-2">{{ manifest.views?.length ?? 0 }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.publicPages') }}</dt>
            <dd class="sm:col-span-2">{{ manifest.public_pages?.length ?? 0 }}</dd>
            <template v-if="review.kind === 'language_pack'">
              <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.manifestSha256') }}</dt>
              <dd class="break-all font-mono sm:col-span-2">{{ review.manifest_sha256 || '—' }}</dd>
            </template>
            <template v-else>
              <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.sha256') }}</dt>
              <dd class="break-all font-mono sm:col-span-2">{{ review.wasm_sha256 || '—' }}</dd>
            </template>
            <template v-if="manifest.homepage">
              <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.homepage') }}</dt>
              <dd class="break-all sm:col-span-2">{{ manifest.homepage }}</dd>
            </template>
          </dl>
        </div>

        <!-- Said at the review: the name is taken. The operator's way on is
             upgrading the installed app from this same source. -->
        <div
          v-if="review.installed && !isUpgrade"
          class="space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          role="alert"
          data-testid="app-plugin-already-installed"
        >
          <p>
            {{ t('appPlugins.wizard.alreadyInstalled', { name: manifest.name, version: review.installed.version, next: manifest.version }) }}
          </p>
          <Button
            v-if="collision"
            type="button"
            size="sm"
            variant="outline"
            :loading="busy"
            data-testid="app-plugin-upgrade-instead"
            @click="upgradeInstead"
          >
            <ArrowUpFromLine class="h-4 w-4" />
            {{ t('appPlugins.wizard.upgradeInstead', { version: manifest.version }) }}
          </Button>
        </div>

        <!-- The engines this app asks for that this server does not have: it
             installs, and whatever needs them will not work until they are. -->
        <div
          v-if="review.engines_missing?.length"
          class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          data-testid="app-plugin-engines-missing"
        >
          {{ t('appPlugins.wizard.enginesMissing', { engines: review.engines_missing.map((e) => e.name).join(', ') }) }}
        </div>

        <div>
          <h3 class="text-sm font-semibold">{{ t('appPlugins.wizard.permissions') }}</h3>
          <p v-if="!review.permissions.length" class="mt-1 text-sm text-zinc-500">{{ t('appPlugins.wizard.noPermissions') }}</p>
          <ul v-else class="rule-list mt-2 rounded-lg" data-testid="app-plugin-permissions">
            <li v-for="perm in review.permissions" :key="perm.id" class="p-3" :data-testid="`perm-${perm.id}`">
              <div class="flex flex-wrap items-center gap-2">
                <Badge tone="brand" size="xs"><span class="font-mono">{{ perm.id }}</span></Badge>
                <span class="text-sm font-medium">{{ perm.label }}</span>
              </div>
              <p class="mt-1 text-xs text-zinc-600 dark:text-zinc-400">
                {{ reasonOf(perm.id, perm.reason) || t('appPlugins.wizard.noReason') }}
              </p>
            </li>
          </ul>
        </div>

        <Checkbox v-model="understood" :label="t('appPlugins.wizard.understand')" name="app-plugin-understand" />

        <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="app-plugin-wizard-error">
          {{ failure }}
        </p>

        <div class="flex justify-between gap-2">
          <Button type="button" size="sm" variant="ghost" :disabled="busy" @click="step = 'source'">
            <ArrowLeft class="h-4 w-4" />
            {{ t('appPlugins.wizard.back') }}
          </Button>
          <Button type="button" size="sm" variant="primary" :disabled="!canInstall" :loading="busy" data-testid="app-plugin-install" @click="install">
            <Upload class="h-4 w-4" />
            {{ isUpgrade ? t('appPlugins.wizard.upgrade') : t('appPlugins.wizard.install') }}
          </Button>
        </div>
      </div>

      <!-- 3. Done -->
      <div v-else class="space-y-4" data-testid="app-plugin-done">
        <p class="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-300">
          <Check class="h-4 w-4" />
          {{ isUpgrade ? t('appPlugins.wizard.doneUpgrade') : t('appPlugins.wizard.doneInstall') }}
        </p>
        <div class="flex justify-end">
          <Button type="button" size="sm" variant="primary" @click="close">{{ t('common.close') }}</Button>
        </div>
      </div>
    </div>
  </Modal>
</template>

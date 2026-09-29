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
 * asks for more than the installed one was granted. The review of an upgrade
 * also says the version jump and how the grant changes (`review.upgrade`).
 *
 * "Review update" (the Apps list, `update` prop) is an upgrade from the
 * app's OWN source: no source step to fill — the server finds and fetches the
 * newer version exactly as the update check does (`from_source`) and the
 * dialog opens on its review.
 *
 * An app with its OWN INTERFACE (`review.ui`) gets an "Interface" group: the
 * package's hash, size and files, every address outside the package with its
 * risk colour (green: filex mirrors it and serves its own copy; amber: the
 * reader's browser fetches it live), and — always — the honest note that a
 * browser cannot fully stop an interface from sending data out (WebRTC).
 * ⚠⚠ Never "it cannot reach the network": that is not true in Firefox.
 *
 * An upgrade's review says, besides the grant: the module and interface
 * hashes from → to, which interface files were added, removed and changed,
 * the filex range and the signature from → to, and the source's release
 * notes (plain text). Nothing updates itself (filex 0.48): this review is
 * where every newer version is approved.
 *
 * ⚠ An app whose `filex` range leaves this server out is said at the review
 * (`review.compat.ok === false`) and cannot be installed from it; the server
 * refuses it too (`incompatible`).
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
import { refusalSentence } from '@/lib/appPluginRefusal';
import { formatBytes } from '@/lib/format';
import { pluginLabelOf } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Modal from '@/components/ui/Modal.vue';
import Badge from '@/components/ui/Badge.vue';
import AppPluginLanguages from './AppPluginLanguages.vue';
import AppPluginPermissionList from './AppPluginPermissionList.vue';

type Source = 'github' | 'file' | 'url';
type Step = 'source' | 'review' | 'done';

const props = defineProps<{
  modelValue: boolean;
  requiresSignature: boolean;
  /** Set → the dialog upgrades this app instead of installing a new one. */
  upgrade?: AppPlugin | null;
  /**
   * Set → the dialog upgrades this app FROM ITS OWN SOURCE to the newer
   * version the update check found, opening straight on the review.
   */
  update?: AppPlugin | null;
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
const uiFile = ref<File | null>(null);
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
const upgradeTarget = computed<AppPlugin | null>(() => props.update ?? props.upgrade ?? switchedTo.value);
const isUpgrade = computed(() => !!upgradeTarget.value);
/** Upgrading from the app's own source: there is no source to fill in. */
const fromSource = computed(() => !!props.update);
/** Where that source is, as the list row names it. */
const sourceText = computed(() => props.update?.manifest_url || props.update?.source_url || '');
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
  uiFile.value = null;
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
    if (!open) return;
    reset();
    // Nothing to fill in: straight to the review of what the source has.
    if (props.update) void toReview();
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
function onUI(e: Event) {
  uiFile.value = (e.target as HTMLInputElement).files?.[0] ?? null;
}

/** The body for the chosen source, or the message saying what is missing. */
function buildSource(): AppPluginInstallSource | string {
  if (fromSource.value) return { kind: 'update' };
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
    return {
      kind: 'upload',
      wasm: wasmFile.value,
      manifest: manifestFile.value,
      ui: uiFile.value,
      signature: signature.value.trim() || undefined,
    };
  }
  const u = url.value.trim();
  const m = manifestUrl.value.trim();
  const h = sha256.value.trim().toLowerCase();
  // A module address needs its hash; a pack's (optional) hash pins its manifest.
  if (!m || (u && !/^[0-9a-f]{64}$/.test(h)) || (h && !/^[0-9a-f]{64}$/.test(h))) return t('appPlugins.wizard.errUrl');
  return { kind: 'url', url: u || undefined, manifest_url: m, sha256: h || undefined };
}

/**
 * Map a refused install onto the sentence written for it, in the reader's
 * language — the one table the Apps list reads an update's failure from too
 * (lib/appPluginRefusal).
 */
function explain(e: unknown): string {
  const err = appPluginError(e);
  missing.value = err?.missing ?? [];
  return (err && refusalSentence(err, t)) || extractError(e, t('errors.generic'));
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

/**
 * The permissions the wizard grants: exactly the ones the review lists — the
 * manifest's, plus those filex DERIVES from what the app brings (`ui`,
 * `ui:eval`, `ui-net:…` for an interface). ⚠ Not `manifest.permissions`:
 * measured 2026-09-27 against a built server, an app with an interface was
 * refused `permissions_incomplete` (missing `ui`) because the grant was the
 * manifest's list alone.
 */
const grant = computed<string[]>(() =>
  review.value ? review.value.permissions.map((p) => p.id) : [],
);

/*
 * The permission rows (each one's label and the app's reason, in the reader's
 * language) are AppPluginPermissionList: the same list a plugin request's
 * review draws (PluginRequestsPanel).
 */

/** The review's range verdict: false when this filex is outside the app's range. */
const compatible = computed(() => review.value?.compat?.ok !== false);

// An install whose name is taken cannot go through: the review says so and
// offers the upgrade instead of letting "Install" be the one to find out. An
// app whose range leaves this filex out cannot either.
const canInstall = computed(
  () =>
    understood.value &&
    !busy.value &&
    review.value !== null &&
    compatible.value &&
    !(review.value.installed && !isUpgrade.value),
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

/** The interface's addresses outside its package, mirrored first. */
const externals = computed(() =>
  [...(review.value?.ui?.external ?? [])].sort((a, b) => Number(a.mode === 'live') - Number(b.mode === 'live')),
);

/** A size as a person reads it. */
function sizeOf(n: number | undefined): string {
  return formatBytes(n ?? 0, locale.value);
}

/** A hash short enough to compare by eye. */
function short(h: string | undefined): string {
  return h ? h.slice(0, 12) : '—';
}

/** The upgrade's module line: changed, the same, gone — or nothing (neither has one). */
const moduleLine = computed<string>(() => {
  const u = review.value?.upgrade;
  if (!u) return '';
  if (u.module_from && u.module_to) {
    return u.module_from === u.module_to
      ? t('appPlugins.wizard.diff.moduleSame')
      : t('appPlugins.wizard.diff.moduleChanged', { from: short(u.module_from), to: short(u.module_to) });
  }
  if (u.module_from && !u.module_to) return t('appPlugins.wizard.diff.moduleRemoved');
  // A module that arrives is said by `adds_module` (a pack) or the hash above.
  return '';
});

/** The upgrade's interface line. */
const uiLine = computed<string>(() => {
  const u = review.value?.upgrade;
  if (!u || (!u.ui_from && !u.ui_to)) return '';
  if (!u.ui_from) return t('appPlugins.wizard.diff.uiAdded');
  if (!u.ui_to) return t('appPlugins.wizard.diff.uiRemoved');
  if (u.ui_from === u.ui_to) return t('appPlugins.wizard.diff.uiSame');
  return t('appPlugins.wizard.diff.uiChanged', { from: short(u.ui_from), to: short(u.ui_to) });
});

/** The file lists, each with how many it left out. */
const fileGroups = computed(() => {
  const f = review.value?.upgrade?.ui_files;
  if (!f) return [];
  const groups: { key: string; names: string[]; count: number }[] = [
    { key: 'filesAdded', names: f.added ?? [], count: f.added_count },
    { key: 'filesRemoved', names: f.removed ?? [], count: f.removed_count },
    { key: 'filesChanged', names: f.changed ?? [], count: f.changed_count },
  ];
  return groups.filter((g) => g.count > 0).map((g) => ({ ...g, more: g.count - g.names.length }));
});

/** The range line, when the range changed. */
const rangeLine = computed<string>(() => {
  const u = review.value?.upgrade;
  if (!u || (u.filex_from ?? '') === (u.filex_to ?? '')) return '';
  const any = t('appPlugins.wizard.diff.anyFilex');
  return t('appPlugins.wizard.diff.filex', { from: u.filex_from || any, to: u.filex_to || any });
});

/** The signature line, when it changed. */
const signedLine = computed<string>(() => {
  const u = review.value?.upgrade;
  if (!u || !!u.signed_from === !!u.signed_to) return '';
  const word = (v: boolean | undefined) => t(v ? 'appPlugins.wizard.diff.isSigned' : 'appPlugins.wizard.diff.isUnsigned');
  return t('appPlugins.wizard.diff.signed', { from: word(u.signed_from), to: word(u.signed_to) });
});
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
        <p
          v-if="fromSource"
          class="break-words text-sm text-zinc-600 dark:text-zinc-400"
          data-testid="app-plugin-from-source"
        >
          {{ t('appPlugins.wizard.fromSource', { source: sourceText }) }}
        </p>
        <div v-if="!fromSource" class="flex gap-2">
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

        <template v-if="fromSource" />
        <template v-else-if="source === 'github'">
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
          <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">{{ t('appPlugins.wizard.ui') }}</label>
          <input
            type="file"
            accept=".zip,application/zip"
            class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            data-testid="app-plugin-ui"
            @change="onUI"
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
            <template v-else-if="review.engine !== false">
              <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.sha256') }}</dt>
              <dd class="break-all font-mono sm:col-span-2">{{ review.wasm_sha256 || '—' }}</dd>
            </template>
            <template v-if="manifest.homepage">
              <dt class="text-zinc-500">{{ t('appPlugins.wizard.facts.homepage') }}</dt>
              <dd class="break-all sm:col-span-2">{{ manifest.homepage }}</dd>
            </template>
          </dl>
        </div>

        <!-- An upgrade says the jump, and how the grant changes: what is
             being approved is what it ADDS. -->
        <div
          v-if="review.upgrade"
          class="space-y-1 rounded-lg border border-zinc-200 p-3 text-sm dark:border-zinc-800"
          data-testid="app-plugin-upgrade-diff"
        >
          <p class="font-medium" data-testid="app-plugin-upgrade-jump">
            {{ t('appPlugins.wizard.jump', { from: review.upgrade.from, to: manifest.version }) }}
          </p>
          <p v-if="review.upgrade.added?.length" class="text-amber-800 dark:text-amber-200" data-testid="app-plugin-upgrade-added">
            {{ t('appPlugins.wizard.addedPermissions', { permissions: review.upgrade.added.join(', ') }) }}
          </p>
          <p v-if="review.upgrade.removed?.length" class="text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-upgrade-removed">
            {{ t('appPlugins.wizard.removedPermissions', { permissions: review.upgrade.removed.join(', ') }) }}
          </p>
          <p v-if="review.upgrade.adds_module" class="text-amber-800 dark:text-amber-200" data-testid="app-plugin-upgrade-module">
            {{ t('appPlugins.wizard.addsModule') }}
          </p>
          <p
            v-if="!review.upgrade.added?.length && !review.upgrade.removed?.length && !review.upgrade.adds_module"
            class="text-zinc-600 dark:text-zinc-400"
          >
            {{ t('appPlugins.wizard.samePermissions') }}
          </p>
          <p v-if="moduleLine" class="font-mono text-xs text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-upgrade-module-hash">
            {{ moduleLine }}
          </p>
          <p v-if="uiLine" class="font-mono text-xs text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-upgrade-ui">
            {{ uiLine }}
          </p>
          <details v-if="fileGroups.length" class="text-xs" data-testid="app-plugin-upgrade-files">
            <summary class="cursor-pointer text-zinc-600 dark:text-zinc-400">
              {{
                t('appPlugins.wizard.diff.files', {
                  added: review.upgrade.ui_files?.added_count ?? 0,
                  removed: review.upgrade.ui_files?.removed_count ?? 0,
                  changed: review.upgrade.ui_files?.changed_count ?? 0,
                })
              }}
            </summary>
            <div v-for="g in fileGroups" :key="g.key" class="mt-1">
              <!-- One line on purpose: a line break between the spans would be condensed away. -->
              <span class="font-medium">{{ t(`appPlugins.wizard.diff.${g.key}`) }}:</span> <span class="break-all font-mono">{{ g.names.join(', ') }}</span><template v-if="g.more > 0"> <span class="text-zinc-500">{{ t('appPlugins.wizard.diff.filesMore', { count: g.more }) }}</span></template>
            </div>
          </details>
          <p v-if="rangeLine" class="text-xs text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-upgrade-range">{{ rangeLine }}</p>
          <p v-if="signedLine" class="text-xs text-amber-800 dark:text-amber-200" data-testid="app-plugin-upgrade-signed">{{ signedLine }}</p>
          <div v-if="review.upgrade.notes" class="text-xs" data-testid="app-plugin-upgrade-notes">
            <h4 class="font-semibold text-zinc-600 dark:text-zinc-300">{{ t('appPlugins.wizard.diff.notes') }}</h4>
            <!-- Plain text, as the source wrote it: never rendered as markup. -->
            <p class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words text-zinc-600 dark:text-zinc-400">{{ review.upgrade.notes }}</p>
          </div>
        </div>

        <!-- The app's own interface: what runs in every reader's browser. -->
        <div
          v-if="review.ui"
          class="space-y-2 rounded-lg border border-zinc-200 p-3 text-sm dark:border-zinc-800"
          data-testid="app-plugin-ui-group"
        >
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="text-sm font-semibold">{{ t('appPlugins.wizard.uiGroup.title') }}</h3>
            <Badge tone="brand" size="xs">{{ t('appPlugins.wizard.uiGroup.badge') }}</Badge>
          </div>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3">
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.uiGroup.sha256') }}</dt>
            <dd class="break-all font-mono sm:col-span-2" data-testid="app-plugin-ui-sha256">{{ review.ui.sha256 }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.uiGroup.files') }}</dt>
            <dd class="sm:col-span-2">{{ review.ui.files }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.wizard.uiGroup.size') }}</dt>
            <dd class="sm:col-span-2">{{ sizeOf(review.ui.bytes) }} ({{ sizeOf(review.ui.unpacked) }})</dd>
          </dl>
          <p v-if="review.engine === false" class="text-xs text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-ui-no-engine">
            {{ t('appPlugins.wizard.uiGroup.noEngine') }}
          </p>
          <div v-if="externals.length">
            <h4 class="text-xs font-semibold text-zinc-600 dark:text-zinc-300">{{ t('appPlugins.wizard.uiGroup.external') }}</h4>
            <ul class="mt-1 space-y-2" data-testid="app-plugin-ui-external">
              <li
                v-for="x in externals"
                :key="`${x.as}:${x.url}`"
                class="rounded-lg border p-2 text-xs"
                :class="
                  x.mode === 'live'
                    ? 'border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200'
                    : 'border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-900/50 dark:bg-emerald-950/30 dark:text-emerald-200'
                "
                :data-testid="`app-plugin-ui-external-${x.mode}`"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <Badge :tone="x.mode === 'live' ? 'amber' : 'emerald'" size="xs">
                    {{ x.mode === 'live' ? t('appPlugins.wizard.uiGroup.live') : t('appPlugins.wizard.uiGroup.mirror') }}
                  </Badge>
                  <span>{{ t(`appPlugins.wizard.uiGroup.as.${x.as}`) }}</span>
                  <span class="break-all font-mono">{{ x.url }}</span>
                </div>
                <p v-if="pluginLabelOf(x.reason, locale)" class="mt-1">{{ pluginLabelOf(x.reason, locale) }}</p>
                <p class="mt-1 opacity-80">
                  {{ x.mode === 'live' ? t('appPlugins.wizard.uiGroup.liveText') : t('appPlugins.wizard.uiGroup.mirrorText') }}
                </p>
              </li>
            </ul>
          </div>
          <!-- ⚠⚠ Always shown, and never "it cannot reach the network". -->
          <p
            class="rounded-lg border border-zinc-200 bg-zinc-50 p-2 text-xs text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300"
            data-testid="app-plugin-ui-honest"
          >
            {{ t('appPlugins.wizard.uiGroup.honest') }}
          </p>
        </div>

        <!-- The app's range leaves this filex out: it cannot be installed
             here, and the review says so before anybody ticks a box. -->
        <div
          v-if="review.compat && !review.compat.ok"
          class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200"
          role="alert"
          data-testid="app-plugin-incompatible"
        >
          {{ t('appPlugins.wizard.incompatible', { name: manifest.name, version: manifest.version, requires: review.compat.requires, filex: review.compat.filex }) }}
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

        <AppPluginPermissionList
          :permissions="review.permissions"
          :reasons="review.manifest.permission_reasons"
          :added="review.upgrade?.added"
        />

        <Checkbox v-model="understood" :label="t('appPlugins.wizard.understand')" name="app-plugin-understand" />

        <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="app-plugin-wizard-error">
          {{ failure }}
        </p>

        <div class="flex justify-between gap-2">
          <Button v-if="!fromSource" type="button" size="sm" variant="ghost" :disabled="busy" @click="step = 'source'">
            <ArrowLeft class="h-4 w-4" />
            {{ t('appPlugins.wizard.back') }}
          </Button>
          <span v-else />
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

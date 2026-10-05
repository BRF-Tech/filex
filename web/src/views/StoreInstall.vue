<script setup lang="ts">
/**
 * StoreInstall - where a store's install link lands
 * (`/admin/store-install#store=…&intent=…`, docs/APP-PLUGINS.md → Installing
 * from a store).
 *
 * The link's fragment was taken off the address bar before the router ran
 * (lib/storeLink.ts, router/index.ts); this page reads it once from the tab's
 * storage and asks the server about it. A second link opened while the page
 * is open (the router takes it, storeLinkArrivals) is read the same way: the
 * review of the one before is closed first (its store is told `cancelled`),
 * or - an install on its way - the new one waits until that is answered.
 * What it can show:
 *
 *   • the TRUST QUESTION - the store is not trusted yet, or its keys changed
 *     since it was: its address and every key's fingerprint, to compare with
 *     what the store publishes. Nothing was read from the link; nothing is
 *     trusted until the administrator ticks the box and presses Trust (its
 *     own POST, naming the fingerprints shown).
 *   • the install REVIEW - the same dialog every install goes through
 *     (AppPluginInstallWizard), filled in by the server from the link and
 *     marked "From store <origin>"; for a paid app, the license key. Closing
 *     it without installing tells the store the link was cancelled.
 *   • what went wrong, in a sentence (lib/storeRefusal.ts).
 *
 * A link only ever opens a review: the install is the administrator's
 * decision, exactly as for a repository they typed.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink, useRouter } from 'vue-router';
import { AlertTriangle, Check, ShieldCheck, Store } from 'lucide-vue-next';

import { AppStoreApi, TRUST_CODES, groupedFingerprint, storeRefusal, type StoreRefusal, type StoreReview } from '@/api/appStore';
import type { AppPlugin } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import { storeSentence } from '@/lib/storeRefusal';
import { isFramed, keepStoreLink, storeLinkArrivals, takeMalformedLink, takeStoreLink, type StoreLink } from '@/lib/storeLink';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Spinner from '@/components/ui/Spinner.vue';
import AppPluginInstallWizard from '@/components/plugins/AppPluginInstallWizard.vue';

type State = 'loading' | 'none' | 'malformed' | 'framed' | 'trust' | 'review' | 'done' | 'cancelled' | 'declined' | 'error';

const { t } = useI18n();
const router = useRouter();
const auth = useAuthStore();

const state = ref<State>('loading');
const link = ref<StoreLink | null>(null);
const trust = ref<StoreRefusal | null>(null);
const compared = ref(false);
const trusting = ref(false);
const review = ref<StoreReview | null>(null);
const wizardOpen = ref(false);
/** Install was pressed and the server has not answered yet (the wizard says). */
const installing = ref(false);
const installed = ref<AppPlugin | null>(null);
const failure = ref('');
/** The storeLinkArrivals count this page last read a link at. */
let readArrival = 0;
/** Reads asked so far: a newer link (or none) makes an older answer stale. */
let reads = 0;

onMounted(() => start());

/** Read the link waiting in this tab, if any. */
function start() {
  readArrival = storeLinkArrivals.value;
  reads++;
  trust.value = null;
  review.value = null;
  installed.value = null;
  failure.value = '';
  compared.value = false;
  // ⚠ The server already refuses to be framed by another site
  // (`frame-ancestors 'self'`, plus the dashboards an operator lists in
  // FILEX_FRAME_ANCESTORS), and filex's own pages frame only their apps and
  // editors (`frame-src`). The trust question and the install review are the
  // clicks a framing page would want to steer, so this page also refuses a
  // frame of any origin by itself.
  if (isFramed()) {
    // The link is dropped, not kept for later: it was opened where it
    // must not be used.
    takeStoreLink();
    takeMalformedLink();
    state.value = 'framed';
    return;
  }
  const bad = takeMalformedLink();
  link.value = takeStoreLink();
  if (!link.value) {
    state.value = bad ? 'malformed' : 'none';
    return;
  }
  void read();
}

// Another link, opened in this tab while the page is open (store fe review #2).
watch(storeLinkArrivals, () => {
  // An install on its way: the new link waits in the tab (onWizard reads it).
  if (installing.value) return;
  // The review of the link before is closed - its store is told cancelled -
  // and onWizard reads the new one.
  if (wizardOpen.value) {
    void onWizard(false);
    return;
  }
  start();
});

/** Ask the server about the link: its review, or the trust question. */
async function read() {
  if (!link.value) return;
  const seq = ++reads;
  state.value = 'loading';
  failure.value = '';
  try {
    const got = await AppStoreApi.intent(link.value.store, link.value.token);
    if (seq !== reads) {
      // A newer link took the page meanwhile: this review is closed unseen.
      void AppStoreApi.cancel(got.handle).catch(() => undefined);
      return;
    }
    review.value = got;
    state.value = 'review';
    wizardOpen.value = true;
  } catch (e: unknown) {
    if (seq !== reads) return;
    if ((e as { response?: { status?: number } })?.response?.status === 401 && (await sessionEnded())) return;
    const r = storeRefusal(e);
    if (r && TRUST_CODES.includes(r.error)) {
      trust.value = r;
      compared.value = false;
      state.value = 'trust';
      return;
    }
    failure.value = (r && storeSentence(r, t)) || extractError(e, t('errors.generic'));
    state.value = 'error';
  }
}

/**
 * A 401: is the session over? Asked of the server (auth.fetchMe, which also
 * clears what the panel still believed - otherwise the sign-in page sends a
 * "signed-in" reader straight back). Over: the link waits in this tab and the
 * sign-in comes back to it (the router brings an administrator to a waiting
 * link); the token is in no address on the way.
 */
async function sessionEnded(): Promise<boolean> {
  const held = link.value;
  if (await auth.fetchMe()) return false;
  if (held) keepStoreLink(held);
  void router?.replace({ name: 'login', query: { redirect: '/store-install' } });
  return true;
}

const changed = computed(() => trust.value?.error === 'store_key_changed');
const storeOrigin = computed(() => trust.value?.detail?.store ?? review.value?.store ?? '');

/** Trust the store with the keys shown, then read the link again. */
async function approve() {
  if (!trust.value?.detail?.store || !compared.value) return;
  trusting.value = true;
  try {
    await AppStoreApi.trust(trust.value.detail.store, trust.value.detail.fingerprints ?? []);
    await read();
  } catch (e: unknown) {
    const r = storeRefusal(e);
    if (r && TRUST_CODES.includes(r.error)) {
      // The keys moved while the page was open: ask again, with the new ones.
      trust.value = r;
      compared.value = false;
      return;
    }
    failure.value = (r && storeSentence(r, t)) || extractError(e, t('errors.generic'));
    state.value = 'error';
  } finally {
    trusting.value = false;
  }
}

function onInstalled(p: AppPlugin) {
  installed.value = p;
}

/**
 * The dialog closed: installed, or the store is told the link was cancelled.
 *
 * ⚠⚠ Never while Install is on its way (store fe review #1). The server
 * finishes the install either way; a close then told the store `cancelled`
 * and said "Nothing was installed" over an app that landed, or was upgraded,
 * a moment later (measured in all three engines). The dialog stays open
 * (AppPluginInstallWizard: no ×, Escape and the backdrop do nothing), says
 * what the server answered, and closes after it: "installed" then.
 */
async function onWizard(open: boolean) {
  if (!open && installing.value) return;
  wizardOpen.value = open;
  if (open) return;
  if (installed.value) {
    state.value = 'done';
  } else {
    const h = review.value?.handle;
    state.value = 'cancelled';
    if (h) {
      try {
        await AppStoreApi.cancel(h);
      } catch {
        /* the review is gone already: nothing to tell */
      }
    }
  }
  // A link that arrived while the review was open is read now.
  if (storeLinkArrivals.value !== readArrival) start();
}

function useLabel(use: string): string {
  return use === 'index' || use === 'license' ? t(`appStore.trust.use.${use}`) : use;
}
</script>

<template>
  <div class="space-y-5" data-testid="store-install">
    <div class="flex items-start gap-3">
      <Store class="mt-1 h-6 w-6 shrink-0 text-brand-600 dark:text-brand-400" />
      <div class="min-w-0">
        <h1 class="text-xl font-semibold">{{ t('appStore.title') }}</h1>
        <p class="text-sm text-zinc-500">{{ t('appStore.subtitle') }}</p>
      </div>
    </div>

    <div v-if="state === 'loading'" class="card card-body text-center text-zinc-500" data-testid="store-install-loading">
      <Spinner />
    </div>

    <div v-else-if="state === 'none'" class="card card-body text-sm text-zinc-600 dark:text-zinc-400" data-testid="store-install-none">
      {{ t('appStore.none') }}
    </div>

    <div v-else-if="state === 'framed'" class="card card-body text-sm text-rose-700 dark:text-rose-300" role="alert" data-testid="store-install-framed">
      {{ t('appStore.framed') }}
    </div>

    <div v-else-if="state === 'malformed'" class="card card-body text-sm text-rose-700 dark:text-rose-300" role="alert" data-testid="store-install-malformed">
      {{ t('appStore.malformed') }}
    </div>

    <!-- The trust question: nothing from the link was read yet. -->
    <section v-else-if="state === 'trust' && trust" class="card card-body space-y-4" data-testid="store-install-trust">
      <div
        class="flex gap-2 rounded-lg border p-3 text-sm"
        :class="
          changed
            ? 'border-rose-200 bg-rose-50 text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200'
            : 'border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200'
        "
        role="alert"
        :data-testid="changed ? 'store-trust-changed' : 'store-trust-new'"
      >
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
        <div class="space-y-1">
          <p class="font-medium">{{ changed ? t('appStore.trust.changedTitle') : t('appStore.trust.newTitle') }}</p>
          <p>{{ changed ? t('appStore.trust.changedBody') : t('appStore.trust.newBody') }}</p>
        </div>
      </div>
      <dl class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-4">
        <dt class="text-zinc-500">{{ t('appStore.trust.store') }}</dt>
        <dd class="break-all font-mono sm:col-span-3" data-testid="store-trust-origin">{{ storeOrigin }}</dd>
      </dl>
      <div>
        <h2 class="text-sm font-semibold">{{ t('appStore.trust.keys') }}</h2>
        <ul class="mt-2 space-y-2" data-testid="store-trust-keys">
          <li
            v-for="k in trust.detail?.keys ?? []"
            :key="k.id"
            class="rounded-lg border border-zinc-200 p-2 text-xs dark:border-zinc-800"
            data-testid="store-trust-key"
          >
            <div class="flex flex-wrap items-center gap-2">
              <Badge tone="brand" size="xs">{{ useLabel(k.use) }}</Badge>
              <span class="font-mono">{{ k.id }}</span>
              <Badge v-if="k.status && k.status !== 'active'" tone="zinc" size="xs">{{ k.status }}</Badge>
            </div>
            <p class="mt-1 break-all font-mono text-sm" :title="k.fingerprint" data-testid="store-trust-fingerprint">
              {{ groupedFingerprint(k.fingerprint) }}
            </p>
          </li>
        </ul>
      </div>
      <div v-if="changed && trust.detail?.previous_keys?.length">
        <h2 class="text-sm font-semibold">{{ t('appStore.trust.previous') }}</h2>
        <ul class="mt-2 space-y-1 text-xs text-zinc-500" data-testid="store-trust-previous">
          <li v-for="k in trust.detail.previous_keys" :key="k.id" class="break-all font-mono">
            {{ useLabel(k.use) }} · {{ k.id }} · {{ groupedFingerprint(k.fingerprint) }}
          </li>
        </ul>
      </div>
      <p class="text-xs text-zinc-600 dark:text-zinc-400">{{ t('appStore.trust.how') }}</p>
      <Checkbox v-model="compared" :label="t('appStore.trust.compared')" name="store-trust-compared" />
      <div class="flex flex-wrap justify-end gap-2">
        <Button variant="ghost" size="sm" data-testid="store-trust-decline" @click="state = 'declined'">
          {{ t('common.cancel') }}
        </Button>
        <Button
          variant="primary"
          size="sm"
          :disabled="!compared"
          :loading="trusting"
          data-testid="store-trust-approve"
          @click="approve"
        >
          <ShieldCheck class="h-4 w-4" />
          {{ t('appStore.trust.approve') }}
        </Button>
      </div>
    </section>

    <div v-else-if="state === 'review'" class="card card-body text-sm text-zinc-600 dark:text-zinc-400" data-testid="store-install-review">
      {{ t('appStore.reviewing', { app: review?.intent.app ?? '', store: review?.store ?? '' }) }}
    </div>

    <div v-else-if="state === 'done'" class="card card-body space-y-3 text-sm" data-testid="store-install-done">
      <p class="flex items-center gap-2 text-emerald-700 dark:text-emerald-300">
        <Check class="h-4 w-4" />
        {{
          t(review?.upgrade_of ? 'appStore.doneUpgrade' : 'appStore.done', {
            app: installed?.name ?? review?.intent.app ?? '',
            version: installed?.version ?? review?.intent.version ?? '',
          })
        }}
      </p>
      <RouterLink
        v-if="installed"
        :to="{ name: 'plugins.app', params: { name: installed.name } }"
        class="text-brand-600 underline dark:text-brand-400"
        data-testid="store-install-open-app"
      >
        {{ t('appStore.openApp') }}
      </RouterLink>
    </div>

    <div v-else-if="state === 'cancelled'" class="card card-body text-sm text-zinc-600 dark:text-zinc-400" data-testid="store-install-cancelled">
      {{ t('appStore.cancelled') }}
    </div>

    <div v-else-if="state === 'declined'" class="card card-body text-sm text-zinc-600 dark:text-zinc-400" data-testid="store-install-declined">
      {{ t('appStore.declined') }}
    </div>

    <div
      v-else-if="state === 'error'"
      class="card card-body rounded-lg border border-rose-200 bg-rose-50 text-sm text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200"
      role="alert"
      data-testid="store-install-error"
    >
      {{ failure }}
    </div>

    <AppPluginInstallWizard
      :model-value="wizardOpen"
      :requires-signature="false"
      :store="review"
      @update:model-value="onWizard"
      @installing="(v: boolean) => (installing = v)"
      @installed="onInstalled"
      @upgraded="onInstalled"
    />
  </div>
</template>

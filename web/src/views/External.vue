<script setup lang="ts">
/**
 * Admin → External services.
 *
 * ⚠ Read the store's header before changing anything here. The short version:
 * three machines must reach three addresses, only one of them was ever
 * checked, and the green badge that check produced was read as an answer to
 * all three. This page now reports each leg separately and never merges them.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  ExternalLink,
  Save,
  Activity,
  Check,
  X,
  HelpCircle,
  AlertTriangle,
  Info,
} from 'lucide-vue-next';

import { useExternalServicesStore } from '@/stores/external';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { ExternalAdvisory, ExternalService } from '@/api/types';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, te, locale } = useI18n();
const ext = useExternalServicesStore();
const toast = useToastStore();

interface Draft {
  url: string;
  jwt_secret: string;
  enabled: boolean;
  /** Only OnlyOffice calls back, so only its card shows this. */
  callback_url: string;
}

const drafts = reactive<Record<string, Draft>>({});
const testingId = ref<string | null>(null);
const savingId = ref<string | null>(null);

function ensureDraft(s: ExternalService): Draft {
  if (!drafts[s.id]) {
    drafts[s.id] = {
      url: s.url ?? '',
      jwt_secret: '',
      enabled: s.enabled,
      callback_url: s.callback_url ?? '',
    };
  }
  return drafts[s.id];
}

async function load() {
  await ext.fetch();
  for (const s of ext.items) ensureDraft(s);
  // ⚠ Unawaited on purpose. The operator must see the browser verdict without
  // pressing anything — that is what would have saved the reporter two rounds
  // — but a black-holed address takes up to 10 s, and the page must not wait
  // for it before painting.
  void ext.probeAllBrowser();
}

async function save(s: ExternalService) {
  const d = ensureDraft(s);
  savingId.value = s.id;
  try {
    await ext.update(s.id, {
      url: d.url || null,
      enabled: d.enabled,
      jwt_secret: d.jwt_secret || undefined,
      ...(callsBack(s) ? { callback_url: d.callback_url } : {}),
    });
    d.jwt_secret = ''; // never echo back
    toast.success(t('external.savedOk'));
    // ⚠ Re-probe BOTH legs after a save. The PATCH only stores the row and
    // stamps its state "unknown", which the list renders as "not configured" —
    // a verdict about the address the operator just typed that nothing has
    // actually measured. Stale verdicts next to a changed address are the
    // exact class of bug this page is being fixed for.
    void ext.test(s.id);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingId.value = null;
  }
}

async function test(s: ExternalService) {
  testingId.value = s.id;
  try {
    const server = await ext.test(s.id);
    const browser = ext.browserProbes[s.id];
    if (server.serverReachable && browser?.state === 'ok' && !hasWarning(s.id)) {
      toast.success(t('external.testOk'));
    } else if (server.serverReachable && browser && browser.state !== 'ok') {
      // ⚠ The sentence that would have saved two rounds: the two probes
      // disagree, and the browser is the one that opens the editor.
      toast.warn(t('external.legs.disagree'));
    } else {
      toast.warn(server.error || t('external.testFail'));
    }
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    testingId.value = null;
  }
}

// ─── verdicts ────────────────────────────────────────────────────────────────

/**
 * Services that fetch from filex and post back to it. Only OnlyOffice does, so
 * only its card gets the callback address and the third leg — showing either
 * on drawio would invent a question that service never asks.
 */
function callsBack(s: ExternalService): boolean {
  return s.id === 'onlyoffice';
}

type LegTone = 'ok' | 'bad' | 'unknown';

/** The third leg, as the last Test measured it. */
function callbackLeg(s: ExternalService): { tone: LegTone; key: string } {
  const res = ext.callbackProbes[s.id];
  if (!res) return { tone: 'unknown', key: 'external.legs.callbackUnknown' };
  if (!res.checked) return { tone: 'unknown', key: 'external.legs.callbackUnchecked' };
  return res.ok
    ? { tone: 'ok', key: 'external.legs.callbackOk' }
    : { tone: 'bad', key: 'external.legs.callbackBad' };
}

function callbackDetail(s: ExternalService): string {
  return ext.callbackProbes[s.id]?.detail ?? '';
}

function advisories(id: string): ExternalAdvisory[] {
  return ext.items.find((s) => s.id === id)?.advisories ?? [];
}
function hasWarning(id: string): boolean {
  return advisories(id).some((a) => a.severity === 'warning');
}

/**
 * The single badge. ⚠ `complete` is reserved for a configuration where BOTH
 * probes answered and nothing is warned about. "The server reached it" gets
 * its own, visibly different label — telling those two apart at a glance is
 * the whole point of this change.
 */
type Overall =
  | 'disabled'
  | 'unconfigured'
  | 'serverUnreachable'
  | 'checking'
  | 'serverOnly'
  | 'needsAttention'
  | 'serverReachable'
  | 'complete';

function overall(s: ExternalService): Overall {
  if (!s.enabled) return 'disabled';
  if (s.last_state === 'disabled') return 'disabled';
  if (s.last_state === 'unconfigured') return 'unconfigured';
  if (s.last_state === 'configured-unreachable') return 'serverUnreachable';
  if (ext.browserProbing[s.id]) return 'checking';
  const p = ext.browserProbes[s.id];
  if (p && p.state !== 'ok' && p.state !== 'skipped') return 'serverOnly';
  if (hasWarning(s.id)) return 'needsAttention';
  if (p && p.state === 'ok') return 'complete';
  return 'serverReachable';
}

const overallTone: Record<Overall, 'emerald' | 'amber' | 'rose' | 'zinc' | 'sky'> = {
  disabled: 'zinc',
  unconfigured: 'zinc',
  serverUnreachable: 'rose',
  checking: 'zinc',
  serverOnly: 'amber',
  needsAttention: 'amber',
  serverReachable: 'sky',
  complete: 'emerald',
};

function serverLeg(s: ExternalService): { key: string; tone: string } {
  switch (s.last_state) {
    case 'healthy':
      return { key: 'external.legs.reachable', tone: 'ok' };
    case 'configured-unreachable':
      return { key: 'external.legs.unreachable', tone: 'bad' };
    case 'disabled':
      return { key: 'external.legs.disabledLeg', tone: 'mute' };
    default:
      return { key: 'external.legs.notConfigured', tone: 'mute' };
  }
}

function browserLeg(s: ExternalService): { key: string; tone: string } {
  if (ext.browserProbing[s.id]) return { key: 'external.browserStates.checking', tone: 'mute' };
  const p = ext.browserProbes[s.id];
  if (!p) return { key: 'external.browserStates.notChecked', tone: 'mute' };
  switch (p.state) {
    case 'ok':
      return { key: 'external.browserStates.ok', tone: 'ok' };
    case 'wrong-content':
      return { key: 'external.browserStates.wrongContent', tone: 'bad' };
    case 'unreachable':
      return { key: 'external.browserStates.unreachable', tone: 'bad' };
    case 'timeout':
      return { key: 'external.browserStates.timeout', tone: 'bad' };
    case 'blocked-mixed-content':
      return { key: 'external.browserStates.mixedContent', tone: 'bad' };
    default:
      return { key: 'external.browserStates.notChecked', tone: 'mute' };
  }
}

function legDisagrees(s: ExternalService): boolean {
  const p = ext.browserProbes[s.id];
  return s.last_state === 'healthy' && !!p && p.state !== 'ok' && p.state !== 'skipped';
}

function legClass(tone: string): string {
  if (tone === 'ok') return 'text-emerald-600 dark:text-emerald-400';
  if (tone === 'bad') return 'text-rose-600 dark:text-rose-400';
  return 'text-zinc-500 dark:text-zinc-400';
}

/** Translate an advisory by code; fall back to the server's English message. */
function advisoryText(a: ExternalAdvisory): string {
  const key = `external.advisories.${a.code}`;
  if (te(key)) {
    return t(key, { host: a.detail ?? '', value: a.detail ?? '', publicUrl: ext.publicUrl });
  }
  return a.message;
}

const publicUrlDisplay = computed(
  () => ext.publicUrl || t('external.threeAddresses.publicUrlUnknown'),
);

/** The two commands that tell an operator which half is wrong. */
const diagnosticBrowserCmd = computed(() => {
  const base = ext.items.find((s) => s.id === 'onlyoffice')?.url || '<document-server-url>';
  return `curl -I ${base.replace(/\/+$/, '')}/web-apps/apps/api/documents/api.js`;
});
const diagnosticCallbackCmd = 'podman exec -it onlyoffice curl -I "$FILEX_PUBLIC_URL/healthz"';

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('external.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('external.subtitle') }}</p>
    </div>

    <!-- ⚠ The three-address requirement lives HERE, where the field is filled
         in — not only in a prerequisites list further down a docs page. A
         green Test outranks a prerequisites list; that is how the reporter
         hit this twice. -->
    <div
      class="card card-body space-y-2 border-l-4 border-l-sky-400 dark:border-l-sky-500"
      data-testid="three-addresses"
    >
      <h2 class="text-sm font-semibold">{{ t('external.threeAddresses.title') }}</h2>
      <ul class="text-xs text-zinc-600 dark:text-zinc-300 space-y-1 list-disc pl-4">
        <li>{{ t('external.threeAddresses.browser') }}</li>
        <li>{{ t('external.threeAddresses.server') }}</li>
        <li>
          {{ t('external.threeAddresses.callback') }}
          <code class="font-mono text-[11px] px-1 rounded bg-zinc-100 dark:bg-zinc-800">{{
            publicUrlDisplay
          }}</code>
        </li>
      </ul>
      <p class="text-xs text-zinc-500 dark:text-zinc-400">
        {{ t('external.threeAddresses.diagnose') }}
      </p>
      <pre
        class="text-[11px] font-mono whitespace-pre-wrap rounded bg-zinc-100 dark:bg-zinc-800 p-2 overflow-x-auto"
      ># {{ t('external.threeAddresses.fromWorkstation') }}
{{ diagnosticBrowserCmd }}

# {{ t('external.threeAddresses.fromDocumentServer') }}
{{ diagnosticCallbackCmd }}</pre
      >
    </div>

    <div v-if="ext.loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="space-y-3">
      <div v-for="s in ext.items" :key="s.id" class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <!-- ⚠ `capitalize` belongs to the service id alone. On the <h2> it
                 also retitled the badge and every other word inside it
                 ("Server-Reachable Only"), and in Turkish it would recase
                 words the language does not recase that way. -->
            <h2 class="text-sm font-semibold flex items-center gap-2">
              <span class="capitalize">{{ s.id }}</span>
              <Badge :tone="overallTone[overall(s)]" dot :data-testid="`overall-${s.id}`">
                {{ t(`external.overall.${overall(s)}`) }}
              </Badge>
              <Badge v-if="s.env_managed" tone="zinc">
                {{ t('external.envManaged') }}
              </Badge>
            </h2>
            <p v-if="s.env_managed" class="text-xs text-amber-600 dark:text-amber-400 mt-0.5">
              {{ t('external.envManagedHint') }}
            </p>
            <p
              v-if="
                s.last_checked_at &&
                !s.last_checked_at.startsWith('0001-01-01') &&
                !s.last_checked_at.startsWith('0000')
              "
              class="text-xs text-zinc-500 mt-0.5"
            >
              {{ formatRelative(s.last_checked_at, locale) }}
            </p>
          </div>
          <a
            v-if="ensureDraft(s).url"
            :href="ensureDraft(s).url"
            target="_blank"
            rel="noopener"
            class="text-zinc-500 hover:text-brand-600 dark:hover:text-brand-400"
          >
            <ExternalLink class="h-4 w-4" />
          </a>
        </div>

        <!-- Three legs, three sentences, never merged. -->
        <div
          class="rounded-md bg-zinc-50 dark:bg-zinc-900/50 p-2.5 space-y-1.5 text-xs"
          :data-testid="`legs-${s.id}`"
        >
          <div class="flex items-start gap-2">
            <Check
              v-if="serverLeg(s).tone === 'ok'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-emerald-500"
            />
            <X
              v-else-if="serverLeg(s).tone === 'bad'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-rose-500"
            />
            <HelpCircle v-else class="h-3.5 w-3.5 shrink-0 mt-px text-zinc-400" />
            <span>
              <span class="text-zinc-600 dark:text-zinc-300">{{ t('external.legs.server') }}</span>
              <span :class="legClass(serverLeg(s).tone)"> {{ t(serverLeg(s).key) }}</span>
            </span>
          </div>

          <div class="flex items-start gap-2">
            <Spinner v-if="ext.browserProbing[s.id]" class="h-3.5 w-3.5 shrink-0 mt-px" />
            <Check
              v-else-if="browserLeg(s).tone === 'ok'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-emerald-500"
            />
            <X
              v-else-if="browserLeg(s).tone === 'bad'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-rose-500"
            />
            <HelpCircle v-else class="h-3.5 w-3.5 shrink-0 mt-px text-zinc-400" />
            <span>
              <span class="text-zinc-600 dark:text-zinc-300">{{ t('external.legs.browser') }}</span>
              <span :class="legClass(browserLeg(s).tone)"> {{ t(browserLeg(s).key) }}</span>
              <span
                v-if="ext.browserProbes[s.id]?.url"
                class="block font-mono text-[10px] text-zinc-400 break-all"
                >{{ ext.browserProbes[s.id]?.url }}</span
              >
            </span>
          </div>

          <div v-if="callsBack(s)" class="flex items-start gap-2">
            <Check
              v-if="callbackLeg(s).tone === 'ok'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-emerald-500"
            />
            <X
              v-else-if="callbackLeg(s).tone === 'bad'"
              class="h-3.5 w-3.5 shrink-0 mt-px text-rose-500"
            />
            <HelpCircle v-else class="h-3.5 w-3.5 shrink-0 mt-px text-zinc-400" />
            <span :data-testid="`leg-callback-${s.id}`">
              <span class="text-zinc-600 dark:text-zinc-300">{{ t('external.legs.callback') }}</span>
              <span :class="legClass(callbackLeg(s).tone)"> {{ t(callbackLeg(s).key) }}</span>
              <span
                v-if="callbackDetail(s)"
                class="block font-mono text-[10px] text-zinc-400 break-all"
                >{{ callbackDetail(s) }}</span
              >
            </span>
          </div>

          <p
            v-if="legDisagrees(s)"
            class="text-amber-700 dark:text-amber-400 pt-1"
            :data-testid="`disagree-${s.id}`"
          >
            {{ t('external.legs.disagree') }}
          </p>
        </div>

        <p v-if="s.last_error" class="text-xs text-rose-600 dark:text-rose-400 font-mono">
          {{ s.last_error }}
        </p>

        <Input
          v-model="ensureDraft(s).url"
          :label="t('external.fields.url')"
          monospace
          placeholder="https://example.com"
        />
        <!-- Advisories sit next to the field they are about. Never a refusal:
             a container-internal address is correct for somebody browsing from
             the same host. -->
        <div
          v-for="a in advisories(s.id)"
          :key="a.code"
          class="flex items-start gap-2 text-xs rounded-md p-2"
          :class="
            a.severity === 'warning'
              ? 'bg-amber-50 dark:bg-amber-950/30 text-amber-800 dark:text-amber-300'
              : 'bg-sky-50 dark:bg-sky-950/30 text-sky-800 dark:text-sky-300'
          "
          :data-testid="`advisory-${s.id}-${a.code}`"
        >
          <AlertTriangle v-if="a.severity === 'warning'" class="h-3.5 w-3.5 shrink-0 mt-px" />
          <Info v-else class="h-3.5 w-3.5 shrink-0 mt-px" />
          <span>{{ advisoryText(a) }}</span>
        </div>

        <Input
          v-model="ensureDraft(s).jwt_secret"
          type="password"
          :label="t('external.fields.jwtSecret')"
          autocomplete="off"
          monospace
          :hint="s.jwt_secret_set ? t('external.fields.jwtSecretHint') : undefined"
        />
        <Input
          v-if="callsBack(s)"
          v-model="ensureDraft(s).callback_url"
          :label="t('external.fields.callbackUrl')"
          monospace
          :placeholder="publicUrlDisplay"
          :hint="t('external.fields.callbackUrlHint')"
        />

        <Toggle v-model="ensureDraft(s).enabled" :label="t('common.enabled')" />

        <div class="flex items-center justify-between gap-2 pt-1">
          <Button
            type="button"
            variant="outline"
            size="sm"
            :loading="testingId === s.id"
            @click="test(s)"
          >
            <Activity class="h-4 w-4" />
            {{ t('common.testNow') }}
          </Button>
          <Button size="sm" :loading="savingId === s.id" @click="save(s)">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

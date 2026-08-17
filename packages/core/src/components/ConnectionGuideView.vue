<script setup lang="ts">
/**
 * ConnectionGuideView — renders one `ProtocolGuide`.
 *
 * The guide itself is generated from the live deployment
 * (`lib/connectionGuides.ts`); this component only draws it. Keeping the
 * two apart is what lets S3 and SFTP arrive as a builder each rather than
 * as a second page with its own copy buttons and its own bugs.
 */
import { computed, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { GuideBlock, ProtocolGuide } from '../lib/connectionGuides';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  guide: ProtocolGuide;
  locale: LocaleCode;
}>();

const { t } = useLocale(() => props.locale);

const activeClient = ref<string>(props.guide.clients[0]?.id ?? '');
const copied = ref<string | null>(null);
let copyTimer: ReturnType<typeof setTimeout> | null = null;

const client = computed(
  () => props.guide.clients.find((c) => c.id === activeClient.value) ?? props.guide.clients[0],
);

/**
 * Copy, with a fallback.
 *
 * `navigator.clipboard` needs a secure context. The desktop app's `app://`
 * scheme is registered secure, and the web app is on https — but an embed
 * on plain http is a real deployment too, and a copy button that silently
 * does nothing there is worse than no button.
 */
async function copy(text: string, id: string) {
  let ok = false;
  try {
    await navigator.clipboard.writeText(text);
    ok = true;
  } catch {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      ok = document.execCommand('copy');
      document.body.removeChild(ta);
    } catch {
      ok = false;
    }
  }
  copied.value = ok ? id : null;
  if (copyTimer) clearTimeout(copyTimer);
  copyTimer = setTimeout(() => {
    copied.value = null;
  }, 1600);
}

function blockId(prefix: string, i: number): string {
  return `${prefix}-${i}`;
}

function isCode(b: GuideBlock): boolean {
  return b.kind === 'code';
}
</script>

<template>
  <div class="fe-guide">
    <p class="fe-guide__summary">{{ guide.summary }}</p>

    <!-- The facts. This is the part that makes the page worth generating:
         the real host, the real storage, the caller's own username. -->
    <section class="fe-guide__facts" data-testid="guide-facts">
      <div v-for="(f, i) in guide.facts" :key="f.label" class="fe-guide__fact">
        <span class="fe-guide__factlabel">{{ f.label }}</span>
        <span class="fe-guide__factvalue">
          <code :class="{ 'fe-guide__ph': f.placeholderOnly }">{{ f.value }}</code>
          <button
            v-if="!f.placeholderOnly"
            type="button"
            class="fe-guide__copy"
            :data-testid="`copy-fact-${i}`"
            @click="copy(f.value, blockId('fact', i))"
          >
            {{ copied === blockId('fact', i) ? t('conn.guide.copied') : t('conn.guide.copy') }}
          </button>
        </span>
        <span v-if="f.hint" class="fe-guide__facthint">{{ f.hint }}</span>
      </div>
    </section>

    <!-- One tab per client. A "how to connect" page that covers one OS is a
         page most readers bounce off. -->
    <nav class="fe-guide__tabs" role="tablist">
      <button
        v-for="c in guide.clients"
        :key="c.id"
        type="button"
        role="tab"
        class="fe-guide__tab"
        :class="{ 'is-active': client && c.id === client.id }"
        :aria-selected="client && c.id === client.id"
        :data-testid="`guide-tab-${c.id}`"
        @click="activeClient = c.id"
      >
        {{ c.name }}
      </button>
    </nav>

    <div v-if="client" class="fe-guide__body" role="tabpanel">
      <template v-for="(b, i) in client.blocks" :key="i">
        <ol v-if="b.kind === 'steps'" class="fe-guide__steps">
          <li v-for="(s, j) in b.steps ?? []" :key="j">{{ s }}</li>
        </ol>
        <div v-else-if="isCode(b)" class="fe-guide__codewrap">
          <div class="fe-guide__codehead">
            <span class="fe-guide__caption">{{ b.caption }}</span>
            <button
              type="button"
              class="fe-guide__copy"
              :data-testid="`copy-code-${i}`"
              @click="copy(b.code ?? '', blockId(client.id, i))"
            >
              {{ copied === blockId(client.id, i) ? t('conn.guide.copied') : t('conn.guide.copy') }}
            </button>
          </div>
          <pre class="fe-guide__code"><code>{{ b.code }}</code></pre>
        </div>
        <p v-else-if="b.kind === 'warn'" class="fe-guide__warn">{{ b.text }}</p>
        <p v-else-if="b.kind === 'note'" class="fe-guide__note">{{ b.text }}</p>
        <p v-else class="fe-guide__text">{{ b.text }}</p>
      </template>
    </div>

    <section v-if="guide.notes.length" class="fe-guide__notes">
      <h4 class="fe-guide__notestitle">{{ t('conn.guide.goodToKnow') }}</h4>
      <p
        v-for="(n, i) in guide.notes"
        :key="i"
        :class="n.kind === 'warn' ? 'fe-guide__warn' : 'fe-guide__note'"
      >
        {{ n.text }}
      </p>
    </section>
  </div>
</template>

<style>
.fe-guide {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.fe-guide__summary {
  margin: 0;
  color: var(--fe-text-muted);
  font-size: 13.5px;
  line-height: 1.55;
}
.fe-guide__facts {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  background: var(--fe-bg-elev);
  padding: 12px 14px;
}
.fe-guide__fact {
  display: grid;
  grid-template-columns: minmax(120px, 180px) 1fr;
  gap: 4px 14px;
  align-items: baseline;
}
.fe-guide__factlabel {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--fe-text-muted);
}
.fe-guide__factvalue {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-wrap: wrap;
}
.fe-guide__factvalue code {
  font-family: var(--fe-font-mono);
  font-size: 12.5px;
  color: var(--fe-text);
  background: var(--fe-bg);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-sm);
  padding: 2px 6px;
  overflow-wrap: anywhere;
}
.fe-guide__ph {
  color: var(--fe-text-muted) !important;
  font-style: italic;
}
.fe-guide__facthint {
  grid-column: 2;
  font-size: 12px;
  color: var(--fe-text-muted);
}
.fe-guide__tabs {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
  border-bottom: 1px solid var(--fe-border);
  padding-bottom: 2px;
}
.fe-guide__tab {
  font: inherit;
  font-size: 13px;
  border: 0;
  background: none;
  color: var(--fe-text-muted);
  padding: 6px 10px;
  border-radius: var(--fe-radius-sm) var(--fe-radius-sm) 0 0;
  cursor: pointer;
  border-bottom: 2px solid transparent;
}
.fe-guide__tab:hover {
  color: var(--fe-text);
  background: var(--fe-bg-hover);
}
.fe-guide__tab.is-active {
  color: var(--fe-primary);
  border-bottom-color: var(--fe-primary);
  font-weight: 600;
}
.fe-guide__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.fe-guide__steps {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13.5px;
  line-height: 1.55;
  color: var(--fe-text);
}
.fe-guide__codewrap {
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius);
  overflow: hidden;
  background: var(--fe-bg-elev);
}
.fe-guide__codehead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--fe-border);
}
.fe-guide__caption {
  font-size: 12px;
  color: var(--fe-text-muted);
  font-family: var(--fe-font-mono);
  overflow-wrap: anywhere;
}
.fe-guide__code {
  margin: 0;
  padding: 10px 12px;
  /* Wide command lines scroll INSIDE the block. Without this the whole
     settings surface grows a horizontal scrollbar and the layout around
     it breaks — the same failure the tab strip had. */
  overflow-x: auto;
  font-family: var(--fe-font-mono);
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--fe-text);
  white-space: pre;
}
.fe-guide__copy {
  font: inherit;
  font-size: 12px;
  border: 1px solid var(--fe-border-strong);
  background: var(--fe-bg);
  color: var(--fe-text);
  border-radius: var(--fe-radius-sm);
  padding: 3px 9px;
  cursor: pointer;
  flex: 0 0 auto;
}
.fe-guide__copy:hover {
  border-color: var(--fe-primary);
  color: var(--fe-primary);
}
.fe-guide__note,
.fe-guide__warn,
.fe-guide__text {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
}
.fe-guide__text {
  color: var(--fe-text);
}
.fe-guide__note {
  color: var(--fe-text-muted);
}
.fe-guide__warn {
  color: var(--fe-danger);
}
.fe-guide__notes {
  display: flex;
  flex-direction: column;
  gap: 6px;
  border-top: 1px solid var(--fe-border);
  padding-top: 12px;
}
.fe-guide__notestitle {
  margin: 0;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--fe-text-muted);
}
</style>

<script setup lang="ts">
/**
 * CodeEditor - a box of source code to edit: filex's Monaco editor (the one
 * the file preview edits code in, useMonacoLoader), with line numbers,
 * syntax colours for `language` and, when the host knows one, an error mark
 * on a line (squiggle, gutter mark, the message on hover; the line is
 * scrolled into view).
 *
 * ⚠ Monaco is an optional peer of this package: a host that does not ship it
 * gets a plain textarea with the same value, events and test id, never an
 * empty box or an "editor loading..." that never ends (PreviewModal learned
 * that the hard way).
 *
 * ⚠ The options are the file preview's (`monacoEditorOptions`): a code box
 * looks the same wherever filex draws one.
 */
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ensureMonaco, monacoEditorOptions } from '../composables/useMonacoLoader';

const props = withDefaults(
  defineProps<{
    modelValue: string;
    /** A Monaco language id (`liquid`, `html`, `css`...); `plaintext` for none. */
    language?: string;
    /** The host's paint. Absent: the system's preference. */
    theme?: 'light' | 'dark';
    readOnly?: boolean;
    /** The line (1-based) an error was found on, and what it says. */
    errorLine?: number | null;
    errorMessage?: string;
    ariaLabel?: string;
    /** The box's height in px (the editor scrolls inside it). */
    height?: number;
    testid?: string;
  }>(),
  { language: 'plaintext', readOnly: false, errorLine: null, errorMessage: '', ariaLabel: '', height: 420, testid: '' },
);
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();

const box = ref<HTMLDivElement | null>(null);
/** Monaco is not in this build: the textarea stands in. */
const plain = ref(false);
/** Monaco mounted (a test reads `data-editor="monaco"`). */
const ready = ref(false);

// eslint-disable-next-line @typescript-eslint/no-explicit-any
let monaco: any = null;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
let editor: any = null;
let glyphs: string[] = [];
let applying = false;

function dark(): boolean {
  if (props.theme) return props.theme === 'dark';
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches;
}

function mark(): void {
  if (!editor || !monaco) return;
  const model = editor.getModel();
  if (!model) return;
  const line = props.errorLine && props.errorLine > 0 && props.errorLine <= model.getLineCount() ? props.errorLine : 0;
  monaco.editor.setModelMarkers(
    model,
    'filex',
    line
      ? [{
          startLineNumber: line,
          endLineNumber: line,
          startColumn: 1,
          endColumn: model.getLineMaxColumn(line),
          message: props.errorMessage || '',
          severity: monaco.MarkerSeverity.Error,
        }]
      : [],
  );
  glyphs = editor.deltaDecorations(
    glyphs,
    line
      ? [{ range: new monaco.Range(line, 1, line, 1), options: { isWholeLine: true, className: 'fe-code-editor__errline', glyphMarginClassName: 'fe-code-editor__errglyph' } }]
      : [],
  );
  if (line) editor.revealLineInCenterIfOutsideViewport(line);
}

onMounted(async () => {
  monaco = await ensureMonaco();
  if (!monaco || !box.value) {
    plain.value = true;
    return;
  }
  editor = monaco.editor.create(
    box.value,
    monacoEditorOptions(
      { value: props.modelValue, language: props.language, readOnly: props.readOnly, dark: dark() },
      { glyphMargin: true, scrollBeyondLastLine: false, wordWrap: 'off', tabSize: 2, ariaLabel: props.ariaLabel },
    ),
  );
  editor.onDidChangeModelContent(() => {
    if (applying) return;
    emit('update:modelValue', editor.getValue());
  });
  ready.value = true;
  await nextTick();
  mark();
});

watch(
  () => props.modelValue,
  (v) => {
    if (!editor || v === editor.getValue()) return;
    // A value from outside (a reset, another template opened): the editor
    // takes it as one edit, so Undo brings the text back.
    applying = true;
    const model = editor.getModel();
    editor.pushUndoStop();
    editor.executeEdits('filex', [{ range: model.getFullModelRange(), text: v }]);
    editor.pushUndoStop();
    applying = false;
    mark();
  },
);
watch(() => props.language, (l) => {
  if (editor && monaco) monaco.editor.setModelLanguage(editor.getModel(), l || 'plaintext');
});
watch(() => props.theme, () => {
  if (monaco) monaco.editor.setTheme(dark() ? 'vs-dark' : 'vs');
});
watch(() => props.readOnly, (ro) => editor?.updateOptions({ readOnly: ro }));
watch(() => [props.errorLine, props.errorMessage], () => mark());

const plainEl = ref<HTMLTextAreaElement | null>(null);

/** Put text where the cursor is (replacing a selection), and keep the focus there. */
function insert(text: string): void {
  if (editor) {
    const sel = editor.getSelection();
    editor.executeEdits('filex', [{ range: sel, text, forceMoveMarkers: true }]);
    editor.focus();
    return;
  }
  const el = plainEl.value;
  const v = props.modelValue;
  if (!el) {
    emit('update:modelValue', v + text);
    return;
  }
  const { selectionStart: a, selectionEnd: b } = el;
  emit('update:modelValue', v.slice(0, a) + text + v.slice(b));
  requestAnimationFrame(() => {
    el.focus();
    el.selectionStart = el.selectionEnd = a + text.length;
  });
}

defineExpose({ insert });

onBeforeUnmount(() => {
  editor?.dispose();
  editor = null;
});
</script>

<template>
  <div
    class="fe-code-editor"
    :data-testid="testid || undefined"
    :data-editor="ready ? 'monaco' : plain ? 'textarea' : 'loading'"
    :data-error-line="errorLine || undefined"
  >
    <textarea
      v-if="plain"
      ref="plainEl"
      class="fe-code-editor__plain"
      :value="modelValue"
      :readonly="readOnly"
      :style="{ height: `${height}px` }"
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      wrap="off"
      dir="ltr"
      :aria-label="ariaLabel || undefined"
      @input="(e) => emit('update:modelValue', (e.target as HTMLTextAreaElement).value)"
    />
    <div v-else ref="box" class="fe-code-editor__box" dir="ltr" :style="{ height: `${height}px` }" />
  </div>
</template>

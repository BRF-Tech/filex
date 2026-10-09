<script setup lang="ts">
/**
 * The panel's one pill.
 *
 * ⚠ In a table cell a pill can be narrower than its words (a column at its
 * minimum on a phone, a person who dragged the edge in). It used to keep its
 * border at the cell's width and let the words run out through it - "Bekliyor"
 * over the edge of its own pill at 390px, and the dot squeezed to nothing. So
 * it carries the table's pill classes (`tbl-pill`, core `styles/base.css`):
 * every run of TEXT in it is a `tbl-pill__text`, which a cell cuts with an
 * ellipsis inside the border, the dot and any icon keep their size, and the
 * table (DataTable) gives the whole text as the pill's title while it is cut.
 * Outside a cell none of that applies and the pill draws exactly as before.
 */
import { Text, computed, h, useSlots, type FunctionalComponent, type Slots, type VNode } from 'vue';

interface Props {
  tone?: 'brand' | 'emerald' | 'rose' | 'amber' | 'sky' | 'zinc' | 'violet';
  size?: 'xs' | 'sm' | 'md';
  dot?: boolean;
  pill?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  tone: 'zinc',
  size: 'sm',
  pill: true,
});

const toneClass = computed(() => {
  return {
    brand: 'bg-brand-50 text-brand-700 ring-brand-600/20 dark:bg-brand-500/10 dark:text-brand-300 dark:ring-brand-500/30',
    emerald: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
    rose: 'bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-500/30',
    amber: 'bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
    sky: 'bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-500/30',
    zinc: 'bg-zinc-100 text-zinc-700 ring-zinc-300 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700',
    violet: 'bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-500/30',
  }[props.tone];
});

const dotColor = computed(
  () =>
    ({
      brand: 'bg-brand-500',
      emerald: 'bg-emerald-500',
      rose: 'bg-rose-500',
      amber: 'bg-amber-500',
      sky: 'bg-sky-500',
      zinc: 'bg-zinc-400',
      violet: 'bg-violet-500',
    })[props.tone],
);

const sizeClass = computed(() => {
  switch (props.size) {
    case 'xs':
      return 'text-[10px] px-1.5 py-0.5 gap-1';
    case 'md':
      return 'text-sm px-2.5 py-1 gap-1.5';
    default:
      return 'text-xs px-2 py-0.5 gap-1.5';
  }
});

// ⚠ `Slots`, written out - the same loop DataTable notes beside its own
// `useSlots()` (a template that reads what reads the slots).
const slots: Slots = useSlots();

/** The slot's nodes, as given. */
function parts(): VNode[] {
  return slots.default?.() ?? [];
}

/**
 * One node of the slot: a TEXT node is the pill's words and gets a box of its
 * own, anything else is drawn as it came.
 *
 * ⚠ Only the text is wrapped, not the whole slot: a pill that puts an icon
 * before its words (`<Lock /> {{ ... }}`) keeps the icon as a flex item of its
 * own, with the gap, instead of an icon and words run together in one line
 * box. `text-overflow` only cuts the text of a block box, which is why the
 * words need a box of their own at all. A text node of white space alone is
 * dropped, as the flex row dropped it before: a box for it would add a gap.
 */
const Part: FunctionalComponent<{ node: VNode }> = (p) => {
  const n = p.node;
  if (n.type !== Text) return n;
  const words = typeof n.children === 'string' ? n.children : '';
  return words.trim() === '' ? null : h('span', { class: 'tbl-pill__text' }, words);
};
Part.props = ['node'];
</script>

<template>
  <span
    class="tbl-pill inline-flex items-center font-medium ring-1 ring-inset whitespace-nowrap"
    :class="[toneClass, sizeClass, pill ? 'rounded-full' : 'rounded-md']"
  >
    <span v-if="dot" class="tbl-pill__dot h-1.5 w-1.5 shrink-0 rounded-full" :class="dotColor" />
    <Part v-for="(n, i) in parts()" :key="i" :node="n" />
  </span>
</template>

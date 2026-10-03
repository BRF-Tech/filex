/**
 * The tools of Admin → Tools, in the order their tabs appear.
 *
 * A tool is maintenance that is not an action on a file: the context menu is
 * for those, and stays that way. Adding one is a row here, a component, and
 * `tools.tabs.<id>` in both catalogues; the page (views/Tools.vue) draws the
 * tabs and keeps the open one in the address (`?tab=<id>`).
 */
import { defineAsyncComponent, type Component } from 'vue';

export interface ToolTab {
  /** Stable id: the `?tab=` value, the test hook, the catalogue key. */
  id: string;
  component: Component;
}

export const TOOLS: readonly ToolTab[] = [
  { id: 'thumbnails', component: defineAsyncComponent(() => import('./ThumbnailRepairTab.vue')) },
];

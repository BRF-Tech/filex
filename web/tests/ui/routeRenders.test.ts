/**
 * A route change must actually render something.
 *
 * ⚠⚠ Written after the whole application went blank, silently.
 *
 * `App.vue` wrapped its `<RouterView>` in `<transition name="fade"
 * mode="out-in">`. `out-in` serialises a route change: the entering component
 * mounts only once the leaving one's leave transition RESOLVES. When that
 * resolution never arrived, the outlet rendered **nothing** — and stayed that
 * way for every navigation afterwards, because the transition never released.
 *
 * Measured 2026-09-13 in a real browser: signing in left `#app` holding only
 * the toast container and the install banner. The URL was right, the route
 * matched, its component resolved, and the console carried **no warning and no
 * error**. Nothing in the test suite noticed, because every component test
 * mounts its component directly and never goes through the outlet.
 *
 * So this pins the outlet itself rather than any one screen: after a
 * client-side navigation, the router's outlet has rendered a view.
 */
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { defineComponent, h, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { createRouter, createWebHistory } from 'vue-router';

const APP = path.resolve(__dirname, '../../src/App.vue');

describe('the router outlet', () => {
  it('does not serialise route changes behind a leave transition', () => {
    const src = readFileSync(APP, 'utf8');
    // The transition itself is fine and is worth keeping — it is `out-in`
    // that turns a stuck leave into a blank application. If you are bringing
    // it back, bring back a measurement with it: sign in, then navigate again,
    // and check `#app` has a view in it.
    expect(
      /<transition[^>]*mode\s*=\s*["']out-in["']/.test(src),
      'App.vue wraps <RouterView> in a transition with mode="out-in" — a leave ' +
        'that never resolves then renders nothing, for every route, with no error',
    ).toBe(false);
  });

  it('renders the entering view after a navigation, with a fade in place', async () => {
    const First = defineComponent({ name: 'First', render: () => h('div', { class: 'first' }, 'one') });
    const Second = defineComponent({ name: 'Second', render: () => h('div', { class: 'second' }, 'two') });

    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/', name: 'first', component: First },
        { path: '/second', name: 'second', component: Second },
      ],
    });

    // The shape App.vue uses, minus `mode`.
    const Host = defineComponent({
      template: `
        <router-view v-slot="{ Component, route }">
          <transition name="fade">
            <component :is="Component" :key="route.path" />
          </transition>
        </router-view>`,
    });

    router.push('/');
    await router.isReady();
    const w = mount(Host, { global: { plugins: [router] } });
    expect(w.find('.first').exists(), 'the first view renders').toBe(true);

    await router.push('/second');
    await nextTick();
    await nextTick();

    expect(
      w.find('.second').exists(),
      'after navigating, the outlet has rendered the entering view — this is ' +
        'the assertion that was missing when the app went blank',
    ).toBe(true);
  });
});

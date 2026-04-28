import 'vue-router';

declare module 'vue-router' {
  interface RouteMeta {
    /** If true, the route does not require authentication. */
    public?: boolean;
    /** Layout hint — currently 'blank' to skip the AdminLayout wrapper. */
    layout?: 'admin' | 'blank';
    /** i18n key for the breadcrumb segment. */
    breadcrumb?: string;
    /** Name of the parent route, used by Breadcrumbs.vue. */
    parent?: string;
  }
}

export {};

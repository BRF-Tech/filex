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
    /**
     * An admin.* permission (backend internal/perm) that opens this admin
     * page to an account without the administrator role — a delegated
     * administrator. Absent, the page is the role's alone.
     */
    adminPerm?: string;
  }
}

export {};

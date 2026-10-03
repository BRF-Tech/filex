<script setup lang="ts">
/**
 * My tenant: a tenant's administrator runs their own tenant
 * (docs/TENANT-ADMIN.md) - its own OIDC and LDAP, the providers the platform
 * operator bound to it, its own domains. The platform operator reaches the
 * same thing on a tenant's page under Admin → Tenants.
 */
import { useI18n } from 'vue-i18n';
import { Building2 } from 'lucide-vue-next';
import TenantSelfService from '@/components/TenantSelfService.vue';

const { t } = useI18n();
</script>

<template>
  <div class="space-y-5 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold flex items-center gap-2"><Building2 class="h-5 w-5" /> {{ t('tenantSelf.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('tenantSelf.subtitle') }}</p>
    </div>
    <TenantSelfService />
  </div>
</template>

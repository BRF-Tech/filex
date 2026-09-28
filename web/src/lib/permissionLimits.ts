import type { PermRuleSettings } from '@/api/roles';
import { formatBytes } from '@/lib/format';

type Translate = (key: string) => string;

/**
 * The limits a permission rule sets (backend internal/perm → settings), as
 * one short phrase each, for a rule's summary and an account's "limits from
 * rules" list alike.
 */
export function permissionLimitLines(s: PermRuleSettings | undefined, t: Translate, locale: string): string[] {
  const out: string[] = [];
  if (!s) return out;
  if (s.share_link_max_days) out.push(`${t('permissions.rules.maxDays')}: ${s.share_link_max_days}`);
  if (s.share_link_password_required) out.push(t('permissions.rules.passwordRequired'));
  if (s.blocked_extensions?.length) out.push(`${t('permissions.rules.blockedExt')}: ${s.blocked_extensions.join(', ')}`);
  if (s.max_upload_bytes) out.push(`${t('permissions.rules.maxUpload')}: ${formatBytes(s.max_upload_bytes, locale)}`);
  if (s.require_2fa) out.push(t('permissions.rules.require2fa'));
  return out;
}

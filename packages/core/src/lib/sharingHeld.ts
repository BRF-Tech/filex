/**
 * Which kinds of sharing the account may use on one item — the three sharing
 * permissions of filex `internal/perm`, each answered where it applies.
 *
 * ⚠ One answer for every door that makes a link or a grant. "+ New → Request
 * files", the share dialog's link and file-request sections, the details
 * panel's "Create link" and the Share action itself all read it, so a role
 * without `share.upload_links` is not offered a file request in one place and
 * refused it by the server in another (0.49.0: the New menu still offered it).
 *
 *   - `links`       — `share.links`: a public download link;
 *   - `uploadLinks` — `share.upload_links`: a file request (a drop link, folders only);
 *   - `users`       — `share.users`: giving people with accounts access.
 *
 * `held` is the explorer's own question (`permHeldAt`), so a permission that
 * differs from folder to folder is answered for THIS item's folder. When the
 * account's permissions are not known the explorer answers yes to all three,
 * and the server decides, as it always did.
 */
export interface SharingHeld {
  links: boolean;
  uploadLinks: boolean;
  users: boolean;
}

/** The permission key behind each kind (filex internal/perm). */
export const SHARING_PERMS = {
  links: 'share.links',
  uploadLinks: 'share.upload_links',
  users: 'share.users',
} as const;

/** Nothing narrowed: what a caller that passes no answer gets. */
export const ALL_SHARING: SharingHeld = Object.freeze({ links: true, uploadLinks: true, users: true });

/** The three answers, from the explorer's permission question. */
export function sharingHeld(held: (perm: string) => boolean): SharingHeld {
  return {
    links: held(SHARING_PERMS.links),
    uploadLinks: held(SHARING_PERMS.uploadLinks),
    users: held(SHARING_PERMS.users),
  };
}

/**
 * Is there anything to share on this item — is the Share action worth
 * offering? A file cannot take a file request, so `share.upload_links` alone
 * opens nothing on a file.
 */
export function canShareAny(s: SharingHeld, isDir: boolean): boolean {
  return s.links || s.users || (isDir && s.uploadLinks);
}

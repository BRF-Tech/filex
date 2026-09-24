type T = (key: string) => string;
type TE = (key: string) => boolean;

/**
 * A storage driver by NAME ("Yerel dosya sistemi", "S3 / Hetzner / MinIO"),
 * the names the storage form offers — for a place that cannot draw
 * StorageTags (an <option>, a sortable column value).
 *
 * ⚠ The admin pages printed the driver id ("local", "local" in a select as
 * "depo (local)") in every language (release-candidate sweep, 2026-09-21). A
 * driver this build has no name for (an external plugin's) is its id.
 */
export function driverName(driver: string, t: T, te: TE): string {
  const k = `storages.driver.${driver}`;
  return te(k) ? t(k) : driver;
}

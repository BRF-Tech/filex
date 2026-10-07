/**
 * #162 - whether the navigation panel draws its "Apps → App store" row: ONE
 * rule, for every host that mounts the explorer (the web SPA's Explore page,
 * the desktop app's window, any embed), so no host writes a second one.
 *
 * The row is drawn when all three hold:
 *   - the HOST has the store screen as a page of its own and handles
 *     `@open-app-store` (`config.appStorePage`) - the explorer cannot give a
 *     host a page, only announce that one was asked for;
 *   - the caller is a PERSON: an app token (an embed's integration) asks for
 *     no app;
 *   - the SERVER shows this person the screen (`GET /api/app-store` →
 *     `visible`): the administrator's setting for their tenant, their role and
 *     groups - decided there, never guessed here. No answer (an older server,
 *     a refusal, the network) is no.
 */

export interface AppStoreStatus {
  visible: boolean;
}

/** Whether the server is worth asking at all (host has the page, a person). */
export function appStoreAsks(hostHasPage: boolean, callerIsApp: boolean): boolean {
  return hostHasPage && !callerIsApp;
}

/** Whether the row is drawn, given the server's answer (null: none). */
export function appStoreRowShown(hostHasPage: boolean, callerIsApp: boolean, status: AppStoreStatus | null | undefined): boolean {
  return appStoreAsks(hostHasPage, callerIsApp) && status?.visible === true;
}

/**
 * jobOpen - where a finished app job sends the person who queued it.
 *
 * ⚠⚠ filex #78. A job's result may name a screen on one of the files the job
 * produced (`surface.open` on `ActionRunOutput`; the server resolves it to
 * the output's real, adapter-qualified path and puts it on the ops row as
 * `open`). The case that found it was the signing app's "Convert to PDF"
 * (filex-sign 0.2; the app signs PDFs only since 0.3): the conversion landed
 * a PDF, and the wizard had to go on ON THE PDF. Before it, the page said
 * "the job is queued" for good and the person had to find the PDF and ask
 * for signatures a second time - a wizard that "does not work".
 *
 * ONE answer for every frame that follows a job: the full page that queued
 * it (PluginPageView) and the explorer whose dialog queued it (FileExplorer's
 * own registered jobs). Two copies of "is this row telling me to go
 * somewhere?" is how one frame comes to follow a failed job's mark and the
 * other not.
 *
 * ⚠ Only a FINISHED app job, only with a request the client can act on, and
 * never over a screen the person opened since (`busy`): a job that ends while
 * somebody is in another dialog does not get to take that dialog away.
 */
import type { PendingOp } from '../composables/usePendingOps';
import type { SurfaceOpenRequest } from '../types/Plugins';
import { isOpenRequest } from './surfaceOpen';

export interface JobOpen {
  /** The app that ran the job - the screen named is one of ITS own. */
  plugin: string;
  open: SurfaceOpenRequest;
}

export function jobOpenOf(
  op: Pick<PendingOp, 'op_type' | 'status' | 'plugin' | 'open'>,
  opts: { busy?: boolean } = {},
): JobOpen | null {
  if (opts.busy) return null;
  if (op.op_type !== 'plugin' || op.status !== 'done') return null;
  if (!op.plugin || !isOpenRequest(op.open)) return null;
  return { plugin: op.plugin, open: op.open };
}

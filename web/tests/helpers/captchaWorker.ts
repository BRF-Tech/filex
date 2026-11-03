// The captcha fallback's worker, run in the test's own thread (#294).
//
// happy-dom has no Worker. This runs the SHIPPED worker text
// (CAPTCHA_WORKER_SOURCE, exactly what the `blob:` worker runs in a browser)
// with a stand-in `self` that carries the messages, so a test measures the
// real scan rather than a mock of it.
import { CAPTCHA_WORKER_SOURCE, type CaptchaWorkerLike } from '@brftech/filex-core/src/lib/captcha';

export function inThreadWorker(): CaptchaWorkerLike {
  const self: { onmessage: ((ev: { data: unknown }) => void) | null; postMessage: (m: unknown) => void } = {
    onmessage: null,
    postMessage: () => {},
  };
  new Function('self', CAPTCHA_WORKER_SOURCE)(self);
  const worker: CaptchaWorkerLike = {
    onmessage: null,
    onerror: null,
    postMessage(msg: unknown) {
      self.postMessage = (m) => worker.onmessage?.({ data: m });
      queueMicrotask(() => self.onmessage?.({ data: msg }));
    },
    terminate() {
      self.postMessage = () => {};
    },
  };
  return worker;
}

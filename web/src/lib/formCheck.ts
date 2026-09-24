import { t } from '@/i18n';

/**
 * The sentence for a form field the browser's own check refused, in the
 * PANEL's language.
 *
 * ⚠⚠ Why (release-candidate sweep, 2026-09-22, QA #38): every admin form whose
 * boxes carry `required` / `type="url"` / `min` was checked by the browser,
 * which showed its own bubble — "Please fill out this field." — in the
 * BROWSER's language, over a Turkish panel (Add storage, Edit user, a replica
 * target, …). The shared field components (ui/Input, ui/Textarea) now take
 * the browser's verdict (the `invalid` event: the form is still not sent) and
 * say it under the box themselves; the bubble is suppressed. One definition,
 * so every form in the panel refuses the same way.
 */
export function validityMessage(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement): string {
  const v = el.validity;
  if (v.valueMissing) return t('forms.required');
  if (v.typeMismatch) {
    if (el.type === 'email') return t('forms.email');
    if (el.type === 'url') return t('forms.url');
  }
  if (v.badInput) return t('forms.number');
  if (v.rangeUnderflow) return t('forms.min', { min: (el as HTMLInputElement).min });
  if (v.rangeOverflow) return t('forms.max', { max: (el as HTMLInputElement).max });
  if (v.stepMismatch) return t('forms.step');
  if (v.tooShort) {
    const n = (el as HTMLInputElement).minLength;
    return t('forms.minLength', { n }, n);
  }
  if (v.tooLong) {
    const n = (el as HTMLInputElement).maxLength;
    return t('forms.maxLength', { n }, n);
  }
  if (v.patternMismatch) return t('forms.pattern');
  return el.validationMessage;
}

/**
 * The `invalid` handler the field components share: suppress the browser's
 * bubble, return our sentence, and move focus to the FIRST refused field of
 * the form (the browser did that as part of the bubble we are suppressing).
 */
export function onFieldInvalid(ev: Event): string {
  const el = ev.target as HTMLInputElement;
  ev.preventDefault();
  const form = el.form;
  if (form && !form.dataset.fxInvalidFocus) {
    form.dataset.fxInvalidFocus = '1';
    el.focus();
    queueMicrotask(() => {
      delete form.dataset.fxInvalidFocus;
    });
  }
  return validityMessage(el);
}

/** An address a server can be reached at: empty, or absolute http(s). */
export function isHttpUrl(v: string | null | undefined): boolean {
  const s = (v ?? '').trim();
  if (!s) return true;
  try {
    const u = new URL(s);
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.host;
  } catch {
    return false;
  }
}

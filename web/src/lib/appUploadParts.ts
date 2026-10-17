/**
 * The files an app's UPLOAD needs beside its manifest, read from the
 * manifest the administrator chose - so the install wizard says which one is
 * missing before it sends anything.
 *
 * An upload carries every package itself: the interface bundle (`ui.zip`,
 * the multipart part `ui`) when the manifest has a `ui` block, and the
 * office engine (`engine.zip`, the part `engine`) when it has an
 * `engines.office` block (filex 0.57). Left out, the server refuses the
 * review - but only after the browser has uploaded everything else, and the
 * Office editor's interface bundle alone is tens of megabytes.
 *
 * ⚠ Only the two blocks' PRESENCE is read here. Whether the manifest is
 * valid, whether it needs a module, whether it is a language pack: the server
 * decides all of that and says it in its own sentence (wasmplugin.stage). A
 * manifest that does not parse is left to the server too.
 */
export type UploadPart = 'ui' | 'engine';

/** What the wizard has chosen beside the manifest. */
export interface ChosenParts {
  ui: boolean;
  engine: boolean;
}

/** The parts the manifest text asks for that are not chosen, in field order. */
export function uploadPartsMissing(manifestText: string, chosen: ChosenParts): UploadPart[] {
  let doc: unknown;
  try {
    doc = JSON.parse(manifestText);
  } catch {
    return [];
  }
  if (!doc || typeof doc !== 'object' || Array.isArray(doc)) return [];
  const m = doc as { ui?: unknown; engines?: { office?: unknown } | null };
  const out: UploadPart[] = [];
  if (isBlock(m.ui) && !chosen.ui) out.push('ui');
  if (m.engines && typeof m.engines === 'object' && isBlock(m.engines.office) && !chosen.engine) out.push('engine');
  return out;
}

function isBlock(v: unknown): boolean {
  return !!v && typeof v === 'object' && !Array.isArray(v);
}

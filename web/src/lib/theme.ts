// Theme handling: 'light' | 'dark' | 'auto'.
// Auto follows the OS via prefers-color-scheme.
const KEY = 'filex.theme';

export type ThemeMode = 'light' | 'dark' | 'auto';

export function getStoredTheme(): ThemeMode {
  const v = localStorage.getItem(KEY);
  if (v === 'light' || v === 'dark' || v === 'auto') return v;
  return 'auto';
}

export function setStoredTheme(mode: ThemeMode): void {
  localStorage.setItem(KEY, mode);
  applyStoredTheme();
}

function isSystemDark(): boolean {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

export function effectiveTheme(): 'light' | 'dark' {
  const mode = getStoredTheme();
  if (mode === 'auto') return isSystemDark() ? 'dark' : 'light';
  return mode;
}

export function applyStoredTheme(): void {
  const dark = effectiveTheme() === 'dark';
  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
}

// React to OS changes when in 'auto' mode.
if (typeof window !== 'undefined' && window.matchMedia) {
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (getStoredTheme() === 'auto') applyStoredTheme();
  });
}

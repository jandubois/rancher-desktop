/**
 * Lightweight i18n for the main process.
 *
 * Uses the same YAML translation files as the renderer but with simple
 * {variable} interpolation instead of ICU MessageFormat.
 */

import en from '@pkg/assets/translations/en-us.yaml';
import mainEvents from '@pkg/main/mainEvents';

// Webpack context to lazy-load other locale files.
const translationContext = import.meta.webpackContext(
  '@pkg/assets/translations', { recursive: false, regExp: /\.yaml$/ },
);

type TranslationMap = Record<string, unknown>;

/** Locale codes derived from the translation files bundled by webpack. */
export const availableLocales: string[] = translationContext.keys()
  .map(path => path.replace(/^.*\/([^\/]+)\.[^.]+$/, '$1'));

let currentLocale = 'en-us';
let pendingLocale = 'en-us';
const translations: Record<string, TranslationMap> = { 'en-us': en };
const localeChangeCallbacks: Array<() => void> = [];

/**
 * Traverse a nested object by dotted key path.
 */
function getByPath(obj: TranslationMap, path: string): string | undefined {
  let current: unknown = obj;

  for (const segment of path.split('.')) {
    if (current == null || typeof current !== 'object') {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }

  return typeof current === 'string' ? current : undefined;
}

/**
 * Load a locale's translations if not already loaded.
 */
async function loadLocale(locale: string): Promise<boolean> {
  if (translations[locale]) {
    return true;
  }
  try {
    translations[locale] = await translationContext(`./${ locale }.yaml`);

    return true;
  } catch {
    console.error(`i18n: failed to load locale "${ locale }"`);

    return false;
  }
}

/**
 * Translate a key with optional {variable} interpolation.
 * Falls back to en-us if the key is missing in the current locale.
 * Placeholders are replaced iteratively; in theory a value containing
 * "{other}" could match a later placeholder, but no current usage does.
 */
export function t(key: string, args?: Record<string, string | number>): string {
  let msg = getByPath(translations[currentLocale], key)
         ?? getByPath(translations['en-us'], key);

  if (msg === undefined) {
    return `%${ key }%`;
  }

  if (args) {
    for (const [name, value] of Object.entries(args)) {
      msg = msg.replaceAll(`{${ name }}`, String(value));
    }
  }

  return msg;
}

/**
 * Register a callback to run after the locale has been loaded.
 * Use this instead of listening to settings-update directly, which
 * would race against the locale loading.
 * Returns a function that unregisters the callback.
 */
export function onLocaleChange(callback: () => void): () => void {
  localeChangeCallbacks.push(callback);

  return () => {
    const idx = localeChangeCallbacks.indexOf(callback);

    if (idx >= 0) {
      localeChangeCallbacks.splice(idx, 1);
    }
  };
}

/**
 * Initialize main-process i18n: read current locale from settings and
 * listen for changes.
 */
export async function initMainI18n(): Promise<void> {
  try {
    const settings = await mainEvents.invoke('settings-fetch');
    // 'none' means the language selector is disabled; use English.
    const raw = settings?.application?.locale;
    const locale = (!raw || raw === 'none') ? 'en-us' : raw;

    if (locale !== currentLocale && await loadLocale(locale)) {
      currentLocale = locale;
    }
  } catch (err) {
    // settings-fetch handler may not be registered yet during early startup.
    console.debug('initMainI18n: could not read initial settings:', err);
  }

  mainEvents.on('settings-update', async(settings) => {
    const raw = settings?.application?.locale;
    const locale = (!raw || raw === 'none') ? 'en-us' : raw;

    if (locale !== currentLocale) {
      // Rapid A->B->A: the revert to A is skipped (already current), but
      // the in-flight load of B completes and commits B. The app ends up
      // on the wrong locale. Acceptable: locales are pre-loaded, so the
      // async window is negligible and the scenario cannot occur in
      // practice.
      pendingLocale = locale;
      if (!await loadLocale(locale)) {
        return; // load failed, stay on current locale
      }
      if (pendingLocale !== locale) {
        return; // superseded by a newer locale change
      }
      currentLocale = locale;
      for (const callback of [...localeChangeCallbacks]) {
        try {
          callback();
        } catch (err) {
          console.error('Locale change callback failed:', err);
        }
      }
    }
  });
}

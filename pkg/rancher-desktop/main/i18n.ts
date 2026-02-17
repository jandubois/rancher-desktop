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
async function loadLocale(locale: string): Promise<void> {
  if (translations[locale]) {
    return;
  }
  try {
    translations[locale] = await translationContext(`./${ locale }.yaml`);
  } catch {
    console.error(`i18n: failed to load locale "${ locale }"`);
  }
}

/**
 * Translate a key with optional {variable} interpolation.
 * Falls back to en-us if the key is missing in the current locale.
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
 */
export function onLocaleChange(callback: () => void): void {
  localeChangeCallbacks.push(callback);
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

    if (locale !== currentLocale) {
      currentLocale = locale;
      await loadLocale(currentLocale);
    }
  } catch {
    // settings-fetch handler may not be registered yet during early startup.
  }

  mainEvents.on('settings-update', async(settings) => {
    const raw = settings?.application?.locale;
    const locale = (!raw || raw === 'none') ? 'en-us' : raw;

    if (locale !== currentLocale) {
      currentLocale = locale;
      await loadLocale(locale);
      for (const callback of localeChangeCallbacks) {
        callback();
      }
    }
  });
}

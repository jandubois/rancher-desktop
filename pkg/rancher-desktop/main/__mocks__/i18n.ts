/**
 * Jest mock for @pkg/main/i18n.
 *
 * The real module uses import.meta.webpackContext (unavailable in Jest).
 * This mock delegates to the shared test translation helper.
 */

export { availableLocales, t } from '@pkg/utils/testUtils/translations';

export function onLocaleChange(_callback: () => void): () => void {
  return () => {};
}

export async function initMainI18n(): Promise<void> {}

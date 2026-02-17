/**
 * Shared translation helper for tests and mocks.
 *
 * Loads the English YAML once and provides a t() function with simple
 * {variable} interpolation, matching the main-process i18n behavior.
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

import yaml from 'js-yaml';

type TranslationMap = Record<string, unknown>;

const thisDir = path.dirname(fileURLToPath(import.meta.url));
const translationsDir = path.resolve(thisDir, '../../assets/translations');
const enPath = path.join(translationsDir, 'en-us.yaml');
const en = yaml.load(fs.readFileSync(enPath, 'utf8')) as TranslationMap;

/** Locale codes derived from the YAML files in the translations directory. */
export const availableLocales: string[] = fs.readdirSync(translationsDir)
  .filter(f => f.endsWith('.yaml'))
  .map(f => f.replace(/\.yaml$/, ''));

function getByPath(obj: TranslationMap, keyPath: string): string | undefined {
  let current: unknown = obj;

  for (const segment of keyPath.split('.')) {
    if (current == null || typeof current !== 'object') {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }

  return typeof current === 'string' ? current : undefined;
}

export function t(key: string, args?: Record<string, string | number>): string {
  let msg = getByPath(en, key);

  if (msg === undefined) {
    return `%${ key }%`;
  }

  if (args) {
    for (const [name, value] of Object.entries(args)) {
      msg = msg!.replaceAll(`{${ name }}`, String(value));
    }
  }

  return msg!;
}

/**
 * Jest mock for @pkg/main/i18n.
 *
 * The real module uses import.meta.webpackContext (unavailable in Jest).
 * This mock loads the English YAML directly and provides the same t()
 * interpolation so that test assertions against error strings keep working.
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

export async function initMainI18n(): Promise<void> {}

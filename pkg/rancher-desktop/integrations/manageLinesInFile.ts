import fs from 'fs';
import os from 'os';

import isEqual from 'lodash/isEqual.js';

import Logging from '@pkg/utils/logging';

export const START_LINE = '### MANAGED BY RANCHER DESKTOP START (DO NOT EDIT)';
export const END_LINE = '### MANAGED BY RANCHER DESKTOP END (DO NOT EDIT)';
const DEFAULT_FILE_MODE = 0o644;
const console = Logging['path-management'];

// Inserts/removes fenced lines into/from a file. Idempotent.
// @param path The path to the file to work on.
// @param desiredManagedLines The lines to insert into the file.
// @param desiredPresent Whether the lines should be present.
export default async function manageLinesInFile(path: string, desiredManagedLines: string[], desiredPresent: boolean): Promise<void> {
  // read file, creating it if it doesn't exist
  let currentContent: string;

  console.log(Error(`manageLinesInFile path=${ path } desiredLines='${ desiredManagedLines }' should${ desiredPresent ? '' : ' not' } exist.`).stack);
  try {
    currentContent = await fs.promises.readFile(path, 'utf8');
    console.log(`  manageLinesInFile current length=${ currentContent.length}`);
  } catch (error: any) {
    if (error.code === 'ENOENT' && desiredPresent) {
      const lines = buildFileLines([], desiredManagedLines, ['']);
      const content = lines.join(os.EOL);

      console.log(`  manageLinesInFile path='${ path }': file does not exist, writing new file`);
      await fs.promises.writeFile(path, content, { mode: DEFAULT_FILE_MODE });

      return;
    } else if (error.code === 'ENOENT' && !desiredPresent) {
      console.log(`  manageLinesInFile path='${ path }': file does not exist, not doing anything`);

      return;
    } else {
      console.log(`  manageLinesInFile throwing error: ${ JSON.stringify(error) }`);
      throw error;
    }
  }

  // split file into three parts
  let before: string[];
  let currentManagedLines: string[];
  let after: string[];

  try {
    const currentLines = currentContent.split('\n');

    console.log(`  manageLinesInFile: splitting ${ currentLines.length } lines`);
    [before, currentManagedLines, after] = splitLinesByDelimiters(currentLines);
  } catch (error) {
    throw new Error(`  manageLinesInFile: could not split ${ path }: ${ error }`);
  }

  console.log(`  manageLinesInFile: splitLinesByDelimiters returned: before='${ before }' current='${ currentManagedLines }' after='${ after }'`);
  // make the changes
  if (desiredPresent && !isEqual(currentManagedLines, desiredManagedLines)) {
    // This is needed to ensure the file ends with an EOL
    if (after.length === 0) {
      after = [''];
    }
    const newLines = buildFileLines(before, desiredManagedLines, after);
    const newContent = newLines.join(os.EOL);

    console.log(`${ path }: adding lines to file. newContent='${ newContent }'`);
    await fs.promises.writeFile(path, newContent);
  }
  if (!desiredPresent) {
    // Ignore the extra empty line that came from the managed block.
    if (after.length === 1 && after[0] === '') {
      after = [];
    }
    if (before.length === 0 && after.length === 0) {
      console.log(`  manageLinesInFile ${ path }: removing empty file.`);
      await fs.promises.rm(path);
    } else {
      const newLines = buildFileLines(before, [], after);
      const newContent = newLines.join(os.EOL);

      console.log(`  manageLinesInFile ${ path }: removing lines from file. newContent='${ newContent }'`);
      await fs.promises.writeFile(path, newContent);
    }
  }
}

// Splits a file into three arrays containing the lines before the managed portion,
// the lines in the managed portion and the lines after the managed portion.
// @param lines An array where each element represents a line in a file.
function splitLinesByDelimiters(lines: string[]): [string[], string[], string[]] {
  const startIndex = lines.indexOf(START_LINE);
  const endIndex = lines.indexOf(END_LINE);

  if (startIndex < 0 && endIndex < 0) {
    return [lines, [], []];
  } else if (startIndex < 0 || endIndex < 0) {
    throw new Error('exactly one of the delimiter lines is not present');
  } else if (startIndex >= endIndex) {
    throw new Error('the delimiter lines are in the wrong order');
  }

  const before = lines.slice(0, startIndex);
  const currentManagedLines = lines.slice(startIndex + 1, endIndex);
  const after = lines.slice(endIndex + 1);

  return [before, currentManagedLines, after];
}

// Builds an array where each element represents a line in a file.
// @param before The portion of the file before the managed lines.
// @param toInsert The managed lines, not including the fences.
// @param after The portion of the file after the managed lines.
function buildFileLines(before: string[], toInsert: string[], after: string[]): string[] {
  const rancherDesktopLines = toInsert.length > 0 ? [START_LINE, ...toInsert, END_LINE] : [];

  return [...before, ...rancherDesktopLines, ...after];
}

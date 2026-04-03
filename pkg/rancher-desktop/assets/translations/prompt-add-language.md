# Add a new language to Rancher Desktop

Register a new locale in the build system, settings validator, and API spec,
then generate translations for all keys that Rancher Desktop displays.

## Prerequisites

Decide on the locale code (e.g., `de`, `fr`, `ja`, `fa`) and the native name
of the language as it appears in its own script (e.g., `Deutsch` for German).
The locale code must match the YAML filename.

## Step 1: Create the locale file

Create an empty file at `pkg/rancher-desktop/assets/translations/<code>.yaml`.
Start empty so `missing` shows every key that needs translation.
Webpack discovers new YAML files automatically.

## Step 2: Add locale display names

Each locale file contains a `locale:` section that maps locale codes to their
native display names. The language picker reads these entries.

Add `locale.<code>: <native name>` to **every** existing locale file and to
`en-us.yaml`. Use the language's native name as the value — the same value in
every file. For example:

```yaml
# in en-us.yaml, zh-hans.yaml, and the new file itself:
locale:
  <code>: <native name>
```

In locale files other than `en-us.yaml`, add a `@reason` comment:

```yaml
# @reason Native name of the language, kept as-is
<code>: <native name>
```

Also add a `locale.none` entry to the new file. Translate the word "None" into
the target language with a `@reason` comment:

```yaml
locale:
  # ... all locale entries ...
  # @reason "<explanation>"
  none: (<translated "None">)
```

## Step 3: Update the API spec

In `pkg/rancher-desktop/assets/specs/command-api.yaml`, find the
`application.locale` enum and add the new code in alphabetical order:

```yaml
locale:
  type: string
  enum: [de, en-us, <code>, zh-hans]
  x-rd-usage: set the UI language
```

## Step 4: Update the settings validator

In `pkg/rancher-desktop/main/commandServer/settingsValidator.ts`, find the
`checkEnum` call for `locale` and add the new code in alphabetical order:

```typescript
locale: this.checkEnum('de', 'en-us', '<code>', 'zh-hans'),
```

## Step 5: Update the validator test

In `pkg/rancher-desktop/main/commandServer/__tests__/settingsValidator.spec.ts`,
find the `locale` describe block and update two places:

1. The `test.each` array of valid locales — add the new code.
2. The expected error message string — it lists all valid enum values.

Search for `must be one of` to find the error string. The codes appear as a
JSON array in alphabetical order:

```
must be one of ["de","en-us","<code>","zh-hans"]
```

## Step 6: Regenerate Go CLI code

Run `yarn postinstall` from the repository root. This regenerates the Go CLI
option definitions from the API spec. The generated file is gitignored.

## Step 7: Run tests

Run the settings validator tests to confirm the registration is correct:

```sh
BROWSERSLIST_IGNORE_OLD_DATA=1 \
  node --experimental-vm-modules node_modules/jest/bin/jest.js \
  pkg/rancher-desktop/main/commandServer/__tests__/settingsValidator.spec.ts
```

All tests (currently 213) must pass.

## Step 8: Commit the infrastructure

Commit all changes from steps 1–7 together. Example message:

```
Add <Language> (<code>) locale infrastructure
```

## Step 9: Generate translations

Run the translate report to get all keys with their English values:

```sh
go tool i18n-report translate --locale=<code>
```

Each line is `key=value`.

To split the work across parallel translation agents, use `--batch` and
`--batches`:

```sh
go tool i18n-report translate --locale=<code> --batch=1 --batches=3
go tool i18n-report translate --locale=<code> --batch=2 --batches=3
go tool i18n-report translate --locale=<code> --batch=3 --batches=3
```

Feed each batch to a separate translation agent using
`prompt-update-translations.md`. Save each agent's output to a file, then
merge all results at once:

```sh
go tool i18n-report merge --locale=<code> batch1.output batch2.output batch3.output
```

The merge command handles three input formats: JSONL agent output, markdown
with YAML fences, and raw flat `key=value` text. It extracts translations
automatically, builds nested YAML with `@reason` comments, and writes the
locale file.

## Step 10: Verify

Run check and confirm all checks pass:

```sh
go tool i18n-report check --locale=<code>
```

Or run each report individually:

```sh
# All keys are translated:
go tool i18n-report translate --locale=<code>

# No orphaned keys:
go tool i18n-report stale --locale=<code>

# No unused keys introduced:
go tool i18n-report unused
```

## Step 11: Commit translations

Commit the translated locale file. Example message:

```
Add <Language> translations for all Rancher Desktop UI strings
```

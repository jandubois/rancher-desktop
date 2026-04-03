# Translate missing keys into a target locale

Generate translations for keys present in `en-us.yaml` but missing from a
target locale file. Each translation includes a `@reason` comment explaining
the choice.

## Steps

1. Run the translate report to get the keys missing from the target locale,
   with their English values:

   ```sh
   go tool i18n-report translate --locale=de
   ```

   Replace `de` with the target locale code. Each line is `key=value`.

2. For each key in the output, check any `@context`, `@meaning`, and
   `@no-translate` annotations in `en-us.yaml`.

3. Generate the translation, following these rules:

   - **Never replace an existing translation without cause.** If a key already
     has a translation in the locale file, keep it. Only change an existing
     translation when it is clearly wrong (mistranslation, broken interpolation,
     stale after an English string change). When you do change one, add a
     `@reason` comment explaining why the old translation was replaced.

   - **Preserve interpolation syntax exactly.** Keep `{variable}` placeholders
     unchanged. Example: `"Container engine: {name}"` becomes
     `"容器引擎: {name}"` — the `{name}` stays literal.

   - **Preserve ICU plural syntax.** Copy the `{count, plural, ...}` structure
     and translate only the message text inside each branch.

   - **Honor @no-translate hints.** Terms listed in `@no-translate` comments
     (brand names, CLI commands, technical identifiers) should stay in English
     by default. If the target language has a widely accepted local equivalent,
     you may translate it — add a `@reason` comment explaining the choice.

   - **Use @context to choose the right register.** A tooltip needs concise
     phrasing; a confirmation dialog can use a full sentence.

   - **Use @meaning to pick the correct term.** When multiple translations
     exist for an English word, the `@meaning` annotation disambiguates.

4. Add a `@reason` comment above each translated key explaining the
   translation choice. This helps reviewers understand your reasoning and
   prevents future sessions from switching to a different phrasing:

   ```yaml
   # @reason 容器引擎 is the standard Chinese term for "container engine";
   #   kept {name} as interpolation placeholder
   containerEngine: "容器引擎: {name}"
   ```

5. Output translations as flat `key=value` or `key: value` lines with
   `# @reason` comments above each key. Save or pipe the output to the
   merge command, which builds the nested YAML locale file automatically:

   ```sh
   go tool i18n-report merge --locale=de translations.txt
   ```

   The merge command also accepts JSONL agent output files and markdown
   with YAML fences — no manual extraction needed.

6. After merging, re-run the translate report and confirm zero keys remain:

   ```sh
   go tool i18n-report translate --locale=de
   ```

   The output should say "No keys missing from de."

7. Run the stale-keys report to ensure no orphaned keys crept in:

   ```sh
   go tool i18n-report stale --locale=de
   ```

## Commit strategy

Commit translations in logical groups (one top-level YAML section per commit).
Include the locale code and section name in the commit message.

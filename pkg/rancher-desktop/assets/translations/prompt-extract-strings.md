# Extract hardcoded strings into en-us.yaml

Find English strings hardcoded in Vue templates and replace them with `t()`
calls backed by entries in `en-us.yaml`.

## Steps

1. Run the untranslated-strings report to find candidates:

   ```sh
   go tool i18n-report untranslated
   ```

   This scans Vue files for attributes like `label="Some Text"` that should
   use translation calls instead.

2. Work one file at a time. For each flagged file:

   a. Read the file and identify every hardcoded user-visible string
      (the report catches common attributes, but also check headings, button
      text, error messages, and template text).

   b. Choose a translation key following the existing naming convention in
      `en-us.yaml`. Keys use dot-separated lowercase segments matching the
      component hierarchy:

      ```
      troubleshooting.kubernetes.resetKubernetes.title
      preferences.containerEngine.allowedImages
      portForwarding.title
      ```

   c. Add the English string to `en-us.yaml` under the appropriate section,
      with a `@context` comment (see `prompt-generate-context.md`).

   d. Replace the hardcoded string in the Vue file with a `t()` call.

3. Commit each file as a separate commit so reviewers can check one
   component at a time.

## Translation call patterns

Use the pattern that matches the context:

| Context | Pattern |
|---|---|
| Vue attribute binding | `:label="t('section.key')"` |
| Template text | `{{ t('section.key') }}` |
| Options API script | `this.t('section.key')` |
| Composition API / setup | `t('section.key')` |

For strings with interpolation, pass variables as the second argument:

```vue
{{ t('tray.containerEngine', { name: engineName }) }}
```

## What to extract

- Button labels, headings, descriptions, tooltips, placeholder text
- Error and confirmation messages
- Menu items and tab labels

## What to leave alone

- Strings that are never shown to users (CSS classes, event names, log keys)
- Brand names used as identifiers, not display text
- Strings already using `t()` calls
- Numeric or symbolic values (`"100%"`, `"/"`)

## Verification

After extracting strings from a file, run the unused-keys report to confirm
the new keys register as referenced:

```sh
go tool i18n-report unused
```

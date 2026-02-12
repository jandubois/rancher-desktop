# Translations

This directory holds the YAML translation files for Rancher Desktop.

## Files

| File | Purpose |
|------|---------|
| `en-us.yaml` | Canonical English strings (source of truth) |
| `de.yaml` | German translation |
| `zh-hans.yaml` | Simplified Chinese translation |

## Architecture

The renderer uses a Vuex store (`store/i18n.js`) with `intl-messageformat` for
ICU MessageFormat. The main process uses `main/i18n.ts` with simple `{variable}`
interpolation. Both load YAML through webpack's `js-yaml-loader` at build time.

Webpack auto-discovers locale YAML files in this directory (one file per
locale, named `{code}.yaml`). The `meta/` subdirectory holds the locale
manifest and per-locale metadata; these are not loaded by webpack.

The `application.locale` setting controls language selection; the main process
and renderer sync it over IPC.

### Template `t()` function

The i18n plugin (`plugins/i18n.js`) injects a global `t(key, args, raw)`
function into all Vue components. The third parameter, `raw`, controls
HTML escaping:

- `t('some.key')` — returns HTML-escaped text (default, safe for rendering).
- `t('some.key', {}, true)` — returns the raw translation string without
  HTML escaping. Use this when the string contains intentional HTML (e.g.,
  `<a>` tags) and the caller handles sanitization.

The `<t>` component and `v-t` directive accept `raw` as a prop or modifier:

```html
<t k="some.key" raw />
<span v-t.raw="'some.key'" />
```

## YAML comment conventions

Add these comments directly above the key they describe. The `js-yaml-loader`
strips all comments at build time, so they cost nothing at runtime.

| Comment | Where | Purpose |
|---------|-------|---------|
| `@context` | en-us.yaml | Where in the UI the string appears |
| `@meaning` | en-us.yaml | Domain-specific meaning when English is ambiguous |
| `@no-translate` | en-us.yaml | Terms that should stay in English by default |
| `@reason` | locale files | Why a particular translation was chosen |

### Examples in en-us.yaml

```yaml
# @context Preferences > Application > General, checkbox label
# @meaning Administrative privilege escalation for bridged networking and docker socket
application:
  adminAccess:
    label: Allow to acquire administrative credentials (sudo access)

# @context Preferences > Container Engine > General, dropdown label
# @meaning The OCI runtime (containerd or moby/dockerd), not a JavaScript engine
containerEngine:
  label: Container Engine

# @no-translate — Unix command name
resetKubernetes:
  description: "Run {command} to reset Kubernetes"
```

### Examples in locale files

```yaml
# @reason "Administratorzugriff" is the standard German term for admin access
#   in software UIs; "sudo" kept untranslated as a Unix command name
application:
  adminAccess:
    label: Administratorzugriff erlauben (sudo-Zugriff)

# @reason "Container-Laufzeit" (container runtime) is more common in German
#   than a literal translation of "container engine"
containerEngine:
  label: Container-Laufzeit
```

## The i18n-report tool

A Go CLI at `src/go/i18n-report/` for translation maintenance. See
`src/go/i18n-report/README.md` for full documentation.

| Subcommand | Description |
|------------|-------------|
| `unused` | Keys in en-us.yaml not referenced in source code |
| `missing` | Keys in en-us.yaml absent from a target locale |
| `stale` | Keys in a locale file absent from en-us.yaml |
| `translate` | Keys missing from a locale, with English values |
| `merge` | Read flat translations, write nested YAML locale file |
| `remove` | Remove keys from translation files (stdin or `--stale`) |
| `untranslated` | Hardcoded English strings in Vue/TS files (heuristic) |
| `references` | Where each en-us.yaml key is used (file:line) |
| `dynamic` | Template literal patterns that reference keys dynamically |
| `check` | Lint check: unused + stale + missing translations |
| `manifest` | Validate meta/locales.yaml manifest |
| `meta` | Generate source metadata for a locale |
| `drift` | Detect translated keys whose English source changed |
| `validate` | Structural checks: placeholders, tags, metadata, overrides |

Run from the repository root:

```sh
go tool i18n-report translate --locale=fa
go tool i18n-report translate --locale=fa --batch=1 --batches=3
go tool i18n-report merge --locale=fa agent1.output agent2.output
go tool i18n-report missing --locale=zh-hans
go tool i18n-report unused --format=json
go tool i18n-report check --locale=de
```

The `merge` subcommand accepts agent output files (JSONL), markdown with YAML
fences, or raw flat `key=value` text. Without file arguments, it reads from
stdin.

## Adding a new language

1. Create an empty locale file `{code}.yaml` in this directory.
2. Register the locale code in four places: en-us.yaml locale names,
   `command-api.yaml` enum, `settingsValidator.ts` `checkEnum`, and
   `settingsValidator.spec.ts` error string.
3. Add the locale to `meta/locales.yaml` with `status: experimental`.
4. Run `yarn postinstall` to regenerate Go CLI code from the API spec.
5. Run `go tool i18n-report translate --locale={code}` to get keys
   that need translation; translate them and merge with
   `go tool i18n-report merge --locale={code}`.

Webpack discovers new YAML files automatically — no other code changes are
needed.

## Maintenance workflow

1. Remove dead keys from all translation files:
   ```sh
   go tool i18n-report unused | go tool i18n-report remove
   ```
2. Remove stale keys from locale files (keys not in en-us.yaml):
   ```sh
   go tool i18n-report remove --stale
   ```
3. Run `i18n-report translate --locale=<code>` to find keys that need
   translation, then merge the results with `i18n-report merge`.
4. Run `i18n-report untranslated` to find hardcoded English strings in
   Vue/TS files that should be externalized.

## Known limitations and deferred work

### Feature gate

The locale selector is hidden by default (`application.locale: 'none'`).
Users must enable it via `rdctl` or by editing `settings.json`. When
removing this gate, also address:

- **Structured validator diagnostics.** `settingsValidator.ts` emits
  localized error strings. Callers already use the `hasLockedFieldError`
  flag instead of string matching, but other error categories still rely
  on the English text. Replace with structured return values so error
  classification works regardless of locale.
- **HTML in translation strings.** Several keys embed `<a>` tags with
  `data-action` or `data-navigate` attributes that application code
  relies on. Restructure these to use component slots or structured
  placeholders so translators cannot break link behavior.
- **Preferences window title.** The native window title is set at
  creation but not refreshed on locale change. Register an
  `onLocaleChange` handler to update it.

### Callback lifecycle

`onLocaleChange()` in `main/i18n.ts` returns an unregister function.
Callers that register during a lifecycle (e.g., tray show) must call
the returned function during teardown (e.g., tray hide) to avoid
leaking callbacks.

### i18n-report tool

- **Validate/check divergence.** `reportValidateQuiet` (used by the
  `check` command) omits metadata coherence checks that `reportValidate`
  includes. Extract shared validation logic.
- **ICU nested placeholders.** `extractPlaceholderNames` only detects
  placeholders at depth 0. Nested ICU constructs (e.g., plurals
  containing placeholders) are not validated. No current strings use
  nested ICU, so this is not yet a practical issue.
- **Non-atomic file writes.** `os.WriteFile` truncates before writing.
  An interrupted process could corrupt locale or metadata files.
  Consider write-to-temp-then-rename.

### Code duplication

The `getByPath` and `{variable}` interpolation logic is reimplemented
in `main/i18n.ts`, `main/__mocks__/i18n.ts`, and
`UpdateStatus.spec.ts`. Extract into a shared test utility.

### Scanner gaps

The source scanner (`scan.go`) does not detect translation candidates
in `showErrorBox` calls (`tray.ts`, `settingsImpl.ts`) or port
forwarding error messages (`backend/kube/client.ts`). It also skips
`__tests__` directories, so the `references` report omits test-only
key usage.

### Navigation identifiers

`transientSettings.ts` uses English nav item names as internal
identifiers. These are no longer displayed directly (preference tabs
use `labelKey`), but the internal/display split remains unresolved.

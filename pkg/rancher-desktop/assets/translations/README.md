# Translations

This directory holds the YAML translation files for Rancher Desktop and the
prompt files that drive the AI-assisted translation workflow.

## Files

| File | Purpose |
|------|---------|
| `en-us.yaml` | Canonical English strings (source of truth) |
| `zh-hans.yaml` | Simplified Chinese translation |
| `prompt-add-language.md` | Register a new locale and generate its translations |
| `prompt-generate-context.md` | Annotate en-us.yaml with `@context`/`@meaning` comments |
| `prompt-extract-strings.md` | Extract hardcoded English from Vue components into en-us.yaml |
| `prompt-update-translations.md` | Generate translations for missing keys in a locale |
| `prompt-review-translations.md` | Structure a review session with a native speaker |

## Architecture

The renderer uses a Vuex store (`store/i18n.js`) with `intl-messageformat` for
ICU MessageFormat. The main process uses `main/i18n.ts` with simple `{variable}`
interpolation. Both load YAML through webpack's `js-yaml-loader` at build time.

Webpack auto-discovers YAML files in this directory. Adding a new file here
makes it available as a locale without code changes.

The `application.locale` setting controls language selection; the main process
and renderer sync it over IPC.

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
| `untranslated` | Hardcoded English strings in Vue files (heuristic) |
| `references` | Where each en-us.yaml key is used (file:line) |
| `check` | Combined lint check (unused + stale + missing) |

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

## Using the prompt files

Each `prompt-*.md` file contains self-contained instructions for an AI
assistant. Feed the prompt content as context along with your task. For
example, to translate missing keys into Simplified Chinese, provide
`prompt-update-translations.md` and ask the assistant to translate the
missing keys for `zh-hans`.

## Adding a new language

See `prompt-add-language.md` for the complete step-by-step procedure. In
summary: create an empty locale file, register the locale code in four places
(en-us.yaml locale names, `command-api.yaml` enum, `settingsValidator.ts`
`checkEnum`, and `settingsValidator.spec.ts` error string), run
`yarn postinstall`, then generate translations with
`prompt-update-translations.md`.

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
   translation. Use `prompt-update-translations.md` to fill them in.
4. Run `i18n-report untranslated` to find hardcoded English strings in Vue
   components. Use `prompt-extract-strings.md` to externalize them.

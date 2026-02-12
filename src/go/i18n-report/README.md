# i18n-report

A CLI tool for maintaining Rancher Desktop's translation files. It scans
source code and YAML locale files to find unused keys, missing translations,
hardcoded English strings, and other i18n issues.

## Quick start

Run from the repository root:

```sh
go tool i18n-report <subcommand> [flags]
```

Or build and run the binary:

```sh
go build -o src/go/i18n-report/i18n-report ./src/go/i18n-report
./src/go/i18n-report/i18n-report <subcommand> [flags]
```

## Subcommands

### unused

Find keys in `en-us.yaml` that no source file references. These keys can
be removed.

```sh
i18n-report unused [--format=json|text]
```

### missing

Find keys in `en-us.yaml` absent from a target locale file.

```sh
i18n-report missing --locale=de [--format=json|text]
```

### stale

Find keys in a locale file absent from `en-us.yaml`. These keys are
obsolete and should be removed.

```sh
i18n-report stale --locale=de [--format=json|text]
```

### translate

List keys missing from a locale, with their English values.

```sh
i18n-report translate --locale=de [--format=json|text]
```

Split the output across parallel translation agents with `--batch` and
`--batches`:

```sh
i18n-report translate --locale=de --batch=1 --batches=3
i18n-report translate --locale=de --batch=2 --batches=3
i18n-report translate --locale=de --batch=3 --batches=3
```

Each batch outputs `key=value` lines suitable for piping to a translation
agent or saving to a file.

### merge

Read flat translations and write (or update) a nested YAML locale file.
Accepts file arguments or reads from stdin.

```sh
i18n-report merge --locale=de batch1.out batch2.out batch3.out
i18n-report merge --locale=de < translations.txt
```

Input formats detected automatically:
- **JSONL agent output** — extracts text from assistant messages
- **Markdown with `` ```yaml `` fences** — extracts content between fences
- **Raw flat text** — `key=value` or `key: value` lines passed through

The merge command preserves existing translations, adds new keys, and
maintains `# @reason` comments. New entries override existing ones for
the same key.

### untranslated

Scan Vue and TypeScript files for hardcoded English strings that should
use `t()` calls.

```sh
i18n-report untranslated [--format=json|text] [--include-descriptions]
```

The `--include-descriptions` flag extends the scan to `description`
properties, catching diagnostics strings in `main/diagnostics/*.ts`.

This report uses heuristics and may produce false positives. Known gaps
include Electron menu labels, `showErrorBox` calls, port forwarding errors,
and template-literal strings.

### references

Show where each `en-us.yaml` key is used in source code.

```sh
i18n-report references [--format=json|text]
```

### remove

Remove keys from translation files. Two modes:

**Pipe mode** — reads dotted keys from stdin and removes them from all
translation files (en-us.yaml and every locale):

```sh
i18n-report unused | i18n-report remove
```

Non-key lines (headers, blank lines) are filtered out automatically, so
the output of `unused` or `stale` can be piped directly.

**Stale mode** — removes keys from each locale file that do not exist in
en-us.yaml:

```sh
i18n-report remove --stale
```

### check

Run unused, stale, and missing checks together. Reports pass/fail counts
and exits with code 1 on any failure.

```sh
i18n-report check --locale=de
```

Example output:

```
  unused keys:                    0  OK
  stale keys in de:               0  OK
  keys missing from de:           0  OK
All checks passed.
```

## Common workflows

### Clean up dead keys

```sh
i18n-report unused | i18n-report remove   # remove from all files
i18n-report remove --stale                # remove locale-only leftovers
```

### Translate missing keys

```sh
i18n-report translate --locale=de --batch=1 --batches=3 > batch1.txt
# Feed each batch to a translation agent (see prompt-update-translations.md).
i18n-report merge --locale=de batch1.out batch2.out batch3.out
i18n-report check --locale=de       # verify
```

### Find strings to extract

```sh
i18n-report untranslated            # find hardcoded English
# Extract strings into en-us.yaml (see prompt-extract-strings.md).
i18n-report unused                  # confirm new keys are referenced
```

### Add a new language

See `prompt-add-language.md` in the translations directory.

## How it works

### Source scanning

The tool walks `pkg/rancher-desktop/` looking for `.vue`, `.ts`, and `.js`
files. It skips `node_modules`, `.git`, `dist`, `vendor`, and `__tests__`
directories.

Key references are found by matching several regex patterns:
- `t('key')`, `t("key")`, `` t(`key`) ``, `this.t(...)`, `$t(...)`
- `titleKey`, `descriptionKey`, `labelKey` properties
- `label-key="..."` Vue template attributes
- Indirect references: property values that match en-us.yaml keys

### Untranslated heuristics

The untranslated scanner checks:
- Unbound HTML attributes (`label="..."`, `placeholder="..."`, etc.)
- Text between HTML tags (same line and cross-line)
- Bound string literal attributes (`:label="'text'"`)
- Electron dialog properties (`title`, `message`, `detail`)
- Validation error messages (`errors.push('...')`)

It skips test files, lines already using `t()` or bound attributes, and
values matching common non-translatable patterns (URLs, CSS classes,
identifiers).

### Merge pipeline

The merge command:
1. Reads existing locale file (if any)
2. Extracts flat text from input files (handling JSONL, markdown, raw)
3. Parses `key=value` or `key: value` lines with `# @reason` comments
4. Merges new entries with existing ones (new overrides old)
5. Writes sorted, nested YAML with blank lines between top-level groups

## Development

### Running tests

```sh
go test ./src/go/i18n-report/...
```

### File layout

| File | Contents |
|------|----------|
| `main.go` | Subcommand dispatch, usage text |
| `repo.go` | Repository root detection, path helpers |
| `yaml.go` | YAML flatten/unflatten, scalar formatting, nested writer |
| `scan.go` | Source file scanning, key reference detection |
| `output.go` | Shared text/JSON output formatter |
| `report_unused.go` | `unused` subcommand |
| `report_missing.go` | `missing` subcommand |
| `report_stale.go` | `stale` subcommand |
| `report_translate.go` | `translate` subcommand |
| `report_merge.go` | `merge` subcommand, input parsing, extraction |
| `report_untranslated.go` | `untranslated` subcommand, heuristic scanner |
| `report_references.go` | `references` subcommand |
| `report_remove.go` | `remove` subcommand, YAML key removal |
| `report_check.go` | `check` subcommand |

All files are in `package main`. The tool has one external dependency:
`gopkg.in/yaml.v3`.

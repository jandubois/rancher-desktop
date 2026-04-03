# Review translations with a native speaker

Walk through proposed translations with a native speaker to catch unnatural
phrasing, incorrect terminology, and inconsistencies.

## Setup

Gather these inputs before the review session:

- The target locale file (e.g., `zh-hans.yaml`)
- The English source file (`en-us.yaml`) with `@context` and `@meaning`
  annotations
- The `@reason` comments from the translation step

Run the missing-keys report to confirm all keys have translations:

```sh
go tool i18n-report missing --locale=zh-hans
```

## Review process

Present each key to the reviewer in this format:

```
Key:         preferences.containerEngine.title
English:     Container Engine
Translation: 容器引擎
Reason:      Standard technical term in Chinese container documentation
Context:     Preferences page, section heading for container runtime settings
```

For each entry, ask the reviewer:

1. Does the translation read naturally to a native speaker?
2. Does it match established terminology for this domain?
3. Does it fit the UI context (length, formality, consistency with nearby
   strings)?

## Recording corrections

When the reviewer proposes a change, update both the translation and its
`@reason` comment:

```yaml
# @reason Reviewer changed from 容器引擎 to 容器运行时 to match
#   industry-standard terminology used in Chinese Kubernetes docs
containerEngine: "容器运行时: {name}"
```

The `@reason` comment preserves the rationale for future translators.

## Focus areas

Prioritize review effort on:

- **Consistency:** Ensure the same English term translates the same way
  throughout the file. Search for duplicates.
- **Natural phrasing:** UI text should sound like native application copy,
  not a word-for-word translation.
- **Technical accuracy:** Verify that domain terms (container, volume, pod,
  image, cluster) use the accepted translations for the target language.
- **Length:** Translations much longer than the English original may overflow
  UI elements. Flag these.
- **Interpolation safety:** Confirm that `{variable}` placeholders survive
  intact and appear in a grammatically correct position.

## After the review

Commit the corrections with a message noting the reviewer and locale, e.g.,
"Apply zh-hans review corrections from [reviewer name]".

Run the stale-keys report to confirm no keys were accidentally removed:

```sh
go tool i18n-report stale --locale=zh-hans
```

# Generate @context and @meaning annotations for en-us.yaml

Add translator annotations to `en-us.yaml` so translators understand where
each string appears and what domain-specific terms mean.

## Annotation format

Write annotations as YAML comments directly above the key they describe:

```yaml
# @context Preferences > Kubernetes page, label for version dropdown
# @meaning The Kubernetes release version, not the app version
kubernetesVersion: Kubernetes version
```

- `@context` states where the string appears in the UI (page, dialog, button,
  tooltip, etc.) and what role it plays.
- `@meaning` clarifies domain-specific terms that a translator might
  misinterpret. Omit it when the English string is unambiguous.
- `@no-translate` lists terms in the value that should stay in English by
  default: brand names (Rancher Desktop, Kubernetes), CLI commands
  (`nerdctl`, `kubectl`), and technical identifiers. Translators may still
  provide local equivalents if a standard term exists in their language.

## Steps

1. Run the references report to find where each key appears in source code:

   ```sh
   go tool i18n-report references --format=json
   ```

2. For each key, read the surrounding code at the reported file:line locations.
   Look for:
   - Which page or component renders the string
   - Whether it labels a button, heading, tooltip, checkbox, error message, etc.
   - What `{variable}` interpolations represent
   - Any domain terms that need explanation

3. Write a `@context` comment that tells the translator the UI location and
   the string's role. Be specific:
   - Good: `@context Container Engine settings page, tooltip for "Allowed Images" toggle`
   - Weak: `@context Settings page`

4. Write a `@meaning` comment only when the English text uses a term with
   multiple possible translations. For example, "volume" means a Docker
   storage volume here, not audio volume.

5. Add `@no-translate` when the string contains brand names or commands that
   should stay in English by default. Example:

   ```yaml
   # @context System tray menu, shows active container runtime
   # @no-translate containerd, moby
   containerEngine: "Container engine: {name}"
   ```

6. Commit after annotating each top-level section (e.g., `tray`, `preferences`,
   `troubleshooting`). This keeps changes reviewable.

## Principles

- Derive all context from live code. Run the references report rather than
  guessing.
- Write annotations for a translator who has never seen the application.
- Keep each annotation to one line when possible.
- Preserve existing YAML structure and values exactly; change only comments.

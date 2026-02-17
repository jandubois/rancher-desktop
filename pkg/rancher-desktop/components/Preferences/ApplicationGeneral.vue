<script lang="ts">

import { defineComponent } from 'vue';
import { mapGetters } from 'vuex';

import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';
import { Settings } from '@pkg/config/settings';
import { RecursiveTypes } from '@pkg/utils/typeUtils';

import type { PropType } from 'vue';

export default defineComponent({
  name:       'preferences-application-general',
  components: { RdCheckbox, RdFieldset },
  props:      {
    preferences: {
      type:     Object as PropType<Settings>,
      required: true,
    },
  },
  data() {
    return {
      sudoAllowedTooltip: `
        If checked, Rancher Desktop will attempt to acquire administrative
        credentials ("sudo access") when starting for some operations.  This
        allows for enhanced functionality, including bridged networking and
        default docker socket support.  Changes will only be applied next time
        Rancher Desktop starts.
      `,
      automaticUpdates: true,
      statistics:       false,
    };
  },
  computed: {
    ...mapGetters('preferences', ['isPlatformWindows', 'isPreferenceLocked']),
    ...mapGetters('i18n', ['availableLocales']),
    isSudoAllowed(): boolean {
      return this.preferences?.application?.adminAccess ?? false;
    },
    canAutoUpdate(): boolean {
      return this.preferences?.application.updater.enabled ?? false;
    },
    isLocaleEnabled(): boolean {
      return this.preferences?.application?.locale !== 'none';
    },
    selectedLocale(): string {
      const locale = this.preferences?.application?.locale;

      return (!locale || locale === 'none') ? 'en-us' : locale;
    },
    showLocaleDisclaimer(): boolean {
      return this.selectedLocale !== 'en-us';
    },
  },
  methods: {
    onChange<P extends keyof RecursiveTypes<Settings>>(property: P, value: RecursiveTypes<Settings>[P]) {
      this.$store.dispatch('preferences/updatePreferencesData', { property, value });
    },
    onLocaleChange(event: Event) {
      const locale = (event.target as HTMLSelectElement).value;

      this.onChange('application.locale', locale);
      this.$store.dispatch('i18n/switchTo', locale);
    },
  },
});
</script>

<template>
  <div class="application-general">
    <rd-fieldset
      v-if="isLocaleEnabled"
      data-test="locale"
      :legend-text="t('application.locale.legendText')"
      :is-experimental="true"
      :is-locked="isPreferenceLocked('application.locale')"
    >
      <template #default="{ isLocked }">
        <select
          data-test="localeSelect"
          :value="selectedLocale"
          :disabled="isLocked"
          class="locale-select"
          @change="onLocaleChange"
        >
          <option
            v-for="(label, code) in availableLocales"
            :key="code"
            :value="code"
          >
            {{ label }}
          </option>
        </select>
        <p
          v-if="showLocaleDisclaimer"
          class="locale-disclaimer"
        >
          {{ t('application.locale.disclaimer') }}
        </p>
      </template>
    </rd-fieldset>
    <rd-fieldset
      v-if="!isPlatformWindows"
      data-test="administrativeAccess"
      legend-text="Administrative Access"
      :legend-tooltip="sudoAllowedTooltip"
    >
      <rd-checkbox
        label="Allow to acquire administrative credentials (sudo access)"
        :value="isSudoAllowed"
        :is-locked="isPreferenceLocked('application.adminAccess')"
        @update:value="onChange('application.adminAccess', $event)"
      />
    </rd-fieldset>
    <rd-fieldset
      data-test="automaticUpdates"
      legend-text="Automatic Updates"
    >
      <rd-checkbox
        data-test="automaticUpdatesCheckbox"
        label="Check for updates automatically"
        :value="canAutoUpdate"
        :is-locked="isPreferenceLocked('application.updater.enabled')"
        @update:value="onChange('application.updater.enabled', $event)"
      />
    </rd-fieldset>
    <rd-fieldset
      data-test="statistics"
      legend-text="Statistics"
    >
      <rd-checkbox
        label="Allow collection of anonymous statistics to help us improve Rancher Desktop"
        :value="preferences.application.telemetry.enabled"
        :is-locked="isPreferenceLocked('application.telemetry.enabled')"
        @update:value="onChange('application.telemetry.enabled', $event)"
      />
    </rd-fieldset>
  </div>
</template>

<style lang="scss" scoped>
  .application-general {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .locale-select {
    width: 100%;
    max-width: 300px;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background-color: var(--input-bg);
    color: var(--input-text);
    font-size: 1rem;
  }

  .locale-disclaimer {
    margin-top: 0.5rem;
    font-size: 0.85rem;
    color: var(--muted);
  }
</style>

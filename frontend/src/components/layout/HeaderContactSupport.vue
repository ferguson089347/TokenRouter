<template>
  <div v-if="hasContacts" ref="dropdownRef" class="relative">
    <button
      type="button"
      data-testid="contact-support-toggle"
      class="header-status-icon-button"
      :aria-label="t('common.contactSupport')"
      :aria-expanded="isOpen"
      :title="t('common.contactSupport')"
      @click="toggleDropdown"
    >
      <Icon name="chat" size="md" />
    </button>
    <MotionTransition name="dropdown-fade" persisted>
      <div
        v-show="isOpen"
        class="dropdown right-0 z-modal-nested mt-2 w-72 origin-top-right py-0"
        @keydown.esc.stop="closeDropdown"
      >
        <div class="menu-section">
          <div class="menu-heading">{{ t('common.contactSupport') }}</div>
          <div class="max-h-panel overflow-y-auto p-3">
            <SupportContactList :value="appStore.contactInfo || ''" @activate="closeDropdown" />
          </div>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Icon from '@/components/icons/Icon.vue'
import SupportContactList from '@/components/support/SupportContactList.vue'
import { useAppStore } from '@/stores/app'
import { parseSupportContacts, safeContactUrl } from '@/utils/supportContacts'

// HeaderContactSupport.vue opens the site's labeled support methods from the
// shared header. Text, links, and QR images use the same renderer as the profile.
// The panel remains mounted while closed so a teleported image preview can finish
// its dialog lifecycle after clicks outside the header close the menu.

// Header state tracks menu visibility and the trigger container used to dismiss outside interactions after support methods are configured.
const { t } = useI18n()
const appStore = useAppStore()
const isOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const hasContacts = computed(() =>
  parseSupportContacts(appStore.contactInfo || '').some(
    (entry) => entry.value || safeContactUrl(entry.image, true)
  )
)

/** toggleDropdown changes visibility for the header's support contact panel. */
function toggleDropdown() {
  isOpen.value = !isOpen.value
}

/** closeDropdown hides the support panel after navigation or outside interaction. */
function closeDropdown() {
  isOpen.value = false
}

/** handleClickOutside dismisses support navigation when interaction leaves its header container.
 * The shared contact list retains its mounted image dialog when the menu closes,
 * allowing preview interactions to continue outside the header dropdown itself.
 */
function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) closeDropdown()
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onBeforeUnmount(() => {
  // Remove the global dismissal listener when the header leaves so future document clicks cannot update a retired support menu instance.
  document.removeEventListener('click', handleClickOutside)
})
</script>

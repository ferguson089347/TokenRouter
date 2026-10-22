<template>
  <RuleListEditor
    :items="entries"
    variant="card"
    add-placement="footer"
    :add-label="t('common.supportContacts.add')"
    :empty-text="t('common.supportContacts.empty')"
    :item-label="(index) => t('common.supportContacts.entry', { index: index + 1 })"
    reorderable
    @add="addEntry"
    @remove="removeEntry"
    @move="moveEntry"
  >
    <template #row="{ item }">
      <div class="space-y-4">
        <label class="block"
          ><span class="input-label">{{ t('common.supportContacts.label') }}</span
          ><input
            v-model="item.label"
            class="input"
            :placeholder="t('common.supportContacts.labelPlaceholder')"
            @input="publish"
        /></label>
        <label class="block"
          ><span class="input-label">{{ t('common.supportContacts.value') }}</span
          ><input
            v-model="item.value"
            class="input"
            :placeholder="t('common.supportContacts.valuePlaceholder')"
            @input="publish"
        /></label>
        <div>
          <p class="input-label">{{ t('common.supportContacts.image') }}</p>
          <!-- Bound each embedded upload to five hundred kibibytes so localized settings retain compact QR images alongside contact text. -->
          <ImageUpload
            :model-value="item.image"
            mode="image"
            :max-size="500 * 1024"
            :upload-label="t('common.supportContacts.upload')"
            :hint="t('common.supportContacts.imageHint')"
            @update:model-value="setImage(item, $event)"
          />
          <label class="mt-2 block"
            ><span class="sr-only">{{ t('common.supportContacts.imageUrl') }}</span
            ><input
              :value="item.image.startsWith('data:') ? '' : item.image"
              class="input"
              type="url"
              :placeholder="t('common.supportContacts.imageUrl')"
              @input="setImage(item, ($event.target as HTMLInputElement).value)"
          /></label>
        </div>
      </div>
    </template>
  </RuleListEditor>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import {
  parseSupportContacts,
  serializeSupportContacts,
  type SupportContact,
} from '@/utils/supportContacts'

// SupportContactEditor.vue edits labeled support addresses and optional QR images
// through the site's localized contact string. It reuses the shared row editor
// and image uploader, keeping row identity stable while input updates are emitted
// to the settings page's existing draft and save registry. Old free text first
// becomes individual rows, and subsequent edits use one versioned string that keeps
// image data with the intended row through saving and translation editing.
//
// Parent echoes of the most recently published value leave local row objects
// intact, allowing inputs to retain focus. A different incoming value reloads the
// rows for explicit server reloads or another language's saved content. Structural
// edits create a new array while retaining the remaining contact objects.

// Editor inputs and row state retain the parent's serialized value while local object identities support focus and row movement during editing.
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const entries = ref<SupportContact[]>(parseSupportContacts(props.modelValue))
let lastPublished = props.modelValue
watch(
  () => props.modelValue,
  (value) => {
    if (value === lastPublished) return
    lastPublished = value
    entries.value = parseSupportContacts(value)
  },
)

/** publish emits the current editable rows through the localized string field. */
function publish() {
  lastPublished = serializeSupportContacts(entries.value)
  emit('update:modelValue', lastPublished)
}

/** addEntry appends an empty contact method for administrator editing. */
function addEntry() {
  entries.value = [...entries.value, { label: '', value: '', image: '' }]
  publish()
}

/** removeEntry removes the selected method before publishing the updated draft. */
function removeEntry(index: number) {
  entries.value = entries.value.filter((_, current) => current !== index)
  publish()
}

/** moveEntry changes display order while retaining each contact row's identity. */
function moveEntry(from: number, to: number) {
  const next = [...entries.value]
  const [entry] = next.splice(from, 1)
  next.splice(to, 0, entry)
  entries.value = next
  publish()
}

/** setImage updates one method's uploaded data or external image address. */
function setImage(entry: SupportContact, image: string) {
  entry.image = image
  publish()
}
</script>

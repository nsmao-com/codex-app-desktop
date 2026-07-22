<script setup lang="ts">
import {
  ExternalLink,
  FileWarning,
  Globe2,
  Network,
  ShieldAlert,
  Terminal,
} from '@lucide/vue'
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { PendingServerRequest } from '@/types/codex'
import { asArray, asRecord, asString } from '@/utils/protocol'

interface McpOption {
  label: string
  value: string
}

interface McpField {
  key: string
  label: string
  description: string
  type: string
  format: string
  required: boolean
  multiple: boolean
  options: McpOption[]
  defaultValue: unknown
}

const props = defineProps<{
  request: PendingServerRequest | null
}>()

const emit = defineEmits<{
  resolve: [action: 'once' | 'session' | 'deny' | 'cancel']
  answer: [answers: Record<string, string[]>]
  'mcp-submit': [action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown> | null]
  'open-url': [url: string]
  cancel: []
}>()

const { t } = useI18n()
const answers = shallowRef<Record<string, string>>({})
const mcpAnswers = shallowRef<Record<string, unknown>>({})

const isOpen = computed(() => props.request !== null)
const isUserInput = computed(() => props.request?.method === 'item/tool/requestUserInput')
const isMcpElicitation = computed(() => props.request?.method === 'mcpServer/elicitation/request')
const isPermissions = computed(() => props.request?.method === 'item/permissions/requestApproval')
const isCommand = computed(() =>
  props.request?.method.includes('command') === true
  || props.request?.method === 'execCommandApproval',
)
const isMcpURL = computed(() => isMcpElicitation.value && asString(props.request?.data.mode) === 'url')

watch(() => props.request, (request) => {
  answers.value = {}
  mcpAnswers.value = {}
  if (request?.method === 'mcpServer/elicitation/request') {
    const defaults = Object.fromEntries(
      mcpFields.value
        .filter((field) => field.defaultValue !== undefined)
        .map((field) => [field.key, field.defaultValue]),
    )
    mcpAnswers.value = defaults
  }
}, { immediate: true })

const questions = computed(() => {
  if (!props.request) return []
  return asArray(props.request.data.questions).map((value, index) => {
    const question = asRecord(value)
    return {
      id: asString(question.id, `question-${index}`),
      header: asString(question.header),
      question: asString(question.question),
      // Protocol field is isOther (camelCase); tolerate snake_case payloads.
      isOther: question.isOther === true || question.is_other === true,
      options: asArray(question.options).map((optionValue) => {
        const option = asRecord(optionValue)
        return { label: asString(option.label), description: asString(option.description) }
      }),
    }
  })
})

const command = computed(() => {
  if (!props.request) return ''
  const value = props.request.data.command
  return Array.isArray(value) ? value.map((part) => asString(part)).join(' ') : asString(value)
})

const canSubmitAnswers = computed(() =>
  questions.value.every((question) => answers.value[question.id]?.trim()),
)

const mcpFields = computed<McpField[]>(() => {
  if (!props.request) return []
  const schema = asRecord(props.request.data.requestedSchema)
  const required = new Set(asArray(schema.required).map((value) => asString(value)))
  return Object.entries(asRecord(schema.properties)).map(([key, value]) => {
    const field = asRecord(value)
    const type = asString(field.type, 'string')
    return {
      key,
      label: asString(field.title, key),
      description: asString(field.description),
      type,
      format: asString(field.format),
      required: required.has(key),
      multiple: type === 'array',
      options: schemaOptions(field),
      defaultValue: field.default,
    }
  })
})

const canSubmitMcp = computed(() =>
  mcpFields.value.every((field) => !field.required || hasMcpValue(mcpAnswers.value[field.key])),
)

const mcpURL = computed(() => asString(props.request?.data.url))

const permissionRows = computed(() => {
  if (!props.request) return []
  const permissions = asRecord(props.request.data.permissions)
  const network = asRecord(permissions.network)
  const fileSystem = asRecord(permissions.fileSystem)
  const entries = asArray(fileSystem.entries).map((value) => {
    const entry = asRecord(value)
    const path = asRecord(entry.path)
    const target = asString(path.path, asString(path.pattern, asString(path.value)))
    return `${asString(entry.access)}: ${target}`
  })
  return [
    network.enabled === true ? t('approval.networkAccess') : '',
    ...asArray(fileSystem.read).map((path) => `${t('approval.readAccess')}: ${asString(path)}`),
    ...asArray(fileSystem.write).map((path) => `${t('approval.writeAccess')}: ${asString(path)}`),
    ...entries,
  ].filter(Boolean)
})

const kicker = computed(() => {
  if (isMcpElicitation.value) return t('approval.mcpRequest')
  if (isUserInput.value) return t('approval.hasQuestion')
  return t('approval.required')
})

const title = computed(() => {
  if (isMcpElicitation.value) return t('approval.mcpTitle', { server: asString(props.request?.data.serverName) })
  if (isUserInput.value) return t('approval.decisionNeeded')
  if (isPermissions.value) return t('approval.permissionTitle')
  return isCommand.value ? t('approval.runCommand') : t('approval.allowChange')
})

function onOpenChange(open: boolean): void {
  if (open) return
  if (isMcpElicitation.value) {
    emit('mcp-submit', 'cancel', null)
    return
  }
  if (isUserInput.value) {
    emit('cancel')
    return
  }
  emit('resolve', 'cancel')
}

function setAnswer(id: string, value: string): void {
  answers.value = { ...answers.value, [id]: value }
}

function isPresetOption(questionId: string, label: string): boolean {
  return answers.value[questionId] === label
}

function otherAnswerValue(question: { id: string; options: Array<{ label: string }> }): string {
  const current = answers.value[question.id] ?? ''
  if (!current) return ''
  // Keep free-form text only; hide when a preset option is selected.
  if (question.options.some((option) => option.label === current)) return ''
  return current
}

function setOtherAnswer(questionId: string, value: string | number): void {
  setAnswer(questionId, String(value ?? ''))
}

function submitAnswers(): void {
  if (!canSubmitAnswers.value) return
  const payload = Object.fromEntries(
    questions.value.map((question) => {
      const value = (answers.value[question.id] ?? '').trim()
      return [question.id, [value]]
    }),
  )
  emit('answer', payload)
}

function schemaOptions(schema: Record<string, unknown>): McpOption[] {
  const itemSchema = asRecord(schema.items)
  const direct = asArray(schema.enum).map((value) => ({ label: asString(value), value: asString(value) }))
  if (direct.length) return direct
  const itemOptions = asArray(itemSchema.enum).map((value) => ({ label: asString(value), value: asString(value) }))
  if (itemOptions.length) return itemOptions
  const titled = asArray(schema.oneOf).length ? asArray(schema.oneOf) : asArray(asRecord(itemSchema).anyOf)
  return titled.map((value) => {
    const option = asRecord(value)
    return { label: asString(option.title, asString(option.const)), value: asString(option.const) }
  }).filter((option) => option.value)
}

function hasMcpValue(value: unknown): boolean {
  if (Array.isArray(value)) return value.length > 0
  if (typeof value === 'string') return value.trim().length > 0
  return value !== null && value !== undefined
}

function setMcpAnswer(key: string, value: unknown): void {
  mcpAnswers.value = { ...mcpAnswers.value, [key]: value }
}

function setMcpInput(field: McpField, raw: string | number): void {
  const value = String(raw ?? '')
  if (field.type === 'number' || field.type === 'integer') {
    setMcpAnswer(field.key, value === '' ? '' : Number(value))
    return
  }
  setMcpAnswer(field.key, value)
}

function selectMcpOption(field: McpField, value: string): void {
  if (!field.multiple) {
    setMcpAnswer(field.key, value)
    return
  }
  const current = Array.isArray(mcpAnswers.value[field.key])
    ? [...mcpAnswers.value[field.key] as unknown[]]
    : []
  const index = current.indexOf(value)
  if (index >= 0) current.splice(index, 1)
  else current.push(value)
  setMcpAnswer(field.key, current)
}

function isMcpOptionSelected(field: McpField, value: string): boolean {
  const current = mcpAnswers.value[field.key]
  return Array.isArray(current) ? current.includes(value) : current === value
}

function submitMcp(): void {
  if (!canSubmitMcp.value) return
  const content = Object.fromEntries(Object.entries(mcpAnswers.value).filter(([, value]) => hasMcpValue(value)))
  emit('mcp-submit', 'accept', isMcpURL.value ? null : content)
}

function statusIcon() {
  if (isMcpElicitation.value) return Network
  if (isPermissions.value) return Globe2
  if (isCommand.value) return Terminal
  if (props.request?.method.includes('file') || props.request?.method.includes('Patch')) return FileWarning
  return ShieldAlert
}
</script>

<template>
  <Dialog :open="isOpen" @update:open="onOpenChange">
    <DialogContent class="scrollbar-thin max-h-[88vh] overflow-y-auto rounded-xl p-0 sm:max-w-xl" :show-close-button="!isUserInput">
      <DialogHeader>
        <div class="flex items-start gap-3 border-b px-5 pb-4 pt-5">
          <div class="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <component :is="statusIcon()" :size="18" />
          </div>
          <div class="flex-1">
            <DialogTitle class="text-sm font-semibold leading-tight">{{ title }}</DialogTitle>
            <DialogDescription class="mt-1 text-xs text-muted-foreground">{{ kicker }}</DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <!-- User input -->
      <div v-if="isUserInput" class="space-y-4 px-5">
        <fieldset v-for="question in questions" :key="question.id" class="space-y-2">
          <legend class="text-xs font-medium">
            <span v-if="question.header" class="mr-1 text-muted-foreground">{{ question.header }}</span>
            {{ question.question }}
          </legend>
          <div class="space-y-1.5">
            <Button
              v-for="option in question.options"
              :key="option.label"
              type="button"
              variant="outline"
              class="h-auto w-full justify-start rounded-md px-3 py-2.5 text-left text-xs shadow-none"
              :class="isPresetOption(question.id, option.label) ? 'border-primary bg-primary/5' : ''"
              @click="setAnswer(question.id, option.label)"
            >
              <span class="mr-2 inline-flex size-4 items-center justify-center rounded-full border">
                <span v-if="isPresetOption(question.id, option.label)" class="size-2 rounded-full bg-primary" />
              </span>
              <span class="flex-1">
                <strong class="block">{{ option.label }}</strong>
                <small v-if="option.description" class="block text-[10px] text-muted-foreground">{{ option.description }}</small>
              </span>
            </Button>
            <div v-if="question.isOther || question.options.length === 0" class="space-y-1">
              <Label class="text-[10px] text-muted-foreground">{{ question.options.length ? t('approval.otherAnswer') : t('approval.yourAnswer') }}</Label>
              <Input
                :model-value="otherAnswerValue(question)"
                :placeholder="t('approval.answerPlaceholder')"
                class="h-9 text-xs"
                @update:model-value="setOtherAnswer(question.id, $event)"
              />
            </div>
          </div>
        </fieldset>
      </div>

      <!-- MCP elicitation -->
      <div v-else-if="isMcpElicitation" class="space-y-4 px-5">
        <div class="border-l-2 border-primary bg-muted/40 px-3 py-2.5">
          <div class="flex items-center gap-2 text-xs font-medium">
            <Network :size="14" class="text-primary" />
            {{ asString(request?.data.serverName) }}
          </div>
          <p class="mt-1 text-[11px] text-muted-foreground">{{ asString(request?.data.message) }}</p>
        </div>

        <div v-if="isMcpURL" class="space-y-2">
          <p class="text-[11px] text-muted-foreground">{{ t('approval.mcpURLHint') }}</p>
          <code class="block break-all rounded-md bg-muted p-2 text-[10px]">{{ mcpURL }}</code>
          <Button variant="outline" size="sm" class="text-xs" @click="emit('open-url', mcpURL)">
            <ExternalLink :size="13" class="mr-1.5" />
            {{ t('approval.openInBrowser') }}
          </Button>
        </div>

        <div v-else class="space-y-4">
          <div v-for="field in mcpFields" :key="field.key" class="space-y-1.5">
            <Label class="flex items-center gap-2 text-xs">
              {{ field.label }}
              <Badge v-if="field.required" variant="secondary" class="text-[9px]">{{ t('approval.requiredField') }}</Badge>
            </Label>
            <p v-if="field.description" class="text-[10px] text-muted-foreground">{{ field.description }}</p>

            <div v-if="field.options.length" class="grid gap-1.5 sm:grid-cols-2">
              <Button
                v-for="option in field.options"
                :key="option.value"
                type="button"
                variant="outline"
                size="sm"
                class="h-auto justify-start px-2.5 py-1.5 text-[11px]"
                :class="isMcpOptionSelected(field, option.value) ? 'border-primary bg-primary/5' : ''"
                @click="selectMcpOption(field, option.value)"
              >
                {{ option.label }}
              </Button>
            </div>
            <div v-else-if="field.type === 'boolean'" class="flex gap-2">
              <Button
                v-for="option in [{ label: t('common.yes'), value: true }, { label: t('common.no'), value: false }]"
                :key="String(option.value)"
                type="button"
                variant="outline"
                size="sm"
                class="text-xs"
                :class="mcpAnswers[field.key] === option.value ? 'border-primary bg-primary/5' : ''"
                @click="setMcpAnswer(field.key, option.value)"
              >
                {{ option.label }}
              </Button>
            </div>
            <Input
              v-else
              :type="field.type === 'number' || field.type === 'integer' ? 'number' : field.format === 'email' ? 'email' : field.format === 'date' ? 'date' : 'text'"
              :model-value="String(mcpAnswers[field.key] ?? '')"
              :placeholder="field.label"
              class="h-9 text-xs"
              @update:model-value="setMcpInput(field, $event)"
            />
          </div>
          <p v-if="!mcpFields.length" class="rounded-md bg-muted p-3 text-[11px] text-muted-foreground">
            {{ t('approval.mcpNoFields') }}
          </p>
        </div>
      </div>

      <!-- Permissions / command / patch -->
      <div v-else class="space-y-3 px-5 text-xs">
        <p v-if="request?.data.reason" class="text-muted-foreground">{{ asString(request.data.reason) }}</p>
        <div v-if="command" class="max-h-40 overflow-y-auto rounded-md bg-[#1d1d1b] p-3 font-mono text-[11px] leading-5 text-[#ecece8]">{{ command }}</div>
        <dl class="space-y-1">
          <div v-if="request?.data.cwd" class="flex gap-2">
            <dt class="text-muted-foreground">{{ t('approval.workingDirectory') }}</dt>
            <dd class="font-medium">{{ asString(request.data.cwd) }}</dd>
          </div>
          <div v-if="request?.data.grantRoot" class="flex gap-2">
            <dt class="text-muted-foreground">{{ t('approval.writeAccess') }}</dt>
            <dd class="font-medium">{{ asString(request.data.grantRoot) }}</dd>
          </div>
          <div v-for="row in permissionRows" :key="row" class="flex gap-2">
            <dt class="text-muted-foreground">{{ t('approval.requestedPermission') }}</dt>
            <dd class="font-medium" :title="row">{{ row }}</dd>
          </div>
        </dl>
        <p class="rounded-md bg-muted p-2 text-[11px] text-muted-foreground">{{ t('approval.safetyNote') }}</p>
      </div>

      <DialogFooter class="sticky bottom-0 gap-2 border-t bg-card px-5 py-4 sm:justify-end">
        <template v-if="isUserInput">
          <Button size="sm" :disabled="!canSubmitAnswers" @click="submitAnswers">
            {{ t('approval.sendAnswer') }}
          </Button>
        </template>
        <template v-else-if="isMcpElicitation">
          <Button variant="outline" size="sm" @click="emit('mcp-submit', 'decline', null)">
            {{ t('approval.deny') }}
          </Button>
          <Button size="sm" :disabled="!canSubmitMcp" @click="submitMcp">
            {{ isMcpURL ? t('approval.continue') : t('approval.sendAnswer') }}
          </Button>
        </template>
        <template v-else>
          <Button variant="ghost" size="sm" class="text-destructive" @click="emit('resolve', 'deny')">
            {{ t('approval.deny') }}
          </Button>
          <Button variant="outline" size="sm" @click="emit('resolve', 'session')">
            {{ t('approval.allowSession') }}
          </Button>
          <Button size="sm" @click="emit('resolve', 'once')">
            {{ t('approval.allowOnce') }}
          </Button>
        </template>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

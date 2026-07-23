import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

export type ConfirmDialogOptions = {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
}

export type PromptDialogOptions = {
  title: string
  description?: string
  defaultValue?: string
  placeholder?: string
  confirmLabel?: string
  cancelLabel?: string
  maxlength?: number
}

type ConfirmRequest = ConfirmDialogOptions & {
  kind: 'confirm'
  resolve: (value: boolean) => void
}

type PromptRequest = PromptDialogOptions & {
  kind: 'prompt'
  resolve: (value: string | null) => void
}

type DialogRequest = ConfirmRequest | PromptRequest

export const useDialogStore = defineStore('dialog', () => {
  const request = shallowRef<DialogRequest | null>(null)

  function confirm(options: ConfirmDialogOptions): Promise<boolean> {
    return new Promise((resolve) => {
      // Replace any pending dialog so callers never hang.
      settle(false, null)
      request.value = {
        kind: 'confirm',
        title: options.title,
        description: options.description,
        confirmLabel: options.confirmLabel,
        cancelLabel: options.cancelLabel,
        destructive: Boolean(options.destructive),
        resolve,
      }
    })
  }

  function prompt(options: PromptDialogOptions): Promise<string | null> {
    return new Promise((resolve) => {
      settle(false, null)
      request.value = {
        kind: 'prompt',
        title: options.title,
        description: options.description,
        defaultValue: options.defaultValue ?? '',
        placeholder: options.placeholder,
        confirmLabel: options.confirmLabel,
        cancelLabel: options.cancelLabel,
        maxlength: options.maxlength ?? 200,
        resolve,
      }
    })
  }

  function settle(confirmed: boolean, promptValue: string | null): void {
    const current = request.value
    if (!current) return
    request.value = null
    if (current.kind === 'confirm') current.resolve(confirmed)
    else current.resolve(confirmed ? promptValue : null)
  }

  function cancel(): void {
    settle(false, null)
  }

  function accept(promptValue = ''): void {
    const current = request.value
    if (!current) return
    if (current.kind === 'confirm') {
      settle(true, null)
      return
    }
    settle(true, promptValue)
  }

  return {
    request,
    confirm,
    prompt,
    cancel,
    accept,
  }
})

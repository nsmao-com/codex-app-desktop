import { toast } from 'vue-sonner'

export type ToastTone = 'success' | 'error' | 'warning' | 'info' | 'neutral'

export interface ToastAction {
  label: string
  onClick: () => void
}

export function notify(tone: ToastTone, title: string, message?: string, action?: ToastAction): void {
  const description = message || undefined
  const common = {
    description,
    action: action
      ? {
          label: action.label,
          onClick: action.onClick,
        }
      : undefined,
  }

  switch (tone) {
    case 'success':
      toast.success(title, common)
      break
    case 'error':
      toast.error(title, common)
      break
    case 'warning':
      toast.warning(title, common)
      break
    case 'info':
    case 'neutral':
    default:
      toast.info(title, common)
      break
  }
}

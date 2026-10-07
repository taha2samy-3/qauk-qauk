import { Toaster as Sonner, type ToasterProps } from 'sonner'
import { useTheme } from '@/lib/theme'

export function Toaster(props: ToasterProps) {
  const { resolved } = useTheme()
  return (
    <Sonner
      theme={resolved}
      richColors
      closeButton
      position="bottom-right"
      toastOptions={{ classNames: { toast: 'font-sans' } }}
      {...props}
    />
  )
}

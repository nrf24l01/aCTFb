import type { VariantProps } from 'class-variance-authority'
import { cva } from 'class-variance-authority'

export { default as Toggle } from './Toggle.vue'

/**
 * Neutral palette shared with `Button`: no filled accent, so a pressed control
 * is legible from its `aria-pressed`/`data-state` styling and its label, never
 * from colour alone.
 */
export const toggleVariants = cva(
  'focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive dark:aria-invalid:border-destructive/50 rounded-md border border-transparent bg-transparent text-sm font-medium focus-visible:ring-3 aria-invalid:ring-3 inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap outline-none select-none hover:bg-muted hover:text-foreground data-[state=on]:bg-secondary data-[state=on]:text-secondary-foreground data-[state=on]:border-border disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
  {
    variants: {
      variant: {
        default: 'bg-transparent',
        outline: 'border-border bg-background hover:bg-muted dark:bg-input/30 dark:hover:bg-input/50',
      },
      size: {
        'default': 'h-9 min-w-9 px-2.5',
        'sm': 'h-8 min-w-8 px-2',
        'lg': 'h-10 min-w-10 px-2.5',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)
export type ToggleVariants = VariantProps<typeof toggleVariants>

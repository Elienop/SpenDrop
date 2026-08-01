"use client"

import {
  CircleCheck,
  Info,
  LoaderCircle,
  OctagonX,
  TriangleAlert,
} from "lucide-react"
import { useTheme } from "@/hooks/useTheme"
import { Toaster as Sonner } from "sonner"

type ToasterProps = React.ComponentProps<typeof Sonner>

const toastClassNames = {
  toast:
    "group toast group-[.toaster]:bg-background group-[.toaster]:text-foreground group-[.toaster]:border-border group-[.toaster]:shadow-lg",
  description: "group-[.toast]:text-muted-foreground",
}

// Inline, not classNames, and deliberately so.
//
// sonner styles its buttons with `[data-button]{height:24px;…}` from a
// stylesheet it injects into <head> at runtime. The shadcn default dresses them
// via `classNames.actionButton`, which Tailwind compiles to
// `.group.toast .group-\[\.toast\]\:bg-primary` — three classes against sonner's
// one, so on paper it wins, and the DOM chain it needs really is there (see
// sonner.test.tsx). In the browser it measurably did NOT: the button rendered at
// sonner's 24px in sonner's own colours with our classes sitting on it, unused.
//
// An inline style outranks every stylesheet rule short of !important, and needs
// no ancestor to match, so it cannot lose that argument whatever the real cause
// turns out to be. 40px is the touch-target floor — the toast doubles as a
// swipe-to-dismiss surface, so a missed tap on a failed save dismisses the one
// path that cannot duplicate the entry.
//
// Theme tokens are HSL components (`--primary: 240 5.9% 10%`), hence the hsl()
// wrapper; a bare var() here silently yields no colour.
const actionButtonStyle: React.CSSProperties = {
  height: "2.5rem",
  padding: "0 1rem",
  fontSize: "0.875rem",
  fontWeight: 500,
  borderRadius: "var(--radius)",
  background: "hsl(var(--primary))",
  color: "hsl(var(--primary-foreground))",
}

const cancelButtonStyle: React.CSSProperties = {
  ...actionButtonStyle,
  background: "hsl(var(--muted))",
  color: "hsl(var(--muted-foreground))",
}

const Toaster = ({ toastOptions, ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme()

  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      icons={{
        success: <CircleCheck className="h-4 w-4" />,
        info: <Info className="h-4 w-4" />,
        warning: <TriangleAlert className="h-4 w-4" />,
        error: <OctagonX className="h-4 w-4" />,
        loading: <LoaderCircle className="h-4 w-4 animate-spin" />,
      }}
      {...props}
      // Merged key by key, never replaced: `toastOptions` used to sit before
      // `{...props}`, so one caller passing any option at all — a duration, a
      // single className — silently dropped the touch-target sizing every
      // action toast depends on. A caller's value still wins for the exact key
      // it sets; everything it doesn't mention keeps the shared floor.
      toastOptions={{
        ...toastOptions,
        classNames: { ...toastClassNames, ...toastOptions?.classNames },
        actionButtonStyle: {
          ...actionButtonStyle,
          ...toastOptions?.actionButtonStyle,
        },
        cancelButtonStyle: {
          ...cancelButtonStyle,
          ...toastOptions?.cancelButtonStyle,
        },
      }}
    />
  )
}

export { Toaster }

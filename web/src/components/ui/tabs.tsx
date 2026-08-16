"use client"

import * as React from "react"
import * as TabsPrimitive from "@radix-ui/react-tabs"

import { cn } from "@/lib/utils"

const Tabs = TabsPrimitive.Root

const TabsList = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn(
      // `min-h-10`, a FLOOR, replacing the stock fixed `h-10`. 40px of list
      // minus 8px of padding left
      // every trigger 32px tall — under the 44px touch floor the rest of the
      // phone shell keeps (`MobileNav`, `CategoryChips`, `TransactionEditSheet`)
      // — and a fixed height CLIPS the taller trigger rather than growing with
      // it. `min-h-10` keeps an empty or single-trigger strip from collapsing
      // below the size it used to be.
      "inline-flex min-h-10 items-center justify-center rounded-md bg-muted p-1 text-muted-foreground",
      className
    )}
    {...props}
  />
))
TabsList.displayName = TabsPrimitive.List.displayName

/**
 * TabsList className for a strip that must survive a phone-width viewport:
 * it caps at the container and scrolls sideways rather than pushing the page
 * wider. `justify-start` is load-bearing — the base `justify-center` centres
 * the overflow, which pushes the FIRST trigger past the left edge, where
 * scrollLeft cannot reach it. Where the strip fits (every viewport from `sm`
 * up) none of the three classes has any effect.
 */
const scrollableTabsList = "max-w-full justify-start overflow-x-auto"

const TabsTrigger = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    className={cn(
      // `min-h-11` is the 44px touch target, and it is NOT gated on a
      // breakpoint. Both ends of the range are the reason: the household's
      // tablet is ~720px in portrait, below `md`, so an `md:`-gated floor would
      // miss it — and the same tablet in LANDSCAPE is ~1152px, so an
      // `sm:`-gated one would miss it there. No breakpoint separates "touch"
      // from "pointer", so every consumer gets the floor. Verified in the
      // browser against all five tablists (Reports, Settings, FilterPanel and
      // QuickAdd's two) at 360 and 1440; none of them sized anything off the
      // old 40px.
      "inline-flex min-h-11 items-center justify-center whitespace-nowrap rounded-sm px-3 py-1.5 text-sm font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm",
      className
    )}
    {...props}
  />
))
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName

const TabsContent = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    className={cn(
      "mt-2 ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
      className
    )}
    {...props}
  />
))
TabsContent.displayName = TabsPrimitive.Content.displayName

export { Tabs, TabsList, TabsTrigger, TabsContent, scrollableTabsList }

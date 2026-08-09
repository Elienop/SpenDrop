"use client"

import * as React from "react"
import * as SelectPrimitive from "@radix-ui/react-select"
import { Check, ChevronDown, ChevronUp } from "lucide-react"

import { cn } from "@/lib/utils"

const Select = SelectPrimitive.Root

const SelectGroup = SelectPrimitive.Group

const SelectValue = SelectPrimitive.Value

const SelectTrigger = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger>
>(({ className, children, ...props }, ref) => (
  <SelectPrimitive.Trigger
    ref={ref}
    className={cn(
      "flex h-10 w-full items-center justify-between rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background data-[placeholder]:text-muted-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 [&>span]:line-clamp-1",
      className
    )}
    {...props}
  >
    {children}
    <SelectPrimitive.Icon asChild>
      <ChevronDown className="h-4 w-4 opacity-50" />
    </SelectPrimitive.Icon>
  </SelectPrimitive.Trigger>
))
SelectTrigger.displayName = SelectPrimitive.Trigger.displayName

const SelectScrollUpButton = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.ScrollUpButton>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollUpButton>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.ScrollUpButton
    ref={ref}
    className={cn(
      "flex cursor-default items-center justify-center py-1",
      className
    )}
    {...props}
  >
    <ChevronUp className="h-4 w-4" />
  </SelectPrimitive.ScrollUpButton>
))
SelectScrollUpButton.displayName = SelectPrimitive.ScrollUpButton.displayName

const SelectScrollDownButton = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.ScrollDownButton>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.ScrollDownButton>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.ScrollDownButton
    ref={ref}
    className={cn(
      "flex cursor-default items-center justify-center py-1",
      className
    )}
    {...props}
  >
    <ChevronDown className="h-4 w-4" />
  </SelectPrimitive.ScrollDownButton>
))
SelectScrollDownButton.displayName =
  SelectPrimitive.ScrollDownButton.displayName

const SelectContent = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Content>
>(({ className, children, position = "popper", ...props }, ref) => (
  <SelectPrimitive.Portal>
    {/* `onCloseAutoFocus` is passed straight through (via `props`) and is NOT
        defaulted to `e.preventDefault()`. It used to be, to suppress a focus
        ring after a mouse pick — but Radix composes the consumer handler with
        its own restore using `composeEventHandlers`, which SKIPS the rest on
        `defaultPrevented`. Preventing here therefore cancelled the one call
        that puts focus back on the trigger (`context.trigger?.focus()`), and
        `document.activeElement` landed on `<body>`: measured in Chrome with
        real key events on a stock Select, still `<body>` two seconds later.
        A keyboard or switch user choosing an option was dropped to the top of
        the document.

        The ring that default was avoiding DOES come back, and that is an
        accepted trade rather than a solved problem. Measured in Chrome on a
        fresh page with mouse-only input: after the restore the trigger matches
        `:focus-visible` and paints its 2px ring. `focus-visible:ring-2` does
        not filter it out — Chrome's heuristic treats a PROGRAMMATIC `.focus()`
        as focus-visible regardless of the modality that triggered it, so the
        `focus:` vs `focus-visible:` distinction buys nothing here.

        Kept anyway, for three reasons: a ring after a mouse pick is cosmetic
        while dropping a keyboard user to `<body>` is a barrier; this is what
        stock shadcn/Radix does, so the old default was the local deviation;
        and suppressing a focus indicator the user can genuinely act on runs
        against WCAG 2.4.7. Do not "fix" the ring by restoring the blanket
        preventDefault — that is the focus drop, not a styling choice. */}
    <SelectPrimitive.Content
      ref={ref}
      className={cn(
        // `max-w-[--radix-select-content-available-width]` pairs with the
        // max-height that was already here. Without it the popover took its
        // width from the longest option: a category name at the server's
        // 100-character ceiling produced a 784px menu at a 360px viewport,
        // 434px of it off-screen and unreachable by touch. Radix's own var,
        // not `100vw` — it is measured from the trigger's position and the
        // collision padding, so it stays correct for a menu opened near an
        // edge or inside a Sheet.
        "relative z-50 max-h-[--radix-select-content-available-height] max-w-[--radix-select-content-available-width] min-w-[8rem] overflow-y-auto overflow-x-hidden rounded-md border bg-popover text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 origin-[--radix-select-content-transform-origin]",
        position === "popper" &&
          "data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1",
        className
      )}
      position={position}
      {...props}
    >
      <SelectScrollUpButton />
      <SelectPrimitive.Viewport
        className={cn(
          "p-1",
          position === "popper" &&
            "h-[var(--radix-select-trigger-height)] w-full min-w-[var(--radix-select-trigger-width)]"
        )}
      >
        {children}
      </SelectPrimitive.Viewport>
      <SelectScrollDownButton />
    </SelectPrimitive.Content>
  </SelectPrimitive.Portal>
))
SelectContent.displayName = SelectPrimitive.Content.displayName

const SelectLabel = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Label>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Label>
>(({ className, ...props }, ref) => (
  // Left at `py-1.5` while `SelectItem` takes a 44px floor. A label is not a
  // tap target, and staying shorter than the options is what makes it read as
  // a group heading rather than a row you could have picked. (No call site in
  // this app renders one today; every `SelectGroup` here is unlabelled.)
  <SelectPrimitive.Label
    ref={ref}
    className={cn("py-1.5 pl-8 pr-2 text-sm font-semibold", className)}
    {...props}
  />
))
SelectLabel.displayName = SelectPrimitive.Label.displayName

const SelectItem = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Item>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Item>
>(({ className, children, ...props }, ref) => (
  // `min-h-11` (44px) is the touch floor, and it lives here rather than at the
  // call sites: the option is the element the user actually taps to CHOOSE,
  // and stock `py-1.5` around `text-sm` measured 32px in Chrome at 360px. A
  // floor on the trigger you open and not on the row you then hit is no floor
  // at all. Deliberately NOT gated behind `md:` (the idiom `PaginationBar`
  // uses on its trigger): a touch tablet held in landscape is above `md` and
  // would keep the 32px rows. A floor, not a height — a two-line option still
  // grows past 44px.
  <SelectPrimitive.Item
    ref={ref}
    className={cn(
      // `[overflow-wrap:anywhere]` is the other half of the content's
      // max-width: once the popover stops growing to fit the longest option,
      // `overflow-x-hidden` would CLIP a long name instead of showing it.
      // `anywhere` (not `break-words`) because it also shrinks the intrinsic
      // min-content size, which is what the flex automatic minimum floors the
      // text at — the same reason the category chips use it. `min-h-11` is a
      // floor, so a wrapped option grows instead of clipping.
      //
      // NO `line-clamp-*` here, and that is a deliberate deviation from the
      // table-cell recipe (which pairs the wrap with a clamp, enforced by the
      // seam test). A table cell is a fixed-height row where unbounded vertical
      // growth is the defect; an option is a control you must read in full to
      // pick correctly, and two categories sharing a long prefix become
      // indistinguishable the moment the tail is clamped away. The vertical
      // growth is contained anyway — the viewport scrolls and the content has a
      // max-height — so the trade the clamp exists to make does not apply.
      "relative flex min-h-11 w-full cursor-default select-none items-center rounded-sm py-1.5 pl-8 pr-2 text-sm outline-none [overflow-wrap:anywhere] focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
      className
    )}
    {...props}
  >
    <span className="absolute left-2 flex h-3.5 w-3.5 items-center justify-center">
      <SelectPrimitive.ItemIndicator>
        <Check className="h-4 w-4" />
      </SelectPrimitive.ItemIndicator>
    </span>

    <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
  </SelectPrimitive.Item>
))
SelectItem.displayName = SelectPrimitive.Item.displayName

const SelectSeparator = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Separator>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Separator>
>(({ className, ...props }, ref) => (
  <SelectPrimitive.Separator
    ref={ref}
    className={cn("-mx-1 my-1 h-px bg-muted", className)}
    {...props}
  />
))
SelectSeparator.displayName = SelectPrimitive.Separator.displayName

export {
  Select,
  SelectGroup,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectLabel,
  SelectItem,
  SelectSeparator,
  SelectScrollUpButton,
  SelectScrollDownButton,
}

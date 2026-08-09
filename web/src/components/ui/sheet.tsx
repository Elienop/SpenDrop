import * as React from "react"
import * as SheetPrimitive from "@radix-ui/react-dialog"
import { cva, type VariantProps } from "class-variance-authority"
import { X } from "lucide-react"

import { cn } from "@/lib/utils"

const Sheet = SheetPrimitive.Root

const SheetTrigger = SheetPrimitive.Trigger

const SheetClose = SheetPrimitive.Close

const SheetPortal = SheetPrimitive.Portal

const SheetOverlay = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof SheetPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <SheetPrimitive.Overlay
    className={cn(
      "fixed inset-0 z-50 bg-black/80  data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
      className
    )}
    {...props}
    ref={ref}
  />
))
SheetOverlay.displayName = SheetPrimitive.Overlay.displayName

const sheetVariants = cva(
  "fixed z-50 flex max-h-[100dvh] flex-col gap-4 bg-background p-6 shadow-lg transition ease-in-out data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:duration-300 data-[state=open]:duration-500",
  {
    variants: {
      side: {
        top: "inset-x-0 top-0 border-b data-[state=closed]:slide-out-to-top data-[state=open]:slide-in-from-top",
        bottom:
          "inset-x-0 bottom-0 border-t data-[state=closed]:slide-out-to-bottom data-[state=open]:slide-in-from-bottom",
        left: "inset-y-0 left-0 h-full w-3/4 border-r data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left sm:max-w-sm",
        right:
          "inset-y-0 right-0 h-full w-3/4  border-l data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right sm:max-w-sm",
      },
    },
    defaultVariants: {
      side: "right",
    },
  }
)

interface SheetContentProps
  extends React.ComponentPropsWithoutRef<typeof SheetPrimitive.Content>,
    VariantProps<typeof sheetVariants> {
  /**
   * Rendered ABOVE the body scroller instead of inside it, so it stays put
   * while the body scrolls. Opt-in: omit it and the DOM is exactly what it
   * was — `{undefined}` renders no node, so no extra flex child and no extra
   * `gap-4`.
   *
   * For a sheet whose title names WHICH thing you are looking at, or that
   * carries a summary figure, this is not polish. Measured on the heatmap's
   * day sheet at 360px with 20 rows: with the header inside the scroller the
   * "$290.30 spent" total left the box after 60px of scroll, with 8 of the 20
   * rows still on screen — so from row 9 on, the sheet explaining one day
   * showed neither the day nor its total.
   *
   * FOUR CONSUMERS STILL PASS `SheetHeader` AS A CHILD, and that was assessed,
   * not overlooked: `TransactionEditSheet`, `MobileNav`, `Categories` and the
   * Transactions filter panel. Measured at 360/390/720, TransactionEditSheet's
   * form does not overflow (`scrollHeight === clientHeight === 603`,
   * `maxScroll: 0`), so its header never scrolls away — the mechanism is
   * shared but the defect is latent there, and migrating it would have been an
   * unverifiable change with no before/after to show. `MobileNav` sets
   * `p-0 gap-0` and owns its own layout. The other two were not measured.
   *
   * So the repo holds a better idiom used once and an older one used four
   * times. This note is which is which, so the next reader does not have to
   * guess whether the single user is the exception or the direction.
   */
  header?: React.ReactNode
}

const SheetContent = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Content>,
  SheetContentProps
>(({ side = "right", className, header, children, ...props }, ref) => (
  <SheetPortal>
    <SheetOverlay />
    <SheetPrimitive.Content
      ref={ref}
      className={cn(sheetVariants({ side }), className)}
      {...props}
    >
      {header}
      {/* Content is a flex column (`flex flex-col` in sheetVariants) and this
          is the only in-flow child that can grow — that pairing is what bounds
          the scroll. As a block child of a fixed-height sheet this div would
          instead size to its content and spill past the sheet's edge,
          unreachable, with `overflow-y-auto` never engaging. It needs no
          `flex-1` to shrink, because `overflow-y-auto` already makes its
          automatic minimum size 0 — and `flex-1` would be wrong: a zero
          flex-basis collapses an auto-height top/bottom sheet.

          `header` above adds a SECOND in-flow child, and the same property is
          what makes that safe: the header's automatic minimum size is its own
          content, so it cannot shrink, while this div's is 0 — every pixel of
          overflow is therefore taken out of the scroller and none out of the
          header. Do not give the header `flex-1` or a `min-h-0` either; both
          would let it start absorbing the shrink instead.

          Scrolling the body rather than the whole sheet is also what keeps the
          Close button below, absolutely positioned against Content, pinned.
          The div stays display:block so the sections inside it stack in
          ordinary flow, and `-m-1 p-1` keeps focus rings on edge children out
          of the clipping box. */}
      <div className="-m-1 overflow-y-auto p-1">{children}</div>
      {/* `-m-3.5 p-3.5` grows the 16px icon to a 44px touch target without
          moving it: the negative margin pulls the border box back out by the
          same 14px the padding adds, so the icon still sits 16px from the top
          and right. The two tokens must stay equal or the icon shifts. */}
      <SheetPrimitive.Close className="absolute right-4 top-4 -m-3.5 rounded-sm p-3.5 opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none data-[state=open]:bg-secondary">
        <X className="h-4 w-4" />
        <span className="sr-only">Close</span>
      </SheetPrimitive.Close>
    </SheetPrimitive.Content>
  </SheetPortal>
))
SheetContent.displayName = SheetPrimitive.Content.displayName

const SheetHeader = ({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      "flex flex-col space-y-2 text-center sm:text-left",
      className
    )}
    {...props}
  />
)
SheetHeader.displayName = "SheetHeader"

const SheetFooter = ({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      "flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2",
      className
    )}
    {...props}
  />
)
SheetFooter.displayName = "SheetFooter"

const SheetTitle = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof SheetPrimitive.Title>
>(({ className, ...props }, ref) => (
  <SheetPrimitive.Title
    ref={ref}
    className={cn("text-lg font-semibold text-foreground", className)}
    {...props}
  />
))
SheetTitle.displayName = SheetPrimitive.Title.displayName

const SheetDescription = React.forwardRef<
  React.ElementRef<typeof SheetPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof SheetPrimitive.Description>
>(({ className, ...props }, ref) => (
  <SheetPrimitive.Description
    ref={ref}
    className={cn("text-sm text-muted-foreground", className)}
    {...props}
  />
))
SheetDescription.displayName = SheetPrimitive.Description.displayName

export {
  Sheet,
  SheetPortal,
  SheetOverlay,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
}

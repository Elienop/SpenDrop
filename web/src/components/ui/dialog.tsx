"use client"

import * as React from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { X } from "lucide-react"

import { cn } from "@/lib/utils"

const Dialog = DialogPrimitive.Root

const DialogTrigger = DialogPrimitive.Trigger

const DialogPortal = DialogPrimitive.Portal

const DialogClose = DialogPrimitive.Close

const DialogOverlay = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      "fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
      className
    )}
    {...props}
  />
))
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName

/**
 * `showCloseButton` opts a dialog out of the built-in X.
 *
 * Default `true`, so all 11 pre-existing production call sites are
 * byte-unchanged. It exists
 * for the one dialog that refuses every close except its own button — the
 * API-token reveal, which holds a plaintext secret the server will never
 * re-issue — where the X rendered, took focus, and did nothing. A control that
 * looks operable and is not is worse than no control.
 *
 * Named after the prop upstream shadcn added for the same purpose in its
 * newer registry style, rather than an inverted `hideClose`, so that whenever
 * this project moves off the `default`/Tailwind-v3 style the two converge
 * instead of conflicting.
 *
 * A CSS-only hide was the alternative and was rejected on testability: no
 * stylesheet is loaded under happy-dom, so a `hidden` utility would leave the
 * button in the accessibility tree in every test while appearing to work in
 * the browser — unverifiable in exactly the environment that has to prove it.
 */
const DialogContent = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & {
    showCloseButton?: boolean
  }
>(({ className, children, showCloseButton = true, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    {/* `grid-rows-[minmax(0,1fr)]` is what lets the scroll wrapper below
        ever engage. Left implicit, the single `auto` row sizes to the
        content's natural height and Chromium KEEPS that row size when
        `max-h` clamps the box (measured at 411x500: box clamped to 468px,
        row still 487px) — so the wrapper is laid out taller than the
        dialog, never overflows internally, and everything past the border
        just spills off-screen, unreachable. An explicit `minmax(0,1fr)`
        row shrinks to the clamped box instead; while the box is unclamped
        the fr still resolves to content height, so short dialogs are
        unchanged. */}
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "fixed left-[50%] top-[50%] z-50 grid max-h-[calc(100dvh-2rem)] w-full max-w-lg translate-x-[-50%] translate-y-[-50%] grid-rows-[minmax(0,1fr)] gap-4 border bg-background p-6 shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg",
        className
      )}
      {...props}
    >
      {/* The scroll lives on this wrapper rather than on Content so the Close
          button below — absolutely positioned against Content — stays pinned
          instead of scrolling out of reach. It carries Content's own `grid
          gap-4` so spacing between header, body and footer is unchanged.
          `-m-1 p-1` widens the clipping box by the 4px a focus ring occupies,
          so rings on full-width children are not sliced off at the edges. */}
      <div className="-m-1 grid min-h-0 gap-4 overflow-y-auto p-1">
        {children}
      </div>
      {/* `-m-3.5 p-3.5` grows the 16px icon to a 44px touch target without
          moving it: the negative margin pulls the border box back out by the
          same 14px the padding adds, so the icon still sits 16px from the top
          and right. The two tokens must stay equal or the icon shifts. */}
      {showCloseButton && (
        <DialogPrimitive.Close className="absolute right-4 top-4 -m-3.5 rounded-sm p-3.5 opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none data-[state=open]:bg-accent data-[state=open]:text-muted-foreground">
          <X className="h-4 w-4" />
          <span className="sr-only">Close</span>
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </DialogPortal>
))
DialogContent.displayName = DialogPrimitive.Content.displayName

const DialogHeader = ({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      "flex flex-col space-y-1.5 text-center sm:text-left",
      className
    )}
    {...props}
  />
)
DialogHeader.displayName = "DialogHeader"

const DialogFooter = ({
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
DialogFooter.displayName = "DialogFooter"

const DialogTitle = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn(
      "text-lg font-semibold leading-none tracking-tight",
      className
    )}
    {...props}
  />
))
DialogTitle.displayName = DialogPrimitive.Title.displayName

const DialogDescription = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn("text-sm text-muted-foreground", className)}
    {...props}
  />
))
DialogDescription.displayName = DialogPrimitive.Description.displayName

export {
  Dialog,
  DialogPortal,
  DialogOverlay,
  DialogClose,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
}

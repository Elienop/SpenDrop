import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

// `coarse:min-h-11` is the 44px touch floor, on the same POINTER gate as
// `SelectTrigger`'s. The full argument for each part of that token is written
// out in `components/ui/select.tsx`; in short: `coarse:` rather than `md:`
// because the household's tablet is ~1130px in landscape, so a width gate hands
// a touch screen the 32-40px desktop sizes (measured: 115 of 132 visible
// buttons under 44px on /transactions at that width); `min-h` rather than `h`
// because the two are separate tailwind-merge conflict groups, so the floor
// survives every call site's `h-8`/`h-9` and CSS clamps the used height up;
// gated rather than unconditional because `twMerge` keeps both classes, so an
// ungated floor would inflate every dense button on a mouse desktop, which the
// owner explicitly ruled out.
//
// On the BASE rather than per-size, so a size added later inherits it — a
// variant that means to sit below the floor has to say so, rather than getting
// there by omission. The consequence, accepted deliberately: on a coarse
// pointer `sm` (36px) and `default` (40px) both land on 44px and stop differing
// in height. Only their padding (`px-3` vs `px-4`) still separates them, which
// is the right outcome for a floor — something below 44px is not a smaller
// button, it is a missed tap.
//
// To opt out, a call site passes `coarse:min-h-0` (and `coarse:min-w-0` for an
// icon): same group AND same modifier, so tailwind-merge lets it win, where a
// bare `min-h-0` would merely sit beside the gated one at equal specificity and
// let stylesheet order decide. `PasswordInput` is the one place that opts out,
// and says why at the site.
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors coarse:min-h-11 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        destructive:
          "bg-destructive text-destructive-foreground hover:bg-destructive/90",
        outline:
          "border border-input bg-background hover:bg-accent hover:text-accent-foreground",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-secondary/80",
        ghost: "hover:bg-accent hover:text-accent-foreground",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "h-10 px-4 py-2",
        sm: "h-9 rounded-md px-3",
        lg: "h-11 rounded-md px-8",
        // BOTH axes, and the width half only exists here. A square target
        // floored in height alone becomes a 44x32 rectangle — taller, still a
        // miss for a thumb. `size-*` belongs to the `h`/`w` groups, so a call
        // site's `size-8` replaces the 40px box above and leaves this standing.
        //
        // A button that is square by className rather than by `size="icon"`
        // (Sidebar's collapsed Log out) gets only the base height floor, so it
        // adds `TOUCH_TARGET_SQUARE` itself.
        icon: "h-10 w-10 coarse:min-w-11",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    )
  }
)
Button.displayName = "Button"

export { Button, buttonVariants }

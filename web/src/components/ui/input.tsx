import * as React from "react"

import { cn } from "@/lib/utils"

// `coarse:min-h-11` is the same 44px touch floor Button and SelectTrigger
// carry, and the same three-way argument applies (written out in full in
// `components/ui/select.tsx`): `coarse:` not `md:` because the household's
// tablet is above `md` and still a touch screen; `min-h` not `h` because the
// two are separate tailwind-merge conflict groups, so the floor survives a
// call site's own height and CSS clamps the used height up; gated rather than
// unconditional because desktop form density is a stated owner constraint.
// The base `h-10` (40px) stands at every one of the ~48 call sites, so on a
// coarse pointer every field was 4px under the floor.
//
// PasswordInput's eye button is unaffected: it opts ITSELF out of Button's
// floor (`coarse:min-h-0`) and centers with `top-1/2 -translate-y-1/2`, so it
// tracks the input's midline whether the field renders 40px or 44px.
const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          "flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-base ring-offset-background coarse:min-h-11 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
          className
        )}
        ref={ref}
        {...props}
      />
    )
  }
)
Input.displayName = "Input"

export { Input }

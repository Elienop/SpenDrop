import * as React from "react"
import * as SwitchPrimitives from "@radix-ui/react-switch"

import { cn } from "@/lib/utils"

// The touch target is the "grow only the hit area" lever from
// `@/lib/touch-target`, not a `coarse:min-h-11` floor: the 24px pill IS the
// control's visual grammar — the thumb's `translate-x-5` travel is derived
// from the `h-6 w-11` box, and growing the box on coarse pointers would both
// stretch the pill and misalign it against the `items-center` label rows it
// sits in. So the box stays 24px everywhere and a coarse-gated pseudo-element
// carries the target instead. `-inset-y-3`, not `-inset-y-2.5`: an absolute
// pseudo resolves its insets against the PADDING box, and `border-2` shrinks
// that to 20px — 10px insets measured a 40px band on the built app (probed
// 2026-08-14, elementFromPoint); 20 + 2x12 = 44. The extensions reach 10px
// past the border box on each side, so a stack of these needs a pitch OVER
// 44px: at `coarse:gap-5` two bands tile exactly (24 + 20 = 44) and the
// shared boundary resolves by paint order — the B45 fix moved Settings'
// notification stack to `coarse:gap-6` for 4px of real clearance. `w-11` is already
// 44 so `inset-x-0` just spans the box — without it left/right stay `auto`
// and an empty absolute pseudo collapses to zero width, a sliver that
// catches no tap at all. The
// pseudo is part of the root button's box, so a tap on it hits the switch —
// the same mechanism PasswordInput's eye and `TOUCH_TARGET_CHECKBOX` already
// use. The inset is derived from THIS control's 20px padding box; it is not
// a shareable constant. The rendered pixels are browser-measured; the class
// arithmetic (band >= 44px, positive seam clearance) is pinned in
// Settings.notifications.test.tsx.
const Switch = React.forwardRef<
  React.ElementRef<typeof SwitchPrimitives.Root>,
  React.ComponentPropsWithoutRef<typeof SwitchPrimitives.Root>
>(({ className, ...props }, ref) => (
  <SwitchPrimitives.Root
    className={cn(
      "peer relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors coarse:before:absolute coarse:before:inset-x-0 coarse:before:-inset-y-3 coarse:before:content-[''] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input",
      className
    )}
    {...props}
    ref={ref}
  >
    <SwitchPrimitives.Thumb
      className={cn(
        "pointer-events-none block h-5 w-5 rounded-full bg-background shadow-lg ring-0 transition-transform data-[state=checked]:translate-x-5 data-[state=unchecked]:translate-x-0"
      )}
    />
  </SwitchPrimitives.Root>
))
Switch.displayName = SwitchPrimitives.Root.displayName

export { Switch }

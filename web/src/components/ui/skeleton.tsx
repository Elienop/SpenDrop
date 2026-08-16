import { cn } from "@/lib/utils"

// `Readonly<…>` wraps the parameter only (SonarQube S6759); the accepted prop
// set is still exactly `React.HTMLAttributes<HTMLDivElement>`, since readonly
// modifiers do not participate in assignability.
function Skeleton({
  className,
  ...props
}: Readonly<React.HTMLAttributes<HTMLDivElement>>) {
  return (
    <div
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  )
}

export { Skeleton }

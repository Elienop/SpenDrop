import * as React from "react"
import * as RechartsPrimitive from "recharts"
import type { TooltipValueType } from "recharts"

import { cn } from "@/lib/utils"

// recharts 3 reads `payload` and `label` from context rather than passing them
// as props, so `ComponentProps<typeof Tooltip>` no longer carries them (they
// live behind the library's internal `PropertiesReadFromContext`). The default
// content components still declare them, so we intersect their prop types back
// in. `accessibilityLayer` is omitted because it collides with the chart-level
// prop of the same name.
//
// recharts does not export the tooltip's name type, and it is only ever a
// series name here, so it is restated locally rather than reached for through
// the library's internals.
type TooltipNameType = number | string

// Format: { THEME_NAME: CSS_SELECTOR }
const THEMES = { light: "", dark: ".dark" } as const

export type ChartConfig = {
  [k in string]: {
    label?: React.ReactNode
    icon?: React.ComponentType
  } & (
    | { color?: string; theme?: never }
    | { color?: never; theme: Record<keyof typeof THEMES, string> }
  )
}

type ChartContextProps = {
  config: ChartConfig
}

const ChartContext = React.createContext<ChartContextProps | null>(null)

function useChart() {
  const context = React.useContext(ChartContext)

  if (!context) {
    throw new Error("useChart must be used within a <ChartContainer />")
  }

  return context
}

const ChartContainer = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> & {
    config: ChartConfig
    children: React.ComponentProps<
      typeof RechartsPrimitive.ResponsiveContainer
    >["children"]
  }
>(({ id, className, children, config, ...props }, ref) => {
  const uniqueId = React.useId()
  const chartId = `chart-${id || uniqueId.replaceAll(":", "")}`

  // Memoised so the provider's value identity only changes when `config` does.
  // Every consumer reads `config` and nothing else, so `config` is the whole
  // dependency list — a wider one would just reintroduce the per-render churn.
  const chartContextValue = React.useMemo(() => ({ config }), [config])

  return (
    <ChartContext.Provider value={chartContextValue}>
      <div
        data-chart={chartId}
        ref={ref}
        className={cn(
          "flex aspect-video justify-center text-xs [&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground [&_.recharts-cartesian-grid_line[stroke='#ccc']]:stroke-border/50 [&_.recharts-curve.recharts-tooltip-cursor]:stroke-border [&_.recharts-dot[stroke='#fff']]:stroke-transparent [&_.recharts-layer]:outline-none [&_.recharts-polar-grid_[stroke='#ccc']]:stroke-border [&_.recharts-radial-bar-background-sector]:fill-muted [&_.recharts-rectangle.recharts-tooltip-cursor]:fill-muted [&_.recharts-reference-line_[stroke='#ccc']]:stroke-border [&_.recharts-sector[stroke='#fff']]:stroke-transparent [&_.recharts-sector]:outline-none [&_.recharts-surface]:outline-none",
          className
        )}
        {...props}
      >
        <ChartStyle id={chartId} config={config} />
        <RechartsPrimitive.ResponsiveContainer>
          {children}
        </RechartsPrimitive.ResponsiveContainer>
      </div>
    </ChartContext.Provider>
  )
})
ChartContainer.displayName = "Chart"

const ChartStyle = ({ id, config }: { id: string; config: ChartConfig }) => {
  const colorConfig = Object.entries(config).filter(
    ([, config]) => config.theme || config.color
  )

  if (!colorConfig.length) {
    return null
  }

  return (
    <style
      dangerouslySetInnerHTML={{
        __html: Object.entries(THEMES)
          .map(
            ([theme, prefix]) => `
${prefix} [data-chart=${id}] {
${colorConfig
  .map(([key, itemConfig]) => {
    const color =
      itemConfig.theme?.[theme as keyof typeof itemConfig.theme] ||
      itemConfig.color
    return color ? `  --color-${key}: ${color};` : null
  })
  .join("\n")}
}
`
          )
          .join("\n"),
      }}
    />
  )
}

const ChartTooltip = RechartsPrimitive.Tooltip

const ChartTooltipContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<typeof RechartsPrimitive.Tooltip> &
    Omit<
      RechartsPrimitive.DefaultTooltipContentProps<
        TooltipValueType,
        TooltipNameType
      >,
      "accessibilityLayer"
    > &
    React.ComponentProps<"div"> & {
      hideLabel?: boolean
      hideIndicator?: boolean
      indicator?: "line" | "dot" | "dashed"
      nameKey?: string
      labelKey?: string
    }
>(
  (
    {
      active,
      payload,
      className,
      indicator = "dot",
      hideLabel = false,
      hideIndicator = false,
      label,
      labelFormatter,
      labelClassName,
      formatter,
      color,
      nameKey,
      labelKey,
    },
    ref
  ) => {
    const { config } = useChart()

    const tooltipLabel = React.useMemo(() => {
      if (hideLabel || !payload?.length) {
        return null
      }

      const [item] = payload
      const key = `${labelKey || item?.dataKey || item?.name || "value"}`
      const itemConfig = getPayloadConfigFromPayload(config, item, key)
      const value =
        !labelKey && typeof label === "string"
          ? config[label as keyof typeof config]?.label || label
          : itemConfig?.label

      if (labelFormatter) {
        return (
          <div className={cn("font-medium", labelClassName)}>
            {labelFormatter(value, payload)}
          </div>
        )
      }

      if (!value) {
        return null
      }

      return <div className={cn("font-medium", labelClassName)}>{value}</div>
    }, [
      label,
      labelFormatter,
      payload,
      hideLabel,
      labelClassName,
      config,
      labelKey,
    ])

    if (!active || !payload?.length) {
      return null
    }

    const nestLabel = payload.length === 1 && indicator !== "dot"

    return (
      <div
        ref={ref}
        className={cn(
          "grid min-w-[12rem] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl",
          className
        )}
      >
        {!nestLabel ? tooltipLabel : null}
        <div className="grid gap-1.5">
          {payload
            .filter((item) => item.type !== "none")
            .map((item, index) => {
              const key = `${nameKey || item.name || item.dataKey || "value"}`
              const itemConfig = getPayloadConfigFromPayload(config, item, key)
              const indicatorColor = color || item.payload.fill || item.color

              return (
                // recharts 3 widened `dataKey` to include a function type,
                // which is not a valid React key. The index is stable here:
                // this list is a single tooltip's payload, rendered in place
                // and never reordered.
                //
                // Deliberate: SonarQube S6479 ("do not use array index in
                // keys") is ACCEPTED on this line. A payload item carries no
                // identity that is both stable and unique — `name` is shared by
                // design (see the "two tooltip rows sharing a name" test in
                // chart.test.tsx, which fails on React's duplicate-key warning
                // if this line ever keys by it) and `dataKey` may be a function, whose
                // stringification two series can also share. A composite of
                // those can collide, and a colliding key is strictly worse than
                // the index: React conflates the two rows. Position in the
                // payload IS the identity recharts gives us.
                <div
                  key={index}
                  className={cn(
                    "flex w-full items-stretch gap-2 [&>svg]:h-2.5 [&>svg]:w-2.5 [&>svg]:text-muted-foreground",
                    indicator === "dot" && "items-center"
                  )}
                >
                  {formatter && item?.value !== undefined && item.name ? (
                    formatter(item.value, item.name, item, index, item.payload)
                  ) : (
                    <div
                      className={cn(
                        "flex flex-1 justify-between gap-4 leading-none",
                        nestLabel ? "items-end" : "items-center"
                      )}
                    >
                      <div className="flex items-center gap-1.5">
                        {nestLabel ? tooltipLabel : null}
                        {itemConfig?.icon ? (
                          <itemConfig.icon />
                        ) : (
                          !hideIndicator && (
                            <div
                              className={cn(
                                "shrink-0 rounded-[2px] border-[--color-border] bg-[--color-bg]",
                                {
                                  "h-2.5 w-2.5": indicator === "dot",
                                  "w-1": indicator === "line",
                                  "w-0 border-[1.5px] border-dashed bg-transparent":
                                    indicator === "dashed",
                                  "my-0.5": nestLabel && indicator === "dashed",
                                }
                              )}
                              style={
                                {
                                  "--color-bg": indicatorColor,
                                  "--color-border": indicatorColor,
                                } as React.CSSProperties
                              }
                            />
                          )
                        )}
                        <span className="text-muted-foreground">
                          {itemConfig?.label || item.name}
                        </span>
                      </div>
                      {item.value != null && (
                        <span className="font-mono font-medium tabular-nums text-foreground">
                          {typeof item.value === "number"
                            ? item.value.toLocaleString(undefined, {
                                minimumFractionDigits: 2,
                                maximumFractionDigits: 2,
                              })
                            : item.value.toLocaleString()}
                        </span>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
        </div>
      </div>
    )
  }
)
ChartTooltipContent.displayName = "ChartTooltip"

const ChartLegend = RechartsPrimitive.Legend

// The `position` values that put the legend against the TOP of the box it is
// placed in, so the gap separating it from the plot has to sit UNDER it.
// `"top"` is the outside placement above the plot area; the three `insideTop*`
// values anchor to the top edge with `verticalAnchor: "start"`, i.e. the legend
// hangs downwards from there (recharts' `getCartesianPosition`). Every other
// value — `"bottom"`, the `insideBottom*` trio, the left/right pair and
// `"center"` — sits below or beside the plot. The `{ x, y }` object form is not
// a string, so it never matches here and lands on the default arm; there is no
// edge it is anchored to that could be read off it.
const TOP_ANCHORED_LEGEND_POSITIONS: ReadonlyArray<string> = [
  "top",
  "insideTop",
  "insideTopLeft",
  "insideTopRight",
]

const ChartLegendContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> &
    // recharts 3 dropped `payload` from `LegendProps` (`Legend.d.ts` omits it
    // and never adds it back), so the upstream
    // `Pick<LegendProps, "payload" | "verticalAlign">` no longer type-checks —
    // and while broken it made `payload` a REQUIRED prop at every
    // `<ChartLegendContent />` call site. Intersecting the default legend
    // content's own props restores `payload` as an optional field, which is
    // what the call sites in SpendingTab and SavingsTab rely on.
    Omit<RechartsPrimitive.DefaultLegendContentProps, "verticalAlign"> & {
      hideIcon?: boolean
      nameKey?: string
      // Both placement props are restated locally, for two different reasons.
      //
      // `position` is declared on `<Legend>` rather than on
      // `DefaultLegendContentProps`, so it does not arrive with the
      // intersection at all.
      //
      // `verticalAlign` does — but recharts 3.10 deprecated it, and INHERITING
      // that declaration is what makes reading the prop below report
      // "'verticalAlign' is deprecated" (SonarQube typescript:S1874 reads
      // TypeScript's own deprecation suggestions, and it fired on the
      // destructure). It is omitted above and restated here so this component
      // can keep honouring the prop without re-raising a finding this branch
      // exists to clear. A CALL SITE that sets it still gets its own warning
      // from `<Legend>`, which is where it belongs. The union is written out
      // rather than imported: recharts does not re-export
      // `VerticalAlignmentType` from its root, and reaching into
      // `recharts/types/…` for it would be worse than restating three literals.
      //
      // Deprecated is not removed: `legendDefaultProps` still defaults
      // `verticalAlign` to "bottom" and `getDefaultPosition` still branches on
      // it, so a call site setting it really does still move the legend.
      // `<Legend>` spreads its resolved props straight onto the `content`
      // element (Legend.js `LegendContent`), so both props arrive here —
      // `verticalAlign` already resolved to its default, `position` as
      // `undefined` when the call site leaves it unset.
      position?: RechartsPrimitive.CartesianPosition
      verticalAlign?: "top" | "middle" | "bottom"
    }
>(
  (
    {
      className,
      hideIcon = false,
      payload,
      position,
      verticalAlign = "bottom",
      nameKey,
    },
    ref
  ) => {
    const { config } = useChart()

    if (!payload?.length) {
      return null
    }

    // Which edge the legend is pinned to, and therefore which side of it faces
    // the plot. Both placement props are read, with `position` winning whenever
    // it is set — the precedence recharts itself applies: `Legend.d.ts` says of
    // `position` "If this is defined, it overrides `align` and `verticalAlign`",
    // and `LegendImpl` only falls back to `getDefaultPosition`, the branch that
    // reads `verticalAlign`, when `props.position == null` (Legend.js). The
    // `= "bottom"` default states recharts' own `legendDefaultProps` value,
    // which is what an unset call site actually delivers here; it does not
    // change the outcome, since a `verticalAlign` of `undefined` takes the same
    // arm as "bottom" — only "top" is a top edge.
    const atTop =
      position === undefined
        ? verticalAlign === "top"
        : typeof position === "string" &&
          TOP_ANCHORED_LEGEND_POSITIONS.includes(position)

    return (
      <div
        ref={ref}
        className={cn(
          // `flex-wrap` is load-bearing, not tidiness. Recharts gives the
          // legend wrapper a FIXED pixel width and this row defaulted to
          // `nowrap`, so once the series outnumbered the space —
          // ~10 expense categories on the owner's ledger, against the two in a
          // dev database — `justify-center` pushed the overflow out of BOTH
          // edges at once and the first and last chips were clipped in half.
          // Wrapping to a second line is the only outcome that keeps every
          // series named. `gap-y` is smaller than `gap-x` because the vertical
          // gap only exists once wrapping happens.
          "flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5",
          // The padding separates the legend from the plot, so it goes on the
          // side facing it: under a legend pinned to the top, above one that
          // sits below or beside the plot. All three production legends set
          // neither placement prop and so land on `pt-3`, which is where
          // `verticalAlign`'s "bottom" default puts them.
          atTop ? "pb-3" : "pt-3",
          className
        )}
      >
        {payload
          .filter((item) => item.type !== "none")
          .map((item, index) => {
            const key = `${nameKey || item.dataKey || "value"}`
            const itemConfig = getPayloadConfigFromPayload(config, item, key)
            // `item.color` is the series' own fill/stroke, which for a
            // gradient-filled series is the literal string `"url(#…)"` — not a
            // CSS colour, so React drops the declaration and the swatch renders
            // fully transparent. Browser-verified on Savings' Year-over-Year:
            // both swatches computed to `rgba(0, 0, 0, 0)` beside three
            // gradient shapes. The config is the same source `ChartStyle` uses
            // to emit `--color-<key>`, so it always holds a real colour when
            // one was declared.
            //
            // `||` rather than `??` on purpose: an empty-string colour is
            // absent, which is how `ChartStyle` treats it (chart.tsx:83). A
            // series with no config colour falls through to exactly the old
            // behaviour.
            //
            // A `{ theme: { light, dark } }` config still renders transparent
            // here; nothing in this app uses that form.
            const swatchColor = itemConfig?.color || item.color

            return (
              // recharts 3 widened `dataKey` to include a function type, and
              // `item.value` — the series NAME, which two series may legitimately
              // share — is not a key either. Same reasoning as
              // `ChartTooltipContent` above: this list is one legend's payload,
              // rendered in place and never reordered. SonarQube S6479 is
              // ACCEPTED here for the same reason it is accepted there. The
              // `ChartLegendContent and ChartTooltipContent React keys` describe
              // block in chart.test.tsx guards BOTH sites against a
              // "stable-looking" key being reinvented — one case each, because
              // a legend case cannot fail for a tooltip mutant: each renders two
              // series that share a name and fails on React's duplicate-key
              // warning.
              //
              // `whitespace-nowrap` keeps a label on one line — but on its own
              // it also makes the chip UNSHRINKABLE, because a flex item's
              // automatic minimum size is its min-content and nowrap raises
              // that to the whole string. Category names are user-supplied and
              // capped at 100 characters server-side, so one long one became a
              // chip wider than the fixed-width legend wrapper and painted
              // outside the card — the same overflow the wrapping above exists
              // to fix, reintroduced for exactly the data that triggers it.
              // `min-w-0` lifts that floor, `max-w-full` caps the chip at the
              // wrapper, `overflow-hidden` contains what is left, and the
              // `truncate` span turns it into an ellipsis rather than a
              // clipped half-glyph.
              <div
                key={index}
                className={cn(
                  "flex min-w-0 max-w-full items-center gap-1.5 overflow-hidden whitespace-nowrap [&>svg]:h-3 [&>svg]:w-3 [&>svg]:text-muted-foreground"
                )}
              >
                {itemConfig?.icon && !hideIcon ? (
                  <itemConfig.icon />
                ) : (
                  <div
                    className="h-2 w-2 shrink-0 rounded-[2px]"
                    style={{
                      backgroundColor: swatchColor,
                    }}
                  />
                )}
                <span className="truncate">{itemConfig?.label}</span>
              </div>
            )
          })}
      </div>
    )
  }
)
ChartLegendContent.displayName = "ChartLegend"

// Helper to extract item config from a payload.
function getPayloadConfigFromPayload(
  config: ChartConfig,
  payload: unknown,
  key: string
) {
  if (typeof payload !== "object" || payload === null) {
    return undefined
  }

  const payloadPayload =
    "payload" in payload &&
    typeof payload.payload === "object" &&
    payload.payload !== null
      ? payload.payload
      : undefined

  let configLabelKey: string = key

  if (
    key in payload &&
    typeof payload[key as keyof typeof payload] === "string"
  ) {
    configLabelKey = payload[key as keyof typeof payload] as string
  } else if (
    payloadPayload &&
    key in payloadPayload &&
    typeof payloadPayload[key as keyof typeof payloadPayload] === "string"
  ) {
    configLabelKey = payloadPayload[
      key as keyof typeof payloadPayload
    ] as string
  }

  return configLabelKey in config
    ? config[configLabelKey]
    : config[key as keyof typeof config]
}

export {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartLegendContent,
  ChartStyle,
}

import { useMemo } from 'react';
import type { CSSProperties, ReactElement } from 'react';
import { useChartTheme } from './useChartTheme';

// === Types ===

export type PatternType = 'solid' | 'stripe' | 'stripe-reverse' | 'dots';

export interface PatternConfig {
  /** SVG fill — solid color or url(#id) */
  fill: string;
  /** SVG stroke for patterned bars */
  stroke?: string;
  strokeWidth?: number;
  /** Base color */
  color: string;
  /** CSS styles for matching legend dot */
  legendStyle: CSSProperties;
}

interface PatternDefInput {
  id: string;
  type: PatternType;
  color: string;
}

// === Helpers ===

function hexToRgba(hex: string, alpha: number): string {
  const m = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  if (!m) return hex;
  return `rgba(${parseInt(m[1], 16)},${parseInt(m[2], 16)},${parseInt(m[3], 16)},${alpha})`;
}

/** Cycle of pattern types for alternating chart segments */
const PATTERN_CYCLE: PatternType[] = [
  'solid', 'stripe', 'solid', 'stripe-reverse', 'solid', 'dots',
];

function makeLegendStyle(type: PatternType, color: string): CSSProperties {
  const semi = hexToRgba(color, 0.4);
  switch (type) {
    case 'solid':
      return { background: color };
    case 'stripe':
      return {
        background: `repeating-linear-gradient(-45deg, transparent, transparent 2px, ${semi} 2px, ${semi} 3.5px)`,
        border: `1.5px solid ${color}`,
      };
    case 'stripe-reverse':
      return {
        background: `repeating-linear-gradient(45deg, transparent, transparent 2px, ${semi} 2px, ${semi} 3.5px)`,
        border: `1.5px solid ${color}`,
      };
    case 'dots':
      return {
        background: `radial-gradient(circle 1px at 3px 3px, ${hexToRgba(color, 0.5)} 1px, transparent 1px)`,
        backgroundSize: '6px 6px',
        border: `1.5px solid ${color}`,
      };
  }
}

// === SVG Pattern Defs Component ===

export function ChartPatternDefs({ patterns }: { patterns: PatternDefInput[] }): ReactElement {
  return (
    <defs>
      {patterns.map((p) => {
        if (p.type === 'solid') return null;
        const bg = hexToRgba(p.color, 0.08);
        switch (p.type) {
          case 'stripe':
            return (
              <pattern key={p.id} id={p.id} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <rect width="6" height="6" fill={bg} />
                <line x1="0" y1="0" x2="0" y2="6" stroke={p.color} strokeWidth="1.5" strokeOpacity="0.5" />
              </pattern>
            );
          case 'stripe-reverse':
            return (
              <pattern key={p.id} id={p.id} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(-45)">
                <rect width="6" height="6" fill={bg} />
                <line x1="0" y1="0" x2="0" y2="6" stroke={p.color} strokeWidth="1.5" strokeOpacity="0.5" />
              </pattern>
            );
          case 'dots':
            return (
              <pattern key={p.id} id={p.id} width="6" height="6" patternUnits="userSpaceOnUse">
                <rect width="6" height="6" fill={hexToRgba(p.color, 0.05)} />
                <circle cx="3" cy="3" r="1" fill={p.color} fillOpacity="0.5" />
              </pattern>
            );
          default:
            return null;
        }
      })}
    </defs>
  );
}

// === Hook ===

export function useChartPatterns() {
  const { incomeColor } = useChartTheme();

  return useMemo(() => {
    // Cash flow: income solid, expense striped (same brand color, differentiated by pattern)
    const cashFlow = {
      income: {
        fill: incomeColor,
        color: incomeColor,
        legendStyle: { background: incomeColor } as CSSProperties,
      },
      expense: {
        fill: 'url(#cf-stripe)',
        stroke: incomeColor,
        strokeWidth: 1.5,
        color: incomeColor,
        legendStyle: makeLegendStyle('stripe', incomeColor),
      },
    };

    const cashFlowDefs: PatternDefInput[] = [
      { id: 'cf-stripe', type: 'stripe', color: incomeColor },
    ];

    // Category patterns: alternate solid/patterned for visual differentiation
    function getCategoryPattern(index: number, color: string): PatternConfig {
      const type = PATTERN_CYCLE[index % PATTERN_CYCLE.length];
      if (type === 'solid') {
        return { fill: color, color, legendStyle: { background: color } };
      }
      const id = `cat-${index}`;
      return {
        fill: `url(#${id})`,
        stroke: color,
        strokeWidth: 1,
        color,
        legendStyle: makeLegendStyle(type, color),
      };
    }

    function getCategoryDefs(items: { color: string }[]): PatternDefInput[] {
      return items.map((item, i) => ({
        id: `cat-${i}`,
        type: PATTERN_CYCLE[i % PATTERN_CYCLE.length],
        color: item.color,
      }));
    }

    // Build a fill→legendStyle map for ChartTooltip
    function buildStyleMap(patterns: PatternConfig[]): Record<string, CSSProperties> {
      const map: Record<string, CSSProperties> = {};
      for (const p of patterns) {
        map[p.fill] = p.legendStyle;
      }
      return map;
    }

    return {
      cashFlow,
      cashFlowDefs,
      getCategoryPattern,
      getCategoryDefs,
      buildStyleMap,
    };
  }, [incomeColor]);
}
